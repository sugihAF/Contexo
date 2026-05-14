package service

import (
	"context"
	"sync"

	"github.com/sugihAF/contexo/internal/schema"
)

// MemStore is an in-memory Store implementation for testing.
type MemStore struct {
	mu       sync.RWMutex
	orgs     []*Org
	repos    []*Repo
	commits  map[string][]*schema.ContextCommit    // repoID -> commits
	gitLinks map[string]map[string][]string         // repoID -> gitSHA -> []commitID
	features map[string]map[string]*schema.FeatureOverview // repoID -> feature -> overview
}

// NewMemStore creates a MemStore.
func NewMemStore() *MemStore {
	return &MemStore{
		commits:  make(map[string][]*schema.ContextCommit),
		gitLinks: make(map[string]map[string][]string),
		features: make(map[string]map[string]*schema.FeatureOverview),
	}
}

func (m *MemStore) CreateOrg(_ context.Context, org *Org) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orgs = append(m.orgs, org)
	return nil
}

func (m *MemStore) ListOrgs(_ context.Context) ([]*Org, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.orgs, nil
}

func (m *MemStore) CreateRepo(_ context.Context, repo *Repo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.repos = append(m.repos, repo)
	return nil
}

func (m *MemStore) ListRepos(_ context.Context) ([]*Repo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.repos, nil
}

func (m *MemStore) CreateCommit(_ context.Context, repoID string, commit *schema.ContextCommit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commits[repoID] = append(m.commits[repoID], commit)
	return nil
}

func (m *MemStore) GetCommit(_ context.Context, repoID, commitID string) (*schema.ContextCommit, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.commits[repoID] {
		if c.CommitID == commitID {
			return c, nil
		}
	}
	return nil, nil
}

func (m *MemStore) ListCommits(_ context.Context, repoID string, limit int) ([]*schema.ContextCommit, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	all := m.commits[repoID]
	if limit > 0 && len(all) > limit {
		return all[:limit], nil
	}
	return all, nil
}

func (m *MemStore) ListCommitsByFeature(_ context.Context, repoID, feature string) ([]*schema.ContextCommit, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*schema.ContextCommit
	for _, c := range m.commits[repoID] {
		if c.Feature == feature {
			result = append(result, c)
		}
	}
	return result, nil
}

func (m *MemStore) LinkGit(_ context.Context, repoID, gitSHA, commitID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gitLinks[repoID] == nil {
		m.gitLinks[repoID] = make(map[string][]string)
	}
	m.gitLinks[repoID][gitSHA] = append(m.gitLinks[repoID][gitSHA], commitID)
	return nil
}

func (m *MemStore) GetByGitSHA(_ context.Context, repoID, gitSHA string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gitLinks[repoID][gitSHA], nil
}

func (m *MemStore) GetBySymbol(_ context.Context, repoID, symbolKey string) ([]*schema.ContextCommit, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*schema.ContextCommit
	for _, c := range m.commits[repoID] {
		if c.Changes != nil {
			for _, sym := range c.Changes.Symbols {
				if sym == symbolKey {
					result = append(result, c)
					break
				}
			}
		}
	}
	return result, nil
}

func (m *MemStore) GetFeatureOverview(_ context.Context, repoID, feature string) (*schema.FeatureOverview, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if repo, ok := m.features[repoID]; ok {
		return repo[feature], nil
	}
	return nil, nil
}

func (m *MemStore) PutFeatureOverview(_ context.Context, overview *schema.FeatureOverview) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.features[overview.RepoID] == nil {
		m.features[overview.RepoID] = make(map[string]*schema.FeatureOverview)
	}
	m.features[overview.RepoID][overview.Feature] = overview
	return nil
}

func (m *MemStore) ListFeatures(_ context.Context, repoID string) ([]*schema.FeatureOverview, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*schema.FeatureOverview
	for _, f := range m.features[repoID] {
		result = append(result, f)
	}
	return result, nil
}

var _ Store = (*MemStore)(nil) // compile-time check
