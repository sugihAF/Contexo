package sqlite

import (
	"context"
	"fmt"
)

// IndexSymbol adds a symbol -> commit mapping.
func (d *DB) IndexSymbol(ctx context.Context, symbolKey, commitID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO symbol_index (symbol_key, commit_id) VALUES (?, ?)`,
		symbolKey, commitID,
	)
	if err != nil {
		return fmt.Errorf("sqlite: index symbol: %w", err)
	}
	return nil
}

// GetCommitsBySymbol returns commit IDs that touch a given symbol.
func (d *DB) GetCommitsBySymbol(ctx context.Context, symbolKey string) ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.QueryContext(ctx,
		`SELECT commit_id FROM symbol_index WHERE symbol_key = ? ORDER BY commit_id`, symbolKey,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get commits by symbol: %w", err)
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
