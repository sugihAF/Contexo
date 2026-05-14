package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store"
)

// UpsertSession inserts or updates a session record.
func (d *DB) UpsertSession(ctx context.Context, meta *schema.SessionMeta) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var endedAt sql.NullString
	if meta.EndedAt != nil {
		endedAt = sql.NullString{String: meta.EndedAt.Format(time.RFC3339Nano), Valid: true}
	}

	_, err := d.db.ExecContext(ctx,
		`INSERT INTO sessions (id, source, repo_id, started_at, ended_at, event_count, feature, jsonl_path)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '')
		 ON CONFLICT(id) DO UPDATE SET
			event_count = excluded.event_count,
			ended_at = excluded.ended_at`,
		meta.ID,
		meta.Source,
		repoID(meta.Repo),
		meta.StartedAt.Format(time.RFC3339Nano),
		endedAt,
		meta.EventCount,
		meta.Feature,
	)
	if err != nil {
		return fmt.Errorf("sqlite: upsert session: %w", err)
	}
	return nil
}

// GetSession retrieves a session by ID.
func (d *DB) GetSession(ctx context.Context, sessionID string) (*schema.SessionMeta, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	row := d.db.QueryRowContext(ctx,
		`SELECT id, source, repo_id, started_at, ended_at, event_count, feature FROM sessions WHERE id = ?`,
		sessionID,
	)

	return scanSession(row)
}

// ListSessions lists sessions matching the filter.
func (d *DB) ListSessions(ctx context.Context, filter store.SessionFilter) ([]*schema.SessionMeta, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, source, repo_id, started_at, ended_at, event_count, feature FROM sessions WHERE 1=1`
	var args []interface{}

	if filter.Source != "" {
		query += ` AND source = ?`
		args = append(args, filter.Source)
	}
	if filter.Feature != "" {
		query += ` AND feature = ?`
		args = append(args, filter.Feature)
	}
	if filter.RepoID != "" {
		query += ` AND repo_id = ?`
		args = append(args, filter.RepoID)
	}

	query += ` ORDER BY started_at DESC`

	if filter.Limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, filter.Limit)
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(` OFFSET %d`, filter.Offset)
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*schema.SessionMeta
	for rows.Next() {
		meta, err := scanSessionRows(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, meta)
	}
	return sessions, rows.Err()
}

// IncrementSessionEventCount creates or updates a session, incrementing event_count by 1.
func (d *DB) IncrementSessionEventCount(ctx context.Context, sessionID, source string, repo *schema.RepoRef, ts time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.ExecContext(ctx,
		`INSERT INTO sessions (id, source, repo_id, started_at, ended_at, event_count, feature, jsonl_path)
		 VALUES (?, ?, ?, ?, NULL, 1, '', '')
		 ON CONFLICT(id) DO UPDATE SET
			event_count = sessions.event_count + 1`,
		sessionID,
		source,
		repoID(repo),
		ts.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("sqlite: increment session event count: %w", err)
	}
	return nil
}

// InsertEvent inserts a session event record.
func (d *DB) InsertEvent(ctx context.Context, evt *schema.SessionEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO events (id, session_id, type, turn, ts, content_text, blob_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		evt.EventID,
		evt.Session.ID,
		evt.Type,
		evt.Turn,
		evt.Ts.Format(time.RFC3339Nano),
		evt.Content.Text,
		"",
	)
	if err != nil {
		return fmt.Errorf("sqlite: insert event: %w", err)
	}
	return nil
}

func repoID(repo *schema.RepoRef) string {
	if repo == nil {
		return ""
	}
	return repo.ID
}

func scanSession(row *sql.Row) (*schema.SessionMeta, error) {
	var meta schema.SessionMeta
	var repoIDStr string
	var startedAtStr string
	var endedAtStr sql.NullString

	err := row.Scan(&meta.ID, &meta.Source, &repoIDStr, &startedAtStr, &endedAtStr, &meta.EventCount, &meta.Feature)
	if err != nil {
		return nil, fmt.Errorf("sqlite: scan session: %w", err)
	}

	meta.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAtStr)
	if endedAtStr.Valid {
		t, _ := time.Parse(time.RFC3339Nano, endedAtStr.String)
		meta.EndedAt = &t
	}
	if repoIDStr != "" {
		meta.Repo = &schema.RepoRef{ID: repoIDStr}
	}

	return &meta, nil
}

func scanSessionRows(rows *sql.Rows) (*schema.SessionMeta, error) {
	var meta schema.SessionMeta
	var repoIDStr string
	var startedAtStr string
	var endedAtStr sql.NullString

	err := rows.Scan(&meta.ID, &meta.Source, &repoIDStr, &startedAtStr, &endedAtStr, &meta.EventCount, &meta.Feature)
	if err != nil {
		return nil, fmt.Errorf("sqlite: scan session row: %w", err)
	}

	meta.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAtStr)
	if endedAtStr.Valid {
		t, _ := time.Parse(time.RFC3339Nano, endedAtStr.String)
		meta.EndedAt = &t
	}
	if repoIDStr != "" {
		meta.Repo = &schema.RepoRef{ID: repoIDStr}
	}

	return &meta, nil
}
