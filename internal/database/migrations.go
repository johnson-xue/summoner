package database

import (
	"fmt"
	"log"
	"strings"
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
	{
		Version:     4,
		Description: "Add updated_at to phases (EditSummary writes it; was missing → edit feature errored 'no such column: updated_at')",
		SQL: `
-- phases.updated_at was referenced by EditSummary (memory.go) but never declared;
-- only workflows carried updated_at. This migration adds the column so the edit
-- feature works.
--
-- BLOCKER FIX (S8 review): the original v4 used "DEFAULT CURRENT_TIMESTAMP",
-- but SQLite forbids a NON-CONSTANT default in ALTER TABLE ADD COLUMN when the
-- table already has rows — it errors "Cannot add a column with non-constant
-- default" and rolls back, permanently bricking the memory layer on upgrade of
-- any populated production DB (v4 never records, every Open() retries + fails).
-- The DEFAULT was never load-bearing: EditSummary sets updated_at = CURRENT_TIMESTAMP
-- explicitly on every edit, so rows that are never edited simply carry NULL (they
-- predate the edit feature — NULL is the honest value). Dropping the DEFAULT makes
-- the ALTER succeed on a populated table.
--
-- IDEMPOTENCY: SQLite ADD COLUMN has no IF NOT EXISTS, so if the column was added
-- out-of-band (manual fix) while v4 was unrecorded, the ALTER errors "duplicate
-- column name". Migrate() now treats that specific error as success (the column
-- exists — the migration's goal is met) and records v4, so re-runs are a true
-- no-op rather than a permanent-failure landmine.
ALTER TABLE phases ADD COLUMN updated_at TIMESTAMP;
		`,
	},
}

// isDuplicateColumnError reports whether err is SQLite's "duplicate column
// name" error from ALTER TABLE ADD COLUMN on a column that already exists.
// Used to make ADD-COLUMN migrations idempotent against out-of-band pre-adds.
func isDuplicateColumnError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
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
			// Idempotency tolerance (S8 review): SQLite ADD COLUMN has no IF NOT
			// EXISTS, so a migration that adds a column errors "duplicate column
			// name" if the column was added out-of-band (manual fix) while the
			// migration was unrecorded. That error means the migration's GOAL is
			// already met (the column exists), so treat it as success: roll back
			// the failed tx and record the migration as applied in a fresh tx so
			// future Open() calls skip it. Without this, a single manual pre-add
			// permanently bricked the memory layer (v4 retried + failed forever).
			if isDuplicateColumnError(err) {
				log.Printf("Migration v%d: column already exists (duplicate column name) — treating as applied", m.Version)
				tx.Rollback()
				if _, err := db.Exec(`INSERT INTO schema_migrations (version, description) VALUES (?, ?)`, m.Version, m.Description); err != nil {
					return fmt.Errorf("record migration v%d (duplicate-column path): %w", m.Version, err)
				}
				continue
			}
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
