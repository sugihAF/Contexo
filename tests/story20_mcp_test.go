package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpserver "github.com/sugihAF/contexo/internal/mcp"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/jsonl"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

func setupMCPServer(t *testing.T) (*mcpserver.Server, *sqlitestore.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.sqlite")
	db, err := sqlitestore.Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, db.Migrate())
	t.Cleanup(func() { db.Close() })

	sessionsDir := filepath.Join(dir, "sessions")
	srv := mcpserver.NewServer(db, db, sessionsDir)

	return srv, db, dir
}

func TestStory20_ListResources(t *testing.T) {
	srv, _, _ := setupMCPServer(t)

	resources := srv.ListResources()
	assert.GreaterOrEqual(t, len(resources), 5)

	// Check names
	names := make(map[string]bool)
	for _, r := range resources {
		names[r.Name] = true
	}
	assert.True(t, names["Context Commit List"])
	assert.True(t, names["Context Commit Detail"])
	assert.True(t, names["Session Slice"])
	assert.True(t, names["Feature Overview"])
	assert.True(t, names["Activity Log"])
}

func TestStory20_ResourceTemplates(t *testing.T) {
	srv, _, _ := setupMCPServer(t)

	resources := srv.ListResources()
	for _, r := range resources {
		assert.Contains(t, r.URITemplate, "ctx://")
	}
}

func TestStory20_ReadCommitList(t *testing.T) {
	srv, db, _ := setupMCPServer(t)
	ctx := context.Background()
	now := time.Now().UTC()

	db.CreateCommit(ctx, &schema.ContextCommit{
		Schema: "ctx.commit.v1", CommitID: "mcp-c1", Title: "MCP test", CreatedAt: now,
	})

	data, err := srv.ReadCommitList(ctx, "")
	require.NoError(t, err)

	var commits []*schema.ContextCommit
	require.NoError(t, json.Unmarshal(data, &commits))
	assert.Len(t, commits, 1)
	assert.Equal(t, "MCP test", commits[0].Title)
}

func TestStory20_ReadCommitDetail(t *testing.T) {
	srv, db, _ := setupMCPServer(t)
	ctx := context.Background()
	now := time.Now().UTC()

	db.CreateCommit(ctx, &schema.ContextCommit{
		Schema: "ctx.commit.v1", CommitID: "mcp-c2", Title: "Detail test",
		Feature: "auth", CreatedAt: now,
	})

	data, err := srv.ReadCommitDetail(ctx, "mcp-c2")
	require.NoError(t, err)

	var commit schema.ContextCommit
	require.NoError(t, json.Unmarshal(data, &commit))
	assert.Equal(t, "Detail test", commit.Title)
	assert.Equal(t, "auth", commit.Feature)
}

func TestStory20_ReadSessionSlice(t *testing.T) {
	srv, _, dir := setupMCPServer(t)

	// Write a JSONL session file
	jsonlPath := filepath.Join(dir, "sessions", "test", "mcp-sess.jsonl")
	w, err := jsonl.NewWriter(jsonlPath)
	require.NoError(t, err)

	now := time.Now().UTC()
	for i := 1; i <= 5; i++ {
		w.Append(&schema.SessionEvent{
			Schema: "ctx.session_event.v1", EventID: fmt.Sprintf("e%d", i),
			Ts: now, Session: schema.SessionRef{ID: "mcp-sess", Source: "test"},
			Type: "user_message", Turn: i,
			Content: schema.Content{Text: fmt.Sprintf("msg %d", i)},
		})
	}

	ctx := context.Background()
	data, err := srv.ReadSessionSlice(ctx, "mcp-sess", "test", 2, 4)
	require.NoError(t, err)

	var events []*schema.SessionEvent
	require.NoError(t, json.Unmarshal(data, &events))
	assert.Len(t, events, 3) // turns 2, 3, 4
}

func TestStory20_ReadFeatureOverview(t *testing.T) {
	srv, db, _ := setupMCPServer(t)
	ctx := context.Background()
	now := time.Now().UTC()

	db.PutOverview(ctx, &schema.FeatureOverview{
		Schema: "ctx.feature_overview.v1", RepoID: "", Feature: "auth",
		Summary: "Auth feature", Status: "active", UpdatedAt: now,
	})

	data, err := srv.ReadFeatureOverview(ctx, "", "auth")
	require.NoError(t, err)

	var overview schema.FeatureOverview
	require.NoError(t, json.Unmarshal(data, &overview))
	assert.Equal(t, "auth", overview.Feature)
	assert.Equal(t, "Auth feature", overview.Summary)
}

func TestStory20_ReadActivityLog(t *testing.T) {
	srv, db, _ := setupMCPServer(t)
	ctx := context.Background()
	now := time.Now().UTC()

	db.AppendActivity(ctx, &schema.ActivityEntry{
		ID: "mcp-act-1", RepoID: "", Feature: "auth",
		Type: "commit", Summary: "Added login", Actor: "dev", Ts: now,
	})

	data, err := srv.ReadActivityLog(ctx, "", "auth", 10)
	require.NoError(t, err)

	var entries []*schema.ActivityEntry
	require.NoError(t, json.Unmarshal(data, &entries))
	assert.Len(t, entries, 1)
	assert.Equal(t, "Added login", entries[0].Summary)
}

func TestStory20_ResourceAnnotationPriorities(t *testing.T) {
	srv, _, _ := setupMCPServer(t)

	resources := srv.ListResources()
	priorities := make(map[string]float64)
	for _, r := range resources {
		if p, ok := r.Annotations["priority"].(float64); ok {
			priorities[r.Name] = p
		}
	}

	assert.InDelta(t, 0.6, priorities["Context Commit List"], 0.01)
	assert.InDelta(t, 0.8, priorities["Context Commit Detail"], 0.01)
	assert.InDelta(t, 0.4, priorities["Session Slice"], 0.01)
}

func TestStory20_HandleResourceRead(t *testing.T) {
	srv, db, _ := setupMCPServer(t)
	ctx := context.Background()
	now := time.Now().UTC()

	db.CreateCommit(ctx, &schema.ContextCommit{
		Schema: "ctx.commit.v1", CommitID: "handle-c1", Title: "Handle test", CreatedAt: now,
	})

	data, mimeType, err := srv.HandleResourceRead(ctx, "ctx://commits")
	require.NoError(t, err)
	assert.Equal(t, "application/json", mimeType)
	assert.Contains(t, string(data), "Handle test")
}
