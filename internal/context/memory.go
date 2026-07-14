package context

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnson-xue/summoner/internal/database"
	"github.com/johnson-xue/summoner/internal/llm"
)

const ChunkSize = 64 * 1024 // 64KB per chunk

// Memory manages workflow context persistence
type Memory struct {
	db          *database.DB
	llmClient   *llm.Client
	projectName string
	projectHash string // Use hash to avoid name conflicts
}

// NewMemory creates a new context memory manager
func NewMemory(projectName string) (*Memory, error) {
	if err := validateProjectName(projectName); err != nil {
		return nil, err
	}

	// FIX: Use project hash to avoid name conflicts
	// If two projects have same name but different paths, they get different DBs
	projectHash := hashProjectName(projectName)

	// Get database path (with env override support)
	dbPath := getDatabasePath(projectHash)

	// Open database
	db, err := database.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Try to create LLM client (failure is non-fatal)
	llmClient, err := llm.NewClient()
	if err != nil {
		log.Printf("Warning: LLM client creation failed: %v (will use fallback)", err)
		llmClient = nil // Will trigger fallback on extraction
	}

	return &Memory{
		db:          db,
		llmClient:   llmClient,
		projectName: projectName,
		projectHash: projectHash,
	}, nil
}

// Close closes the database connection
func (m *Memory) Close() error {
	return m.db.Close()
}

// GetDB returns the underlying database (for advanced operations)
func (m *Memory) GetDB() *database.DB {
	return m.db
}

// getDatabasePath returns the database file path
// Security: validates SUMMONER_DB_PATH to prevent path traversal attacks
func getDatabasePath(projectHash string) string {
	// Check environment variable override
	if basePath := os.Getenv("SUMMONER_DB_PATH"); basePath != "" {
		// Validate path to prevent path traversal
		if err := validateDatabaseBasePath(basePath); err != nil {
			log.Printf("Warning: invalid SUMMONER_DB_PATH (%v), using default", err)
			return getDefaultDatabasePath(projectHash)
		}
		return filepath.Join(basePath, projectHash+".db")
	}

	return getDefaultDatabasePath(projectHash)
}

// getDefaultDatabasePath returns the default database path
func getDefaultDatabasePath(projectHash string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Security: fail fast instead of using current directory
		log.Fatalf("Fatal: cannot determine home directory: %v", err)
	}

	return filepath.Join(home, ".claude", "plugins", "summoner", "memory", projectHash+".db")
}

// validateDatabaseBasePath validates the base path for security
func validateDatabaseBasePath(basePath string) error {
	// Must be absolute path
	if !filepath.IsAbs(basePath) {
		return fmt.Errorf("path must be absolute")
	}

	// Resolve to clean absolute path
	absPath, err := filepath.Abs(basePath)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}

	// Check for path traversal attempts
	cleanPath := filepath.Clean(basePath)
	if cleanPath != basePath {
		return fmt.Errorf("path contains traversal elements")
	}

	// Ensure directory exists or can be created
	if err := os.MkdirAll(absPath, 0755); err != nil {
		return fmt.Errorf("cannot create directory: %w", err)
	}

	return nil
}

// validateProjectName validates the project name for security
// Addresses S5: Insufficient Input Validation in Project Name [MEDIUM]
func validateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("project name cannot be empty")
	}

	// Length limit
	if len(name) > 255 {
		return fmt.Errorf("project name too long (max 255 chars)")
	}

	// Disallow path separators and special chars
	invalidChars := []string{"/", "\\", "..", "\x00", "\n", "\r"}
	for _, char := range invalidChars {
		if strings.Contains(name, char) {
			return fmt.Errorf("project name contains invalid character: %s", char)
		}
	}

	// Disallow leading/trailing whitespace
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("project name has leading/trailing whitespace")
	}

	return nil
}

// hashProjectName creates a hash of project name for safe filename
// FIX: Prevents conflicts between projects with same name but different paths
func hashProjectName(projectName string) string {
	// Use SHA256 hash of the absolute project path
	absPath, err := filepath.Abs(projectName)
	if err != nil {
		absPath = projectName
	}

	hash := sha256.Sum256([]byte(absPath))
	return hex.EncodeToString(hash[:8]) // Use first 8 bytes (16 hex chars)
}

// StartWorkflow creates a new workflow
func (m *Memory) StartWorkflow(command, userInput string) (string, error) {
	workflowID := generateWorkflowID(command)

	_, err := m.db.Exec(`
		INSERT INTO workflows (id, command, user_input, status)
		VALUES (?, ?, ?, 'running')
	`, workflowID, command, userInput)

	if err != nil {
		return "", fmt.Errorf("create workflow: %w", err)
	}

	return workflowID, nil
}

// generateWorkflowID creates a unique workflow ID
func generateWorkflowID(command string) string {
	// Extract command type: /summoner:fix -> fix
	parts := strings.Split(command, ":")
	cmdType := "workflow"
	if len(parts) > 1 {
		cmdType = parts[1]
	}

	// Timestamp
	now := time.Now()
	timestamp := now.Format("20060102-150405")

	return fmt.Sprintf("%s-%s", cmdType, timestamp)
}

// SavePhase saves a phase output with LLM-extracted summary
// FIX: LLM extraction outside transaction to reduce lock time
// FIX: Supports large files with async batch insert (future optimization)
func (m *Memory) SavePhase(req SavePhaseRequest) (int64, error) {
	// Step 1: Extract summary (outside transaction, may take 10-60s)
	var summary string
	var score int
	var tokenCost int

	result, err := m.extractSummary(req.FullOutput, req.ProjectGuide)
	if err != nil {
		log.Printf("LLM extraction failed: %v, using fallback", err)
		summary = fallbackSummary(req.FullOutput)
		score = 0 // 0 indicates fallback
		tokenCost = 0
	} else {
		summary = result.Summary
		score = result.Score
		if result.TokenUsage != nil {
			tokenCost = result.TokenUsage.Total
		}
	}

	// Step 2: Database operations (in transaction, fast)
	tx, err := m.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // Auto-rollback on error

	// Get next sequence number
	var sequence int
	err = tx.QueryRow(`
		SELECT COALESCE(MAX(sequence), 0) + 1
		FROM phases WHERE workflow_id = ?
	`, req.WorkflowID).Scan(&sequence)
	if err != nil {
		return 0, fmt.Errorf("get sequence: %w", err)
	}

	// Insert phase record
	res, err := tx.Exec(`
		INSERT INTO phases
		(workflow_id, phase_name, skill_name, sequence, summary,
		 summary_score, full_output_size, token_cost, status, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'completed', CURRENT_TIMESTAMP)
	`, req.WorkflowID, req.PhaseName, req.SkillName, sequence,
		summary, score, len(req.FullOutput), tokenCost)
	if err != nil {
		return 0, fmt.Errorf("insert phase: %w", err)
	}

	phaseID, _ := res.LastInsertId()

	// Save output chunks
	if err := m.saveChunks(tx, phaseID, req.FullOutput); err != nil {
		return 0, fmt.Errorf("save chunks: %w", err)
	}

	// Update chunk count
	chunkCount := (len(req.FullOutput) + ChunkSize - 1) / ChunkSize
	_, err = tx.Exec(`
		UPDATE phases SET full_output_chunks = ? WHERE id = ?
	`, chunkCount, phaseID)
	if err != nil {
		return 0, fmt.Errorf("update chunk count: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}

	log.Printf("Phase saved: id=%d, sequence=%d, summary_score=%d/5, token_cost=%d",
		phaseID, sequence, score, tokenCost)

	return phaseID, nil
}

// saveChunks saves full output in chunks
// FIX: Batch insert for large files (>1MB) to improve performance
func (m *Memory) saveChunks(tx *sql.Tx, phaseID int64, fullOutput string) error {
	if len(fullOutput) == 0 {
		return nil
	}

	// For small outputs, use simple insert
	if len(fullOutput) <= ChunkSize {
		_, err := tx.Exec(`
			INSERT INTO phase_output_chunks (phase_id, chunk_index, content)
			VALUES (?, 0, ?)
		`, phaseID, fullOutput)
		return err
	}

	// For large outputs, batch insert (reduce transaction overhead)
	chunks := make([]string, 0, (len(fullOutput)+ChunkSize-1)/ChunkSize)
	for i := 0; i < len(fullOutput); i += ChunkSize {
		end := i + ChunkSize
		if end > len(fullOutput) {
			end = len(fullOutput)
		}
		chunks = append(chunks, fullOutput[i:end])
	}

	// Insert in batches of 100 chunks
	const batchSize = 100
	for i := 0; i < len(chunks); i += batchSize {
		end := i + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}

		// Build batch insert
		var values []interface{}
		placeholders := make([]string, 0, end-i)
		for idx, chunk := range chunks[i:end] {
			placeholders = append(placeholders, "(?, ?, ?)")
			values = append(values, phaseID, i+idx, chunk)
		}

		query := fmt.Sprintf(`
			INSERT INTO phase_output_chunks (phase_id, chunk_index, content)
			VALUES %s
		`, strings.Join(placeholders, ", "))

		if _, err := tx.Exec(query, values...); err != nil {
			return fmt.Errorf("batch insert chunks: %w", err)
		}
	}

	return nil
}

// GetPhase retrieves a phase by ID
func (m *Memory) GetPhase(phaseID int64) (*Phase, error) {
	var p Phase
	err := m.db.QueryRow(`
		SELECT id, workflow_id, phase_name, skill_name, sequence,
		       summary, summary_score, summary_edited, full_output_size,
		       full_output_chunks, token_cost, status, started_at, completed_at
		FROM phases WHERE id = ?
	`, phaseID).Scan(
		&p.ID, &p.WorkflowID, &p.PhaseName, &p.SkillName, &p.Sequence,
		&p.Summary, &p.SummaryScore, &p.SummaryEdited, &p.FullOutputSize,
		&p.FullOutputChunks, &p.TokenCost, &p.Status, &p.StartedAt, &p.CompletedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get phase: %w", err)
	}

	return &p, nil
}

// GetSummary retrieves a phase summary
func (m *Memory) GetSummary(phaseID int64) (string, error) {
	var summary string
	err := m.db.QueryRow(`
		SELECT summary FROM phases WHERE id = ?
	`, phaseID).Scan(&summary)
	if err != nil {
		return "", fmt.Errorf("get summary: %w", err)
	}

	return summary, nil
}

// GetFullOutput retrieves the complete phase output
func (m *Memory) GetFullOutput(phaseID int64) (string, error) {
	rows, err := m.db.Query(`
		SELECT content FROM phase_output_chunks
		WHERE phase_id = ?
		ORDER BY chunk_index
	`, phaseID)
	if err != nil {
		return "", fmt.Errorf("query chunks: %w", err)
	}
	defer rows.Close()

	var builder strings.Builder
	for rows.Next() {
		var chunk string
		if err := rows.Scan(&chunk); err != nil {
			return "", fmt.Errorf("scan chunk: %w", err)
		}
		builder.WriteString(chunk)
	}

	return builder.String(), rows.Err()
}

// GetContextChain retrieves all phases up to a sequence number
func (m *Memory) GetContextChain(workflowID string, upToSequence int) ([]Phase, error) {
	query := `
		SELECT id, workflow_id, phase_name, skill_name, sequence,
		       summary, summary_score, summary_edited, full_output_size,
		       full_output_chunks, token_cost, status
		FROM phases
		WHERE workflow_id = ? AND status = 'completed'
	`
	args := []interface{}{workflowID}

	if upToSequence > 0 {
		query += " AND sequence <= ?"
		args = append(args, upToSequence)
	}

	query += " ORDER BY sequence"

	rows, err := m.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query context chain: %w", err)
	}
	defer rows.Close()

	var phases []Phase
	for rows.Next() {
		var p Phase
		err := rows.Scan(&p.ID, &p.WorkflowID, &p.PhaseName, &p.SkillName,
			&p.Sequence, &p.Summary, &p.SummaryScore, &p.SummaryEdited,
			&p.FullOutputSize, &p.FullOutputChunks, &p.TokenCost, &p.Status)
		if err != nil {
			return nil, fmt.Errorf("scan phase: %w", err)
		}
		phases = append(phases, p)
	}

	return phases, rows.Err()
}

// EditSummary updates a phase summary with user intervention tracking
func (m *Memory) EditSummary(phaseID int64, newSummary, reason string) error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Get old summary
	var oldSummary string
	err = tx.QueryRow(`
		SELECT summary FROM phases WHERE id = ?
	`, phaseID).Scan(&oldSummary)
	if err != nil {
		return fmt.Errorf("get old summary: %w", err)
	}

	// Record intervention
	_, err = tx.Exec(`
		INSERT INTO interventions
		(phase_id, intervention_type, field_name, before_value, after_value, reason)
		VALUES (?, 'edit_summary', 'summary', ?, ?, ?)
	`, phaseID, oldSummary, newSummary, reason)
	if err != nil {
		return fmt.Errorf("record intervention: %w", err)
	}

	// Update summary
	_, err = tx.Exec(`
		UPDATE phases
		SET summary = ?, summary_edited = 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, newSummary, phaseID)
	if err != nil {
		return fmt.Errorf("update summary: %w", err)
	}

	return tx.Commit()
}
