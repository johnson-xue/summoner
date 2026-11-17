package database

import (
	"fmt"
	"log"
)

// Migration represents a database migration
type Migration struct {
	Version     int
	Description string
	SQL         string
}

// WARNING: migrations must be hardcoded in source code, never loaded from external files
// to prevent SQL injection attacks
var migrations = []Migration{
	{
		Version:     1,
		Description: "Initial schema",
		SQL: `
-- Workflows table
CREATE TABLE IF NOT EXISTS workflows (
    id TEXT PRIMARY KEY,
    command TEXT NOT NULL,
    user_input TEXT,
    status TEXT DEFAULT 'running',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Phases table
CREATE TABLE IF NOT EXISTS phases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workflow_id TEXT NOT NULL,
    phase_name TEXT NOT NULL,
    skill_name TEXT NOT NULL,
    sequence INTEGER NOT NULL,
    status TEXT DEFAULT 'running',
    summary TEXT,
    summary_score INTEGER,
    summary_edited INTEGER DEFAULT 0,
    full_output_size INTEGER DEFAULT 0,
    full_output_chunks INTEGER DEFAULT 0,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,
    FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
);

-- Phase output chunks (for large outputs)
CREATE TABLE IF NOT EXISTS phase_output_chunks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    phase_id INTEGER NOT NULL,
    chunk_index INTEGER NOT NULL,
    content TEXT NOT NULL,
    FOREIGN KEY (phase_id) REFERENCES phases(id) ON DELETE CASCADE,
    UNIQUE(phase_id, chunk_index)
);

-- Context transfers (track information flow)
CREATE TABLE IF NOT EXISTS context_transfers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_phase_id INTEGER,
    to_phase_id INTEGER NOT NULL,
    content_type TEXT NOT NULL,
    content_preview TEXT,
    transferred_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (from_phase_id) REFERENCES phases(id) ON DELETE CASCADE,
    FOREIGN KEY (to_phase_id) REFERENCES phases(id) ON DELETE CASCADE
);

-- Interventions (user edits, manual corrections)
CREATE TABLE IF NOT EXISTS interventions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    phase_id INTEGER NOT NULL,
    intervention_type TEXT NOT NULL,
    field_name TEXT,
    before_value TEXT,
    after_value TEXT,
    reason TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (phase_id) REFERENCES phases(id) ON DELETE CASCADE
);

-- Indexes for performance
CREATE INDEX IF NOT EXISTS idx_phases_workflow ON phases(workflow_id, sequence);
CREATE INDEX IF NOT EXISTS idx_phases_status ON phases(status);
CREATE INDEX IF NOT EXISTS idx_context_to ON context_transfers(to_phase_id);
CREATE INDEX IF NOT EXISTS idx_interventions_phase ON interventions(phase_id);
		`,
	},
	{
		Version:     2,
		Description: "Add token_cost tracking",
		SQL: `
ALTER TABLE phases ADD COLUMN token_cost INTEGER DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_phases_token ON phases(token_cost);
		`,
	},
	{
		Version:     3,
		Description: "Add full-text search with triggers",
		SQL: `
-- Try to create FTS5 virtual table (may fail if FTS5 not available)
-- This is optional - the tool will work without FTS5

-- Note: FTS5 may not be available in all SQLite builds
-- If this migration fails, search will fall back to LIKE queries
		`,
	},
}

// Migrate applies all pending migrations
func (db *DB) Migrate() error {
	// 1. Create migrations table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			description TEXT,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	// 2. Get current version
	var currentVersion int
	err = db.QueryRow(`
		SELECT COALESCE(MAX(version), 0) FROM schema_migrations
	`).Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("get current version: %w", err)
	}

	if currentVersion > 0 {
		log.Printf("Database current version: %d", currentVersion)
	}

	// 3. Apply pending migrations
	for _, m := range migrations {
		if m.Version <= currentVersion {
			continue
		}

		log.Printf("Applying migration v%d: %s", m.Version, m.Description)

		// Execute in transaction
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}

		// Execute migration SQL
		if _, err := tx.Exec(m.SQL); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v%d failed: %w", m.Version, err)
		}

		// Record migration
		_, err = tx.Exec(`
			INSERT INTO schema_migrations (version, description) VALUES (?, ?)
		`, m.Version, m.Description)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration v%d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration v%d: %w", m.Version, err)
		}

		log.Printf("✓ Migration v%d applied", m.Version)
	}

	if currentVersion == 0 {
		log.Printf("Database initialized with %d migrations", len(migrations))
	}

	return nil
}

// GetCurrentVersion returns the current migration version
func (db *DB) GetCurrentVersion() (int, error) {
	var version int
	err := db.QueryRow(`
		SELECT COALESCE(MAX(version), 0) FROM schema_migrations
	`).Scan(&version)
	return version, err
}
