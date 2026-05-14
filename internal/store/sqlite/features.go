package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
)

// PutOverview stores or updates a feature overview.
func (d *DB) PutOverview(ctx context.Context, overview *schema.FeatureOverview) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	dataJSON, err := json.Marshal(overview)
	if err != nil {
		return fmt.Errorf("sqlite: marshal overview: %w", err)
	}

	_, err = d.db.ExecContext(ctx,
		`INSERT INTO features (repo_id, feature, summary, status, data_json, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(repo_id, feature) DO UPDATE SET
			summary = excluded.summary,
			status = excluded.status,
			data_json = excluded.data_json,
			updated_at = excluded.updated_at`,
		overview.RepoID,
		overview.Feature,
		overview.Summary,
		overview.Status,
		string(dataJSON),
		overview.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("sqlite: put overview: %w", err)
	}
	return nil
}

// GetOverview retrieves a feature overview.
func (d *DB) GetOverview(ctx context.Context, repoID, feature string) (*schema.FeatureOverview, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	row := d.db.QueryRowContext(ctx,
		`SELECT data_json FROM features WHERE repo_id = ? AND feature = ?`,
		repoID, feature,
	)

	var dataJSON string
	if err := row.Scan(&dataJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite: get overview: %w", err)
	}

	var overview schema.FeatureOverview
	if err := json.Unmarshal([]byte(dataJSON), &overview); err != nil {
		return nil, fmt.Errorf("sqlite: unmarshal overview: %w", err)
	}
	return &overview, nil
}

// AppendActivity adds an activity entry.
func (d *DB) AppendActivity(ctx context.Context, entry *schema.ActivityEntry) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.ExecContext(ctx,
		`INSERT INTO activity_log (id, repo_id, feature, type, summary, commit_id, session_id, actor, ts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID,
		entry.RepoID,
		entry.Feature,
		entry.Type,
		entry.Summary,
		entry.CommitID,
		entry.SessionID,
		entry.Actor,
		entry.Ts.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("sqlite: append activity: %w", err)
	}
	return nil
}

// ListActivity returns recent activity entries in reverse chronological order.
func (d *DB) ListActivity(ctx context.Context, repoID, feature string, limit int) ([]*schema.ActivityEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, repo_id, feature, type, summary, commit_id, session_id, actor, ts
		FROM activity_log WHERE repo_id = ? AND feature = ?
		ORDER BY ts DESC`

	if limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, limit)
	}

	rows, err := d.db.QueryContext(ctx, query, repoID, feature)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list activity: %w", err)
	}
	defer rows.Close()

	var entries []*schema.ActivityEntry
	for rows.Next() {
		var e schema.ActivityEntry
		var tsStr string
		err := rows.Scan(&e.ID, &e.RepoID, &e.Feature, &e.Type, &e.Summary,
			&e.CommitID, &e.SessionID, &e.Actor, &tsStr)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan activity: %w", err)
		}
		e.Ts, _ = time.Parse(time.RFC3339Nano, tsStr)
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}
