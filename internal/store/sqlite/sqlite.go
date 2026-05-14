package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite database connection.
type DB struct {
	mu sync.RWMutex
	db *sql.DB
}

// Open opens or creates a SQLite database at the given path.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("sqlite: mkdir: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}

	// Set pragmas after opening (must be separate calls)
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("sqlite: pragma journal_mode: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA busy_timeout=5000"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("sqlite: pragma busy_timeout: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}

	return &DB{db: sqlDB}, nil
}

// Migrate creates all required tables.
func (d *DB) Migrate() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(migrateSQL)
	if err != nil {
		return fmt.Errorf("sqlite: migrate: %w", err)
	}
	return nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// RawDB returns the underlying *sql.DB for advanced queries.
func (d *DB) RawDB() *sql.DB {
	return d.db
}
