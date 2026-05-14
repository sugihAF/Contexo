package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a PostgreSQL connection pool.
type DB struct {
	pool *pgxpool.Pool
}

// Open creates a new PostgreSQL connection pool.
func Open(ctx context.Context, connStr string) (*DB, error) {
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Migrate runs database migrations.
func (db *DB) Migrate(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, migrateSQL)
	if err != nil {
		return fmt.Errorf("postgres: migrate: %w", err)
	}
	return nil
}

// Close closes the connection pool.
func (db *DB) Close() {
	db.pool.Close()
}

// Pool returns the underlying pool.
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

const migrateSQL = `
CREATE TABLE IF NOT EXISTS orgs (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS repos (
	id TEXT PRIMARY KEY,
	org_id TEXT NOT NULL REFERENCES orgs(id),
	name TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS commits (
	id TEXT NOT NULL,
	repo_id TEXT NOT NULL,
	title TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	feature TEXT NOT NULL DEFAULT '',
	author TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL,
	data_json JSONB NOT NULL DEFAULT '{}',
	PRIMARY KEY (repo_id, id)
);

CREATE INDEX IF NOT EXISTS idx_commits_feature ON commits(repo_id, feature);

CREATE TABLE IF NOT EXISTS sessions (
	id TEXT NOT NULL,
	repo_id TEXT NOT NULL,
	source TEXT NOT NULL DEFAULT '',
	started_at TIMESTAMPTZ NOT NULL,
	ended_at TIMESTAMPTZ,
	event_count INTEGER NOT NULL DEFAULT 0,
	feature TEXT NOT NULL DEFAULT '',
	s3_key TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (repo_id, id)
);

CREATE TABLE IF NOT EXISTS api_keys (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	key_hash TEXT NOT NULL UNIQUE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	last_used_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS git_links (
	repo_id TEXT NOT NULL,
	git_sha TEXT NOT NULL,
	commit_id TEXT NOT NULL,
	PRIMARY KEY (repo_id, git_sha, commit_id)
);

CREATE TABLE IF NOT EXISTS commit_symbols (
	repo_id TEXT NOT NULL,
	commit_id TEXT NOT NULL,
	symbol_key TEXT NOT NULL,
	PRIMARY KEY (repo_id, commit_id, symbol_key)
);

CREATE TABLE IF NOT EXISTS features (
	repo_id TEXT NOT NULL,
	feature TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	data_json JSONB NOT NULL DEFAULT '{}',
	updated_at TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (repo_id, feature)
);
`
