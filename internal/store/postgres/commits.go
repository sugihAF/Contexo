package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/server/service"
)

// CreateOrg stores a new organization.
func (db *DB) CreateOrg(ctx context.Context, org *service.Org) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO orgs (id, name, created_at) VALUES ($1, $2, $3)`,
		org.ID, org.Name, org.CreatedAt,
	)
	return err
}

// ListOrgs lists all organizations.
func (db *DB) ListOrgs(ctx context.Context) ([]*service.Org, error) {
	rows, err := db.pool.Query(ctx, `SELECT id, name, created_at FROM orgs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orgs []*service.Org
	for rows.Next() {
		var o service.Org
		if err := rows.Scan(&o.ID, &o.Name, &o.CreatedAt); err != nil {
			return nil, err
		}
		orgs = append(orgs, &o)
	}
	return orgs, rows.Err()
}

// CreateRepo stores a new repository.
func (db *DB) CreateRepo(ctx context.Context, repo *service.Repo) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO repos (id, org_id, name, created_at) VALUES ($1, $2, $3, $4)`,
		repo.ID, repo.OrgID, repo.Name, repo.CreatedAt,
	)
	return err
}

// ListRepos lists all repositories.
func (db *DB) ListRepos(ctx context.Context) ([]*service.Repo, error) {
	rows, err := db.pool.Query(ctx, `SELECT id, org_id, name, created_at FROM repos ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []*service.Repo
	for rows.Next() {
		var r service.Repo
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Name, &r.CreatedAt); err != nil {
			return nil, err
		}
		repos = append(repos, &r)
	}
	return repos, rows.Err()
}

// CreateCommit stores a context commit.
func (db *DB) CreateCommit(ctx context.Context, repoID string, commit *schema.ContextCommit) error {
	dataJSON, err := json.Marshal(commit)
	if err != nil {
		return fmt.Errorf("postgres: marshal commit: %w", err)
	}
	_, err = db.pool.Exec(ctx,
		`INSERT INTO commits (id, repo_id, title, summary, feature, author, created_at, data_json)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		commit.CommitID, repoID, commit.Title, commit.SummaryText(), commit.Feature,
		commit.AuthorName(), commit.CreatedAt, string(dataJSON),
	)
	return err
}

// GetCommit retrieves a commit.
func (db *DB) GetCommit(ctx context.Context, repoID, commitID string) (*schema.ContextCommit, error) {
	var dataJSON string
	err := db.pool.QueryRow(ctx,
		`SELECT data_json FROM commits WHERE repo_id = $1 AND id = $2`,
		repoID, commitID,
	).Scan(&dataJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: get commit: %w", err)
	}

	var commit schema.ContextCommit
	if err := json.Unmarshal([]byte(dataJSON), &commit); err != nil {
		return nil, err
	}
	return &commit, nil
}

// ListCommits lists commits for a repo.
func (db *DB) ListCommits(ctx context.Context, repoID string, limit int) ([]*schema.ContextCommit, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT data_json FROM commits WHERE repo_id = $1 ORDER BY created_at DESC LIMIT $2`,
		repoID, limit,
	)
	if err != nil {
		return nil, err
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

// ListCommitsByFeature lists commits filtered by feature.
func (db *DB) ListCommitsByFeature(ctx context.Context, repoID, feature string) ([]*schema.ContextCommit, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT data_json FROM commits WHERE repo_id = $1 AND feature = $2 ORDER BY created_at DESC`,
		repoID, feature,
	)
	if err != nil {
		return nil, err
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

// LinkGit creates a link between a git SHA and a context commit.
func (db *DB) LinkGit(ctx context.Context, repoID, gitSHA, commitID string) error {
	_, err := db.pool.Exec(ctx,
		`INSERT INTO git_links (repo_id, git_sha, commit_id) VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING`,
		repoID, gitSHA, commitID,
	)
	return err
}

// GetByGitSHA returns commit IDs linked to a git SHA.
func (db *DB) GetByGitSHA(ctx context.Context, repoID, gitSHA string) ([]string, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT commit_id FROM git_links WHERE repo_id = $1 AND git_sha = $2`,
		repoID, gitSHA,
	)
	if err != nil {
		return nil, err
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

// GetBySymbol returns commits that touch a given symbol.
func (db *DB) GetBySymbol(ctx context.Context, repoID, symbolKey string) ([]*schema.ContextCommit, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT c.data_json FROM commits c
		 INNER JOIN commit_symbols cs ON cs.repo_id = c.repo_id AND cs.commit_id = c.id
		 WHERE c.repo_id = $1 AND cs.symbol_key = $2
		 ORDER BY c.created_at DESC`,
		repoID, symbolKey,
	)
	if err != nil {
		return nil, err
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

// GetFeatureOverview retrieves a feature overview.
func (db *DB) GetFeatureOverview(ctx context.Context, repoID, feature string) (*schema.FeatureOverview, error) {
	var dataJSON string
	err := db.pool.QueryRow(ctx,
		`SELECT data_json FROM features WHERE repo_id = $1 AND feature = $2`,
		repoID, feature,
	).Scan(&dataJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var overview schema.FeatureOverview
	if err := json.Unmarshal([]byte(dataJSON), &overview); err != nil {
		return nil, err
	}
	return &overview, nil
}

// PutFeatureOverview stores a feature overview.
func (db *DB) PutFeatureOverview(ctx context.Context, overview *schema.FeatureOverview) error {
	dataJSON, err := json.Marshal(overview)
	if err != nil {
		return err
	}
	_, err = db.pool.Exec(ctx,
		`INSERT INTO features (repo_id, feature, summary, status, data_json, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (repo_id, feature) DO UPDATE SET
		 summary = EXCLUDED.summary, status = EXCLUDED.status,
		 data_json = EXCLUDED.data_json, updated_at = EXCLUDED.updated_at`,
		overview.RepoID, overview.Feature, overview.Summary, overview.Status,
		string(dataJSON), overview.UpdatedAt,
	)
	return err
}

// ListFeatures lists all features for a repo.
func (db *DB) ListFeatures(ctx context.Context, repoID string) ([]*schema.FeatureOverview, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT data_json FROM features WHERE repo_id = $1 ORDER BY feature`,
		repoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var features []*schema.FeatureOverview
	for rows.Next() {
		var dataJSON string
		if err := rows.Scan(&dataJSON); err != nil {
			return nil, err
		}
		var f schema.FeatureOverview
		if err := json.Unmarshal([]byte(dataJSON), &f); err != nil {
			return nil, err
		}
		features = append(features, &f)
	}
	return features, rows.Err()
}
