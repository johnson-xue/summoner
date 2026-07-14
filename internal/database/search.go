package database

import (
	"database/sql"
	"log"
	"strings"
)

// SearchPhases searches phases using full-text search
func (db *DB) SearchPhases(keyword, workflowID string) ([]PhaseSearchResult, error) {
	// Try FTS search first
	results, err := db.searchWithFTS(keyword, workflowID)
	if err == nil {
		return results, nil
	}

	log.Printf("FTS search failed: %v, falling back to LIKE search", err)

	// Fallback to LIKE search
	return db.searchWithLike(keyword, workflowID)
}

// PhaseSearchResult represents a search result
type PhaseSearchResult struct {
	ID             int
	WorkflowID     string
	PhaseName      string
	SkillName      string
	Sequence       int
	Summary        string
	SummaryScore   int
	FullOutputSize int
}

func (db *DB) searchWithFTS(keyword, workflowID string) ([]PhaseSearchResult, error) {
	// Prepare search term for FTS5
	searchTerm := prepareSearchTerm(keyword)

	query := `
		SELECT p.id, p.workflow_id, p.phase_name, p.skill_name,
		       p.sequence, p.summary, p.summary_score, p.full_output_size
		FROM phases p
		JOIN phases_fts fts ON p.id = fts.rowid
		WHERE phases_fts MATCH ?
	`
	args := []interface{}{searchTerm}

	if workflowID != "" {
		query += " AND p.workflow_id = ?"
		args = append(args, workflowID)
	}

	query += " ORDER BY p.sequence"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPhaseResults(rows)
}

func (db *DB) searchWithLike(keyword, workflowID string) ([]PhaseSearchResult, error) {
	query := `
		SELECT id, workflow_id, phase_name, skill_name, sequence,
		       summary, summary_score, full_output_size
		FROM phases
		WHERE (phase_name LIKE ? OR skill_name LIKE ? OR summary LIKE ?)
	`
	pattern := "%" + keyword + "%"
	args := []interface{}{pattern, pattern, pattern}

	if workflowID != "" {
		query += " AND workflow_id = ?"
		args = append(args, workflowID)
	}

	query += " ORDER BY sequence"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPhaseResults(rows)
}

func scanPhaseResults(rows *sql.Rows) ([]PhaseSearchResult, error) {
	var results []PhaseSearchResult

	for rows.Next() {
		var r PhaseSearchResult
		err := rows.Scan(&r.ID, &r.WorkflowID, &r.PhaseName, &r.SkillName,
			&r.Sequence, &r.Summary, &r.SummaryScore, &r.FullOutputSize)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}

	return results, rows.Err()
}

// prepareSearchTerm converts keyword to FTS5 search syntax
func prepareSearchTerm(keyword string) string {
	// For Chinese: split by character and OR them
	if containsChinese(keyword) {
		runes := []rune(keyword)
		terms := make([]string, 0, len(runes))
		for _, r := range runes {
			if r >= 0x4e00 && r <= 0x9fa5 { // Chinese character range
				terms = append(terms, string(r))
			}
		}
		if len(terms) > 0 {
			return strings.Join(terms, " OR ")
		}
	}

	// For English: add prefix wildcard for partial matching
	return keyword + "*"
}

func containsChinese(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fa5 {
			return true
		}
	}
	return false
}
