package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/server"
	"github.com/sugihAF/contexo/internal/server/service"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
	syncclient "github.com/sugihAF/contexo/internal/sync"
)

func setupSyncTestServer() *httptest.Server {
	store := service.NewMemStore()
	svc := service.New(store)
	validator := func(key string) (string, bool) {
		return "test-user", true
	}
	router := server.NewRouter(svc, validator)
	return httptest.NewServer(router)
}

func TestStory19_PushCommitsToServer(t *testing.T) {
	ts := setupSyncTestServer()
	defer ts.Close()

	client := syncclient.NewClient(ts.URL, "test-key")

	commit := &schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "sync-c1",
		Title:     "Sync test",
		CreatedAt: time.Now().UTC(),
	}

	err := client.PushCommit("repo1", commit)
	require.NoError(t, err)
}

func TestStory19_PushSessionChunk(t *testing.T) {
	ts := setupSyncTestServer()
	defer ts.Close()

	client := syncclient.NewClient(ts.URL, "test-key")
	err := client.PushSessionChunk("repo1", "sess-001", "chunk-001", []byte("compressed data"))
	require.NoError(t, err)
}

func TestStory19_PushMarksSynced(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlitestore.Open(filepath.Join(dir, "test.sqlite"))
	require.NoError(t, err)
	require.NoError(t, db.Migrate())
	defer db.Close()

	ctx := context.Background()
	require.NoError(t, db.MarkSynced(ctx, "commit", "c1"))

	synced, err := db.IsSynced(ctx, "commit", "c1")
	require.NoError(t, err)
	assert.True(t, synced)

	synced2, err := db.IsSynced(ctx, "commit", "c2")
	require.NoError(t, err)
	assert.False(t, synced2)
}

func TestStory19_PullCommitsFromServer(t *testing.T) {
	ts := setupSyncTestServer()
	defer ts.Close()

	client := syncclient.NewClient(ts.URL, "test-key")

	// Push some commits first
	for i := 0; i < 3; i++ {
		commit := &schema.ContextCommit{
			Schema:    "ctx.commit.v1",
			CommitID:  fmt.Sprintf("pull-c%d", i),
			Title:     fmt.Sprintf("Pull test %d", i),
			CreatedAt: time.Now().UTC(),
		}
		require.NoError(t, client.PushCommit("repo1", commit))
	}

	// Pull
	commits, err := client.PullCommits("repo1")
	require.NoError(t, err)
	assert.Len(t, commits, 3)
}

func TestStory19_PullDoesNotDownloadSessions(t *testing.T) {
	// Pull only fetches commit metadata, not session logs
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only /v1/repos/repo1/commits endpoint should be called
		assert.Contains(t, r.URL.Path, "commits")
		assert.NotContains(t, r.URL.Path, "sessions")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]*schema.ContextCommit{})
	}))
	defer ts.Close()

	client := syncclient.NewClient(ts.URL, "test-key")
	_, err := client.PullCommits("repo1")
	require.NoError(t, err)
}

func TestStory19_RoundTripPushPull(t *testing.T) {
	ts := setupSyncTestServer()
	defer ts.Close()

	clientA := syncclient.NewClient(ts.URL, "key-a")
	clientB := syncclient.NewClient(ts.URL, "key-b")

	// A pushes
	commit := &schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "roundtrip-c1",
		Title:     "Round trip test",
		Feature:   "sync",
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, clientA.PushCommit("repo1", commit))

	// B pulls
	commits, err := clientB.PullCommits("repo1")
	require.NoError(t, err)
	require.Len(t, commits, 1)
	assert.Equal(t, "roundtrip-c1", commits[0].CommitID)
	assert.Equal(t, "Round trip test", commits[0].Title)
}
