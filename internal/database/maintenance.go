package database

import (
	"database/sql"
	"fmt"
	"time"
)

// DBStats represents database statistics
type DBStats struct {
	TotalWorkflows  int
	TotalPhases     int
	TotalChunks     int
	DatabaseSize    int64
	OldestWorkflow  *time.Time
}

// CleanupOldWorkflows deletes workflows older than specified days
func (db *DB) CleanupOldWorkflows(olderThanDays int) (int, error) {
	result, err := db.Exec(`
		DELETE FROM workflows
		WHERE created_at < datetime('now', '-' || ? || ' days')
	`, olderThanDays)
	if err != nil {
		return 0, fmt.Errorf("cleanup workflows: %w", err)
	}

	count, _ := result.RowsAffected()
	return int(count), nil
}

// CleanupOrphanedChunks removes chunks whose phase has been deleted
func (db *DB) CleanupOrphanedChunks() (int, error) {
	result, err := db.Exec(`
		DELETE FROM phase_output_chunks
		WHERE phase_id NOT IN (SELECT id FROM phases)
	`)
	if err != nil {
		return 0, fmt.Errorf("cleanup orphaned chunks: %w", err)
	}

	count, _ := result.RowsAffected()
	return int(count), nil
}

// Vacuum compacts the database and frees unused space
func (db *DB) Vacuum() error {
	_, err := db.Exec("VACUUM")
	if err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	return nil
}

// Analyze updates query optimizer statistics
func (db *DB) Analyze() error {
	_, err := db.Exec("ANALYZE")
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	return nil
}

// GetStats returns database statistics
func (db *DB) GetStats() (*DBStats, error) {
	stats := &DBStats{}

	// Total workflows
	err := db.QueryRow("SELECT COUNT(*) FROM workflows").Scan(&stats.TotalWorkflows)
	if err != nil {
		return nil, fmt.Errorf("count workflows: %w", err)
	}

	// Total phases
	err = db.QueryRow("SELECT COUNT(*) FROM phases").Scan(&stats.TotalPhases)
	if err != nil {
		return nil, fmt.Errorf("count phases: %w", err)
	}

	// Total chunks
	err = db.QueryRow("SELECT COUNT(*) FROM phase_output_chunks").Scan(&stats.TotalChunks)
	if err != nil {
		return nil, fmt.Errorf("count chunks: %w", err)
	}

	// Database size
	err = db.QueryRow(`
		SELECT page_count * page_size
		FROM pragma_page_count(), pragma_page_size()
	`).Scan(&stats.DatabaseSize)
	if err != nil {
		return nil, fmt.Errorf("get database size: %w", err)
	}

	// Oldest workflow
	var oldestStr sql.NullString
	err = db.QueryRow("SELECT MIN(created_at) FROM workflows").Scan(&oldestStr)
	if err != nil {
		return nil, fmt.Errorf("get oldest workflow: %w", err)
	}
	if oldestStr.Valid {
		oldest, _ := time.Parse("2006-01-02 15:04:05", oldestStr.String)
		stats.OldestWorkflow = &oldest
	}

	return stats, nil
}
