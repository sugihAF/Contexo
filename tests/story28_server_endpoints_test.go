package tests

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/schema"
)

func TestStory28_ListFeaturesEndpoint(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	// Create a commit with a feature
	commit := schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "feat-test-1",
		Title:     "Auth feature",
		Feature:   "auth",
		CreatedAt: time.Now().UTC(),
	}
	req := authRequest("POST", ts.URL+"/v1/repos/repo1/commits", commit)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	// List features
	req2 := authRequest("GET", ts.URL+"/v1/repos/repo1/features", nil)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
}

func TestStory28_PutAndGetFeatureOverview(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	overview := schema.FeatureOverview{
		Schema:    "ctx.feature_overview.v1",
		Feature:   "auth",
		RepoID:    "repo1",
		Summary:   "Authentication feature",
		Status:    "in_progress",
		UpdatedAt: time.Now().UTC(),
	}

	// Put
	req := authRequest("PUT", ts.URL+"/v1/repos/repo1/features/auth/overview", overview)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Get
	req2 := authRequest("GET", ts.URL+"/v1/repos/repo1/features/auth/overview", nil)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var got schema.FeatureOverview
	json.NewDecoder(resp2.Body).Decode(&got)
	assert.Equal(t, "auth", got.Feature)
	assert.Equal(t, "Authentication feature", got.Summary)
}

func TestStory28_CreateGitLink(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	// Create a commit first
	commit := schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "link-commit-1",
		Title:     "Link test",
		CreatedAt: time.Now().UTC(),
	}
	req := authRequest("POST", ts.URL+"/v1/repos/repo1/commits", commit)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	// Create git link
	linkReq := authRequest("POST", ts.URL+"/v1/repos/repo1/git-links", map[string]string{
		"git_sha":   "abc123",
		"commit_id": "link-commit-1",
	})
	resp2, err := http.DefaultClient.Do(linkReq)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusCreated, resp2.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&result)
	assert.Equal(t, "abc123", result["git_sha"])
	assert.Equal(t, "link-commit-1", result["commit_id"])
}

func TestStory28_GetRelatedByGitSHA(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	// Create commit and link
	commit := schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "related-commit-1",
		Title:     "Related test",
		CreatedAt: time.Now().UTC(),
	}
	req := authRequest("POST", ts.URL+"/v1/repos/repo1/commits", commit)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	linkReq := authRequest("POST", ts.URL+"/v1/repos/repo1/git-links", map[string]string{
		"git_sha":   "def456",
		"commit_id": "related-commit-1",
	})
	resp2, _ := http.DefaultClient.Do(linkReq)
	resp2.Body.Close()

	// Get related
	req3 := authRequest("GET", ts.URL+"/v1/repos/repo1/git/def456/related", nil)
	resp3, err := http.DefaultClient.Do(req3)
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusOK, resp3.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp3.Body).Decode(&result)
	assert.Equal(t, "def456", result["git_sha"])
	commitIDs := result["commit_ids"].([]interface{})
	assert.Contains(t, commitIDs, "related-commit-1")
}

func TestStory28_SymbolBlame(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	// Create commit with symbols
	commit := schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "blame-commit-1",
		Title:     "Blame test",
		CreatedAt: time.Now().UTC(),
		Changes: &schema.ChangeSet{
			Symbols: []string{"auth::Handler"},
		},
	}
	req := authRequest("POST", ts.URL+"/v1/repos/repo1/commits", commit)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	// Get blame
	req2 := authRequest("GET", ts.URL+"/v1/repos/repo1/symbols/auth::Handler/blame", nil)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&result)
	assert.Equal(t, "auth::Handler", result["symbol_key"])
}

func TestStory28_ContextLevelFeature(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	req := authRequest("GET", ts.URL+"/v1/repos/repo1/context?level=feature", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, "feature", result["level"])
}

func TestStory28_ContextLevelMetadata(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	req := authRequest("GET", ts.URL+"/v1/repos/repo1/context?level=metadata", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, "metadata", result["level"])
	assert.Equal(t, "repo1", result["repo_id"])
}
