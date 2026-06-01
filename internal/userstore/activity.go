package userstore

import (
	"fmt"
	"time"
)

// activityRetentionPerRepo bounds the per-repo activity feed; older events are
// pruned on insert. It's a var (not const) so tests can shrink it.
var activityRetentionPerRepo = 1000

// Activity is one recorded member action (push/pull) on a repo. Email is
// joined in from the users table by ListActivity. Detail is an opaque JSON
// blob (pushed paths for "push", client/agent for "pull"); "" when absent.
type Activity struct {
	UserID    string
	Email     string
	Action    string
	Detail    string
	CreatedAt time.Time
}

// RecordActivity appends one event to a repo's feed, then prunes the feed back
// to activityRetentionPerRepo newest events. Best-effort: callers log-and-ignore.
func (s *Store) RecordActivity(repoID, userID, action, detail string) error {
	if _, err := s.db.Exec(
		`INSERT INTO repo_activity (repo_id, user_id, action, detail, created_at) VALUES (?, ?, ?, ?, ?)`,
		repoID, userID, action, detail, time.Now().UTC().Unix(),
	); err != nil {
		return fmt.Errorf("userstore: record activity: %w", err)
	}
	return s.pruneActivity(repoID)
}

// pruneActivity deletes all but the newest activityRetentionPerRepo events for
// repoID. Ordering by id (a monotonic autoincrement) is stable even when
// several events share a created_at second.
func (s *Store) pruneActivity(repoID string) error {
	if _, err := s.db.Exec(
		`DELETE FROM repo_activity
		   WHERE repo_id = ?
		     AND id NOT IN (
		         SELECT id FROM repo_activity
		          WHERE repo_id = ?
		          ORDER BY id DESC
		          LIMIT ?
		     )`,
		repoID, repoID, activityRetentionPerRepo,
	); err != nil {
		return fmt.Errorf("userstore: prune activity: %w", err)
	}
	return nil
}

// ListActivity returns a page of events for repoID (reverse-chronological),
// each with the actor's email joined from the users table. limit <= 0 defaults
// to 50; offset < 0 is treated as 0.
func (s *Store) ListActivity(repoID string, limit, offset int) ([]Activity, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(
		`SELECT a.user_id, u.email, a.action, COALESCE(a.detail, ''), a.created_at
		   FROM repo_activity a
		   JOIN users u ON u.id = a.user_id
		  WHERE a.repo_id = ?
		  ORDER BY a.id DESC
		  LIMIT ? OFFSET ?`,
		repoID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("userstore: list activity: %w", err)
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var (
			a  Activity
			ts int64
		)
		if err := rows.Scan(&a.UserID, &a.Email, &a.Action, &a.Detail, &ts); err != nil {
			return nil, fmt.Errorf("userstore: scan activity: %w", err)
		}
		a.CreatedAt = time.Unix(ts, 0).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountActivity returns the total number of events stored for repoID (bounded
// by activityRetentionPerRepo). Used to drive pagination.
func (s *Store) CountActivity(repoID string) (int, error) {
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM repo_activity WHERE repo_id = ?`, repoID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("userstore: count activity: %w", err)
	}
	return n, nil
}
