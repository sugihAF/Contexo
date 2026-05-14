package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store"
)

// CreateCommit stores a context commit.
func (d *DB) CreateCommit(ctx context.Context, commit *schema.ContextCommit) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	dataJSON, err := json.Marshal(commit)
	if err != nil {
		return fmt.Errorf("sqlite: marshal commit: %w", err)
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO commits (id, title, summary, feature, author, created_at, parent_id, data_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		commit.CommitID,
		commit.Title,
		commit.SummaryText(),
		commit.Feature,
		commit.AuthorName(),
		commit.CreatedAt.Format(time.RFC3339Nano),
		commit.ParentID,
		string(dataJSON),
	)
	if err != nil {
		return fmt.Errorf("sqlite: insert commit: %w", err)
	}

	if commit.Changes != nil {
		for _, f := range commit.Changes.Files {
			_, err = tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO commit_files (commit_id, path, action) VALUES (?, ?, ?)`,
				commit.CommitID, f.Path, f.Action,
			)
			if err != nil {
				return fmt.Errorf("sqlite: insert commit_file: %w", err)
			}
		}
		for _, sym := range commit.Changes.Symbols {
			_, err = tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO commit_symbols (commit_id, symbol_key) VALUES (?, ?)`,
				commit.CommitID, sym,
			)
			if err != nil {
				return fmt.Errorf("sqlite: insert commit_symbol: %w", err)
			}
			_, err = tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO symbol_index (symbol_key, commit_id) VALUES (?, ?)`,
				sym, commit.CommitID,
			)
			if err != nil {
				return fmt.Errorf("sqlite: insert symbol_index: %w", err)
			}
		}
	}

	return tx.Commit()
}

// GetCommit retrieves a commit by ID.
func (d *DB) GetCommit(ctx context.Context, commitID string) (*schema.ContextCommit, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	row := d.db.QueryRowContext(ctx, `SELECT data_json FROM commits WHERE id = ?`, commitID)

	var dataJSON string
	if err := row.Scan(&dataJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite: get commit: %w", err)
	}

	var commit schema.ContextCommit
	if err := json.Unmarshal([]byte(dataJSON), &commit); err != nil {
		return nil, fmt.Errorf("sqlite: unmarshal commit: %w", err)
	}
	return &commit, nil
}

// ListCommits lists commits matching the filter.
func (d *DB) ListCommits(ctx context.Context, filter store.CommitFilter) ([]*schema.ContextCommit, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT data_json FROM commits WHERE 1=1`
	var args []interface{}

	if filter.Feature != "" {
		query += ` AND feature = ?`
		args = append(args, filter.Feature)
	}
	if filter.Author != "" {
		query += ` AND author = ?`
		args = append(args, filter.Author)
	}

	query += ` ORDER BY created_at DESC`

	if filter.Limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, filter.Limit)
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list commits: %w", err)
	}
	defer rows.Close()

	var commits []*schema.ContextCommit
	for rows.Next() {
		var dataJSON string
		if err := rows.Scan(&dataJSON); err != nil {
			return nil, fmt.Errorf("sqlite: scan commit: %w", err)
		}
		var c schema.ContextCommit
		if err := json.Unmarshal([]byte(dataJSON), &c); err != nil {
			return nil, fmt.Errorf("sqlite: unmarshal commit: %w", err)
		}
		commits = append(commits, &c)
	}
	return commits, rows.Err()
}

// LinkGit creates a bidirectional link between a git SHA and a context commit.
func (d *DB) LinkGit(ctx context.Context, gitSHA, commitID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO git_links (git_sha, commit_id) VALUES (?, ?)`,
		gitSHA, commitID,
	)
	if err != nil {
		return fmt.Errorf("sqlite: link git: %w", err)
	}
	return nil
}

// GetByGitSHA returns commit IDs linked to a git SHA.
func (d *DB) GetByGitSHA(ctx context.Context, gitSHA string) ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.QueryContext(ctx,
		`SELECT commit_id FROM git_links WHERE git_sha = ?`, gitSHA,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get by git sha: %w", err)
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

// GetGitSHAsByCommit returns git SHAs linked to a commit ID.
func (d *DB) GetGitSHAsByCommit(ctx context.Context, commitID string) ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.QueryContext(ctx,
		`SELECT git_sha FROM git_links WHERE commit_id = ?`, commitID,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get git shas: %w", err)
	}
	defer rows.Close()

	var shas []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		shas = append(shas, sha)
	}
	return shas, rows.Err()
}

// GetBySymbol returns commits that touch a given symbol.
func (d *DB) GetBySymbol(ctx context.Context, symbolKey string) ([]*schema.ContextCommit, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.QueryContext(ctx,
		`SELECT c.data_json FROM commits c
		 INNER JOIN symbol_index si ON si.commit_id = c.id
		 WHERE si.symbol_key = ?
		 ORDER BY c.created_at DESC`, symbolKey,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get by symbol: %w", err)
	}
	defer rows.Close()

	var commits []*schema.ContextCommit
	for rows.Next() {
		var dataJSON string
		if err := rows.Scan(&dataJSON); err != nil {
			return nil, err
		}
		var c schema.ContextCommit
		if err := json.Unmarshal([]byte(dataJSON), &c); err != nil {
			return nil, err
		}
		commits = append(commits, &c)
	}
	return commits, rows.Err()
}
