package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/server"
	"github.com/sugihAF/contexo/internal/server/service"
)

func setupTestServer() *httptest.Server {
	store := service.NewMemStore()
	svc := service.New(store)
	validator := func(key string) (string, bool) {
		if key == "test-key" {
			return "test-user", true
		}
		return "", false
	}
	router := server.NewRouter(svc, validator)
	return httptest.NewServer(router)
}

func authRequest(method, url string, body interface{}) *http.Request {
	var reqBody *bytes.Buffer
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewBuffer(data)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}
	req, _ := http.NewRequest(method, url, reqBody)
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestStory17_HealthEndpoint(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestStory17_CreateOrg(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	req := authRequest("POST", ts.URL+"/v1/orgs", map[string]string{"name": "TestOrg"})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestStory17_CreateRepo(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	// Create org first
	req1 := authRequest("POST", ts.URL+"/v1/orgs", map[string]string{"name": "TestOrg"})
	resp1, _ := http.DefaultClient.Do(req1)
	var org map[string]interface{}
	json.NewDecoder(resp1.Body).Decode(&org)
	resp1.Body.Close()

	// Create repo
	req2 := authRequest("POST", ts.URL+"/v1/repos", map[string]interface{}{
		"org_id": org["id"],
		"name":   "TestRepo",
	})
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusCreated, resp2.StatusCode)
}

func TestStory17_CreateAndGetCommit(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	commit := schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "test-commit-1",
		Title:     "Test commit",
		Feature:   "auth",
		CreatedAt: time.Now().UTC(),
	}

	// Create
	req := authRequest("POST", ts.URL+"/v1/repos/repo1/commits", commit)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Get
	req2 := authRequest("GET", ts.URL+"/v1/repos/repo1/commits/test-commit-1", nil)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var got schema.ContextCommit
	json.NewDecoder(resp2.Body).Decode(&got)
	assert.Equal(t, "Test commit", got.Title)
}

func TestStory17_ListCommitsByFeature(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	// Create commits
	for _, feat := range []string{"auth", "auth", "db"} {
		commit := schema.ContextCommit{
			Schema:    "ctx.commit.v1",
			CommitID:  "c-" + feat + "-" + time.Now().String(),
			Title:     feat + " commit",
			Feature:   feat,
			CreatedAt: time.Now().UTC(),
		}
		req := authRequest("POST", ts.URL+"/v1/repos/repo1/commits", commit)
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
	}

	// List by feature
	req := authRequest("GET", ts.URL+"/v1/repos/repo1/features/auth/commits", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var commits []*schema.ContextCommit
	json.NewDecoder(resp.Body).Decode(&commits)
	assert.Len(t, commits, 2)
}

func TestStory17_AuthMiddlewareRejects(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	// No auth
	resp, err := http.Get(ts.URL + "/v1/orgs")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
