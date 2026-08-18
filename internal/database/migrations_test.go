package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrate_v4_OnPopulatedTable is the S8 BLOCKER regression test: the
// original v4 used "ALTER TABLE phases ADD COLUMN updated_at TIMESTAMP
// DEFAULT CURRENT_TIMESTAMP", which SQLite rejects ("Cannot add a column
// with non-constant default") when phases has any rows. That permanently
// bricked the memory layer on upgrade of any populated production DB: v4
// never recorded, every Open() retried + failed. This test seeds a DB at
// schema v3 WITH a phase row, then re-opens (re-running Migrate) and
// asserts v4 applies and the column is usable.
func TestMigrate_v4_OnPopulatedTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// First open: applies all migrations v1-v4 on an empty phases table.
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	// Seed a phase row so the table is populated, then close. This simulates
	// a real production DB that recorded at least one workflow.
	if _, err := db.Exec(`INSERT INTO phases (workflow_id, phase_name, skill_name, sequence) VALUES (?, ?, ?, ?)`,
		"wf-test", "diagnose", "phase.debug", 1); err != nil {
		t.Fatalf("seed phase: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Simulate the bug: revert to schema v3 by deleting the v4 migration
	// record, so the next Open() will re-apply v4 — but now on a POPULATED
	// table (the blocker scenario). Drop the updated_at column too so v4's
	// ALTER has work to do (mimicking a pre-v4 DB).
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	// SQLite has no DROP COLUMN in 3.39 without the full table-rebuild; the
	// updated_at column may or may not exist depending on whether the first
	// Open applied v4. To force the blocker scenario cleanly, recreate the
	// DB from scratch at v3 with a row. Simpler: delete the v4 record and
	// verify re-Open still succeeds (if v4 was applied, Migrate skips it; if
	// we also drop the column, v4 re-runs). Use the duplicate-column path
	// test below for the idempotency scenario; here, delete the v4 record
	// AND drop+rebuild is overkill — instead, test on a fresh v3 DB.
	raw.Close()

	// Cleaner scenario: build a v3-only DB with a populated phases table,
	// then Open() (which runs Migrate including v4) must succeed.
	v3Path := filepath.Join(t.TempDir(), "v3only.db")
	buildV3DBWithPhase(t, v3Path)

	db2, err := Open(v3Path)
	if err != nil {
		t.Fatalf("Open on populated v3 DB (BLOCKER regression): %v — the old v4 would brick here with 'Cannot add a column with non-constant default'", err)
	}
	defer db2.Close()

	// v4 applied: updated_at column exists and is usable by EditSummary's UPDATE.
	var colName string
	err = db2.QueryRow(`PRAGMA table_info(phases)`).Scan() // multi-row; just ensure query runs
	_ = colName
	// Verify the column exists by inserting+updating.
	res, err := db2.Exec(`UPDATE phases SET updated_at = CURRENT_TIMESTAMP WHERE workflow_id = ?`, "wf-v3")
	if err != nil {
		t.Fatalf("EditSummary-style UPDATE on updated_at failed: %v — column not added", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		t.Fatalf("expected 1 row updated, got %d", n)
	}
}

// TestMigrate_v4_Idempotent_PreAddedColumn tests the S8 idempotency finding:
// if updated_at was added out-of-band (manual fix) while v4 was unrecorded,
// Migrate must treat "duplicate column name" as success (column exists =
// goal met) and record v4 — NOT permanently brick the memory layer.
func TestMigrate_v4_Idempotent_PreAddedColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "preadd.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	// Manually delete the v4 migration record AND leave the column in place,
	// so the next Migrate() will re-run v4's ALTER and hit "duplicate column
	// name". The idempotency tolerance must catch it.
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 4`); err != nil {
		t.Fatalf("delete v4 record: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Re-open: Migrate re-runs v4 → ALTER errors "duplicate column name" →
	// idempotency tolerance records v4 as applied.
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("re-Open after manual pre-add (idempotency regression): %v — the old code bricked here with 'migration v4 failed: duplicate column name'", err)
	}
	defer db2.Close()

	// v4 now recorded: a further re-open is a true no-op (skips v4).
	var v int
	if err := db2.QueryRow(`SELECT version FROM schema_migrations WHERE version = 4`).Scan(&v); err != nil {
		t.Fatalf("v4 should be recorded after idempotent recovery: %v", err)
	}
}

// buildV3DBWithPhase creates a DB with migrations v1-v3 applied (v4 absent)
// and one phase row, simulating a real pre-v0.1.8 production DB.
func buildV3DBWithPhase(t *testing.T, dbPath string) {
	t.Helper()
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	defer raw.Close()
	// The Migrate() wrapper creates schema_migrations in its step 1; since we
	// apply migration SQL directly (bypassing Migrate), create it ourselves.
	if _, err := raw.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		description TEXT,
		applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	// Run migrations v1, v2, v3 SQL directly (skip v4).
	for _, m := range migrations {
		if m.Version > 3 {
			continue
		}
		if _, err := raw.Exec(m.SQL); err != nil {
			t.Fatalf("apply v%d: %v", m.Version, err)
		}
	}
	// Record v1-v3 as applied.
	for v := 1; v <= 3; v++ {
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, v); err != nil {
			t.Fatalf("record v%d: %v", v, err)
		}
	}
	// Seed a phase row.
	if _, err := raw.Exec(`INSERT INTO phases (workflow_id, phase_name, skill_name, sequence) VALUES (?, ?, ?, ?)`,
		"wf-v3", "diagnose", "phase.debug", 1); err != nil {
		t.Fatalf("seed phase: %v", err)
	}
}
