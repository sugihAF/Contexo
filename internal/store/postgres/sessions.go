package postgres

import (
	"context"
	"fmt"
	"time"
)

// SessionRecord represents a server-side session metadata record.
type SessionRecord struct {
	ID         string
	RepoID     string
	Source     string
	StartedAt  time.Time
	EndedAt    *time.Time
	EventCount int
	Feature    string
	S3Key      string
}

// CreateSession stores session metadata.
func (db *DB) CreateSession(ctx context.Context, sess *SessionRecord) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO sessions (id, repo_id, source, started_at, ended_at, event_count, feature, s3_key)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (repo_id, id) DO UPDATE SET
			event_count = EXCLUDED.event_count,
			ended_at = EXCLUDED.ended_at,
			s3_key = EXCLUDED.s3_key`,
		sess.ID, sess.RepoID, sess.Source, sess.StartedAt, sess.EndedAt,
		sess.EventCount, sess.Feature, sess.S3Key,
	)
	if err != nil {
		return fmt.Errorf("postgres: create session: %w", err)
	}
	return nil
}

// GetSession retrieves session metadata.
func (db *DB) GetSession(ctx context.Context, repoID, sessionID string) (*SessionRecord, error) {
	var sess SessionRecord
	err := db.pool.QueryRow(ctx,
		`SELECT id, repo_id, source, started_at, ended_at, event_count, feature, s3_key
		 FROM sessions WHERE repo_id = $1 AND id = $2`,
		repoID, sessionID,
	).Scan(&sess.ID, &sess.RepoID, &sess.Source, &sess.StartedAt, &sess.EndedAt,
		&sess.EventCount, &sess.Feature, &sess.S3Key)
	if err != nil {
		return nil, fmt.Errorf("postgres: get session: %w", err)
	}
	return &sess, nil
}
