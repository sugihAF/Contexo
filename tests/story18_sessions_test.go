package tests

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/store/s3"
)

func TestStory18_CreateSession(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	body := map[string]interface{}{
		"id":          "sess-001",
		"source":      "claude_code",
		"started_at":  "2026-01-15T10:30:00Z",
		"event_count": 10,
	}

	req := authRequest("POST", ts.URL+"/v1/repos/repo1/sessions", body)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestStory18_UploadChunk(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	req := authRequest("PUT", ts.URL+"/v1/repos/repo1/sessions/sess-001/chunks/chunk-001", map[string]string{
		"data": "test chunk data",
	})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Equal(t, "uploaded", result["status"])
}

func TestStory18_GetSessionMeta(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	req := authRequest("GET", ts.URL+"/v1/repos/repo1/sessions/sess-001", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestStory18_GetSessionSlice(t *testing.T) {
	ts := setupTestServer()
	defer ts.Close()

	req := authRequest("GET", ts.URL+"/v1/repos/repo1/sessions/sess-001/slice?from_turn=1&to_turn=5", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestStory18_S3KeyFormat(t *testing.T) {
	key := s3.SessionKey("repo1", "sess-001", "chunk-001")
	assert.Equal(t, "repo1/sess-001/chunk-001.jsonl.gz", key)
}
