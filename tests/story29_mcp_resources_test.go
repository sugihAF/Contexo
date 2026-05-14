package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/mcp"
	"github.com/sugihAF/contexo/internal/schema"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

func setupMCPTestServer(t *testing.T) (*mcp.Server, *sqlitestore.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := sqlitestore.Open(path)
	require.NoError(t, err)
	require.NoError(t, db.Migrate())
	t.Cleanup(func() { db.Close() })

	sessDir := filepath.Join(t.TempDir(), "sessions")
	srv := mcp.NewServer(db, db, sessDir)
	return srv, db
}

func TestStory29_ListResourcesIncludesNewTemplates(t *testing.T) {
	srv, _ := setupMCPTestServer(t)

	templates := srv.ListResources()
	// Should have at least 8 templates (original 5 + 3 new)
	assert.GreaterOrEqual(t, len(templates), 8)

	// Check new templates exist
	names := make(map[string]bool)
	for _, tmpl := range templates {
		names[tmpl.Name] = true
	}

	assert.True(t, names["Feature List"], "should have Feature List template")
	assert.True(t, names["Context Level"], "should have Context Level template")
	assert.True(t, names["Symbol Blame"], "should have Symbol Blame template")
}

func TestStory29_ReadFeatureList(t *testing.T) {
	srv, db := setupMCPTestServer(t)
	ctx := context.Background()

	// Create commits with features
	now := time.Now().UTC()
	for _, f := range []string{"auth", "database", "auth"} {
		commit := &schema.ContextCommit{
			Schema:    "ctx.commit.v1",
			CommitID:  "mcp-feat-" + f + "-" + time.Now().String(),
			Title:     f + " commit",
			Feature:   f,
			CreatedAt: now,
		}
		require.NoError(t, db.CreateCommit(ctx, commit))
		now = now.Add(time.Millisecond)
	}

	// Read features via MCP
	data, mime, err := srv.HandleResourceRead(ctx, "ctx://features")
	require.NoError(t, err)
	assert.Equal(t, "application/json", mime)

	var features []map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &features))
	assert.Len(t, features, 2) // auth + database (deduplicated)

	featureNames := make(map[string]bool)
	for _, f := range features {
		featureNames[f["feature"].(string)] = true
	}
	assert.True(t, featureNames["auth"])
	assert.True(t, featureNames["database"])
}

func TestStory29_ReadContextLevelFeature(t *testing.T) {
	srv, db := setupMCPTestServer(t)
	ctx := context.Background()

	// Create a feature overview
	now := time.Now().UTC()
	overview := &schema.FeatureOverview{
		Schema:  "ctx.feature_overview.v1",
		RepoID:  "",
		Feature: "auth",
		Summary: "Auth system",
		Status:  "active",
		UpdatedAt: now,
	}
	require.NoError(t, db.PutOverview(ctx, overview))

	// Read context level=feature
	data, mime, err := srv.HandleResourceRead(ctx, "ctx://context?level=feature&feature=auth")
	require.NoError(t, err)
	assert.Equal(t, "application/json", mime)

	var result schema.FeatureOverview
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, "auth", result.Feature)
	assert.Equal(t, "Auth system", result.Summary)
}

func TestStory29_ReadContextLevelLog(t *testing.T) {
	srv, db := setupMCPTestServer(t)
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		commit := &schema.ContextCommit{
			Schema:    "ctx.commit.v1",
			CommitID:  "log-" + time.Now().String(),
			Title:     "Log commit",
			Feature:   "auth",
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, db.CreateCommit(ctx, commit))
	}

	data, mime, err := srv.HandleResourceRead(ctx, "ctx://context?level=log&feature=auth&limit=10")
	require.NoError(t, err)
	assert.Equal(t, "application/json", mime)

	var commits []*schema.ContextCommit
	require.NoError(t, json.Unmarshal(data, &commits))
	assert.Len(t, commits, 3)
}

func TestStory29_ReadContextLevelMetadata(t *testing.T) {
	srv, _ := setupMCPTestServer(t)
	ctx := context.Background()

	data, _, err := srv.HandleResourceRead(ctx, "ctx://context?level=metadata")
	require.NoError(t, err)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, "metadata", result["level"])
}

func TestStory29_ReadSymbolBlame(t *testing.T) {
	srv, db := setupMCPTestServer(t)
	ctx := context.Background()

	now := time.Now().UTC()
	commit := &schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "blame-mcp-1",
		Title:     "Auth handler",
		CreatedAt: now,
		Changes: &schema.ChangeSet{
			Symbols: []string{"auth::Handler"},
		},
	}
	require.NoError(t, db.CreateCommit(ctx, commit))

	data, mime, err := srv.HandleResourceRead(ctx, "ctx://blame/auth::Handler")
	require.NoError(t, err)
	assert.Equal(t, "application/json", mime)

	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, "auth::Handler", result["symbol_key"])
	commits := result["commits"].([]interface{})
	assert.Len(t, commits, 1)
}

func TestStory29_ReadContextUnknownLevel(t *testing.T) {
	srv, _ := setupMCPTestServer(t)
	ctx := context.Background()

	_, _, err := srv.HandleResourceRead(ctx, "ctx://context?level=invalid")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown context level")
}

func TestStory29_ReadUnknownResource(t *testing.T) {
	srv, _ := setupMCPTestServer(t)
	ctx := context.Background()

	_, _, err := srv.HandleResourceRead(ctx, "ctx://nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown resource")
}

