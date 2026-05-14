package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sugihAF/contexo/internal/schema"
)

// Store defines the storage interface used by the service.
type Store interface {
	CreateOrg(ctx context.Context, org *Org) error
	ListOrgs(ctx context.Context) ([]*Org, error)
	CreateRepo(ctx context.Context, repo *Repo) error
	ListRepos(ctx context.Context) ([]*Repo, error)
	CreateCommit(ctx context.Context, repoID string, commit *schema.ContextCommit) error
	GetCommit(ctx context.Context, repoID, commitID string) (*schema.ContextCommit, error)
	ListCommits(ctx context.Context, repoID string, limit int) ([]*schema.ContextCommit, error)
	ListCommitsByFeature(ctx context.Context, repoID, feature string) ([]*schema.ContextCommit, error)
	LinkGit(ctx context.Context, repoID, gitSHA, commitID string) error
	GetByGitSHA(ctx context.Context, repoID, gitSHA string) ([]string, error)
	GetBySymbol(ctx context.Context, repoID, symbolKey string) ([]*schema.ContextCommit, error)
	GetFeatureOverview(ctx context.Context, repoID, feature string) (*schema.FeatureOverview, error)
	PutFeatureOverview(ctx context.Context, overview *schema.FeatureOverview) error
	ListFeatures(ctx context.Context, repoID string) ([]*schema.FeatureOverview, error)
}

// Org represents an organization.
type Org struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Repo represents a repository.
type Repo struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Service encapsulates business logic.
type Service struct {
	store Store
}

// New creates a Service.
func New(store Store) *Service {
	return &Service{store: store}
}

// CreateOrg creates a new organization.
func (s *Service) CreateOrg(ctx context.Context, name string) (*Org, error) {
	org := &Org{
		ID:        uuid.Must(uuid.NewV7()).String(),
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateOrg(ctx, org); err != nil {
		return nil, fmt.Errorf("service: create org: %w", err)
	}
	return org, nil
}

// ListOrgs lists organizations.
func (s *Service) ListOrgs(ctx context.Context) ([]*Org, error) {
	return s.store.ListOrgs(ctx)
}

// CreateRepo creates a new repository.
func (s *Service) CreateRepo(ctx context.Context, orgID, name string) (*Repo, error) {
	repo := &Repo{
		ID:        uuid.Must(uuid.NewV7()).String(),
		OrgID:     orgID,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateRepo(ctx, repo); err != nil {
		return nil, fmt.Errorf("service: create repo: %w", err)
	}
	return repo, nil
}

// ListRepos lists repositories.
func (s *Service) ListRepos(ctx context.Context) ([]*Repo, error) {
	return s.store.ListRepos(ctx)
}

// CreateCommit stores a context commit.
func (s *Service) CreateCommit(ctx context.Context, repoID string, commit *schema.ContextCommit) error {
	return s.store.CreateCommit(ctx, repoID, commit)
}

// GetCommit retrieves a commit.
func (s *Service) GetCommit(ctx context.Context, repoID, commitID string) (*schema.ContextCommit, error) {
	return s.store.GetCommit(ctx, repoID, commitID)
}

// ListCommits lists commits for a repo.
func (s *Service) ListCommits(ctx context.Context, repoID string, limit int) ([]*schema.ContextCommit, error) {
	return s.store.ListCommits(ctx, repoID, limit)
}

// ListCommitsByFeature lists commits filtered by feature.
func (s *Service) ListCommitsByFeature(ctx context.Context, repoID, feature string) ([]*schema.ContextCommit, error) {
	return s.store.ListCommitsByFeature(ctx, repoID, feature)
}

// LinkGit links a git SHA to a context commit.
func (s *Service) LinkGit(ctx context.Context, repoID, gitSHA, commitID string) error {
	return s.store.LinkGit(ctx, repoID, gitSHA, commitID)
}

// GetByGitSHA returns commit IDs linked to a git SHA.
func (s *Service) GetByGitSHA(ctx context.Context, repoID, gitSHA string) ([]string, error) {
	return s.store.GetByGitSHA(ctx, repoID, gitSHA)
}

// GetBySymbol returns commits that touch a given symbol.
func (s *Service) GetBySymbol(ctx context.Context, repoID, symbolKey string) ([]*schema.ContextCommit, error) {
	return s.store.GetBySymbol(ctx, repoID, symbolKey)
}

// GetFeatureOverview retrieves a feature overview.
func (s *Service) GetFeatureOverview(ctx context.Context, repoID, feature string) (*schema.FeatureOverview, error) {
	return s.store.GetFeatureOverview(ctx, repoID, feature)
}

// PutFeatureOverview stores a feature overview.
func (s *Service) PutFeatureOverview(ctx context.Context, overview *schema.FeatureOverview) error {
	return s.store.PutFeatureOverview(ctx, overview)
}

// ListFeatures lists all features for a repo.
func (s *Service) ListFeatures(ctx context.Context, repoID string) ([]*schema.FeatureOverview, error) {
	return s.store.ListFeatures(ctx, repoID)
}
