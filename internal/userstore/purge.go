package userstore

import (
	"errors"
	"fmt"
)

// KnownRepoIDs returns every distinct repo id that has a membership row, an
// invite key or an activity row, sorted ascending (byte order). Ids with no
// repo directory are included: rows outlive an operator's deletion of a
// repo, and a creator needs every id whose rows could name the new repo's
// directory (see PurgeRepo).
//
// It reads all three tables (activity holds up to activityRetentionPerRepo
// rows per repo), so it costs a scan of their repo_id indexes. Callers run it
// only when a repo is created, which is rare.
func (s *Store) KnownRepoIDs() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT repo_id FROM repo_members
		 UNION SELECT repo_id FROM repo_invite_keys
		 UNION SELECT repo_id FROM repo_activity
		 ORDER BY repo_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("userstore: known repo ids: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("userstore: known repo ids: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("userstore: known repo ids: %w", err)
	}
	return out, nil
}

// PurgeRepo deletes every membership row, invite key and activity row of
// repoID and of each alias id, in one transaction: all of them or, on error,
// none. Every id must be non-empty.
//
// It is for a repo id whose directory was just created, definitively (the
// creator ran git init; see gitstore.Store.Create), together with the other
// ids that name that same directory. Rows left by an operator-deleted repo
// of that directory must not grant access to the new repo, show it the old
// repo's feed (members' emails and paths) or let an old invite key join it.
// Never call it for a repo that may exist: a stat error can make a live repo
// look absent, and this would wipe its members.
//
// It removes the rows present when it commits. A row that a concurrent
// request inserts afterwards (an invite key minted, or a join resolved,
// against a stale row before the purge) is not covered here; closing that
// needs the invite routes to check and insert atomically with respect to
// repo creation.
func (s *Store) PurgeRepo(repoID string, aliases ...string) error {
	ids := append([]string{repoID}, aliases...)
	for _, id := range ids {
		if id == "" {
			return errors.New("userstore: purge repo: empty repo id")
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("userstore: purge repo: %w", err)
	}
	// Undoes every delete on an early return; a no-op after Commit.
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		for _, q := range []string{
			`DELETE FROM repo_members WHERE repo_id = ?`,
			`DELETE FROM repo_invite_keys WHERE repo_id = ?`,
			`DELETE FROM repo_activity WHERE repo_id = ?`,
		} {
			if _, err := tx.Exec(q, id); err != nil {
				return fmt.Errorf("userstore: purge repo: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("userstore: purge repo: %w", err)
	}
	return nil
}
