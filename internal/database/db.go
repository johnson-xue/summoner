package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps sql.DB with additional functionality
type DB struct {
	*sql.DB
	closed bool
	mu     sync.RWMutex
}

// Open opens a SQLite database with optimized settings
func Open(dbPath string) (*DB, error) {
	// 0. Validate database path
	if dbPath == "" {
		return nil, fmt.Errorf("database path cannot be empty")
	}

	if strings.Contains(dbPath, "?") {
		return nil, fmt.Errorf("database path cannot contain '?': %s", dbPath)
	}

	// Ensure parent directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	// 1. Build DSN with optimizations
	// _journal_mode=WAL: Write-Ahead Logging (better concurrency)
	// _busy_timeout=5000: Wait 5s on lock conflicts
	// cache=shared: Share cache between connections
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=5000&cache=shared", dbPath)

	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// 2. Configure connection pool
	sqlDB.SetMaxOpenConns(10)               // Max 10 concurrent connections
	sqlDB.SetMaxIdleConns(5)                // Keep 5 idle connections
	sqlDB.SetConnMaxLifetime(time.Hour)     // Recycle connections after 1 hour

	// 3. Apply SQLite pragmas for performance
	pragmas := []string{
		"PRAGMA synchronous = NORMAL",      // Balance performance and safety
		"PRAGMA cache_size = -64000",       // 64MB cache
		"PRAGMA temp_store = MEMORY",       // Temp tables in memory
		"PRAGMA mmap_size = 268435456",     // 256MB memory-mapped I/O
	}

	for _, pragma := range pragmas {
		if _, err := sqlDB.Exec(pragma); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("set pragma %s: %w", pragma, err)
		}
	}

	db := &DB{DB: sqlDB}

	// 4. Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	// 5. Apply migrations
	if err := db.Migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	log.Printf("Database opened: %s", dbPath)
	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.closed {
		return nil
	}

	db.closed = true
	log.Printf("Database closed")
	return db.DB.Close()
}

// Ping checks if the database connection is alive
func (db *DB) Ping() error {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.closed {
		return fmt.Errorf("database closed")
	}

	return db.DB.Ping()
}

// IsClosed returns whether the database is closed
func (db *DB) IsClosed() bool {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.closed
}

// Exec wraps sql.DB.Exec with closed check
func (db *DB) Exec(query string, args ...interface{}) (sql.Result, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.closed {
		return nil, fmt.Errorf("database closed")
	}

	return db.DB.Exec(query, args...)
}

// Query wraps sql.DB.Query with closed check
func (db *DB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.closed {
		return nil, fmt.Errorf("database closed")
	}

	return db.DB.Query(query, args...)
}

// QueryRow wraps sql.DB.QueryRow with closed check
func (db *DB) QueryRow(query string, args ...interface{}) *sql.Row {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.closed {
		// Return a Row that will error on Scan
		return db.DB.QueryRow("SELECT NULL WHERE 1=0")
	}

	return db.DB.QueryRow(query, args...)
}

// Begin wraps sql.DB.Begin with closed check
func (db *DB) Begin() (*sql.Tx, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.closed {
		return nil, fmt.Errorf("database closed")
	}

	return db.DB.Begin()
}
