package sqlite

import (
	"context"
	"fmt"
	"time"
)

// MarkSynced marks an item as synced.
func (d *DB) MarkSynced(ctx context.Context, itemType, itemID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.ExecContext(ctx,
		`INSERT INTO sync_status (item_type, item_id, synced_at) VALUES (?, ?, ?)
		 ON CONFLICT(item_type, item_id) DO UPDATE SET synced_at = excluded.synced_at`,
		itemType, itemID, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("sqlite: mark synced: %w", err)
	}
	return nil
}

// IsSynced checks if an item has been synced.
func (d *DB) IsSynced(ctx context.Context, itemType, itemID string) (bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT count(*) FROM sync_status WHERE item_type = ? AND item_id = ?`,
		itemType, itemID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetUnsyncedCommits returns commit IDs that haven't been synced yet.
func (d *DB) GetUnsyncedCommits(ctx context.Context) ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.QueryContext(ctx,
		`SELECT c.id FROM commits c
		 LEFT JOIN sync_status s ON s.item_type = 'commit' AND s.item_id = c.id
		 WHERE s.item_id IS NULL
		 ORDER BY c.created_at`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get unsynced: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
