// Package userstore is a SQLite-backed store for users, personal access
// tokens, per-repo memberships, and invite keys. The git-backed repo content
// itself lives in gitstore — userstore only owns access metadata.
package userstore

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    email         TEXT UNIQUE NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    google_sub    TEXT UNIQUE,
    created_at    INTEGER NOT NULL,
    last_login_at INTEGER
);

CREATE TABLE IF NOT EXISTS personal_access_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT UNIQUE NOT NULL,
    label        TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_pats_user ON personal_access_tokens(user_id);

CREATE TABLE IF NOT EXISTS repo_members (
    repo_id  TEXT NOT NULL,
    user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role     TEXT NOT NULL DEFAULT 'member',
    added_at INTEGER NOT NULL,
    PRIMARY KEY (repo_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_repo_members_user ON repo_members(user_id);

CREATE TABLE IF NOT EXISTS repo_invite_keys (
    id          TEXT PRIMARY KEY,
    repo_id     TEXT NOT NULL,
    key_hash    TEXT UNIQUE NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_invite_keys_repo ON repo_invite_keys(repo_id);
`

// Store wraps the SQLite database holding access metadata.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at path and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("userstore: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("userstore: ping: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("userstore: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB exposes the underlying *sql.DB (mainly for tests).
func (s *Store) DB() *sql.DB { return s.db }
