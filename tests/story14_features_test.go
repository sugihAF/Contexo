package tests

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/cli"
	"github.com/sugihAF/contexo/internal/schema"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

func TestStory14_PutGetOverviewRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	overview := &schema.FeatureOverview{
		Schema:    "ctx.feature_overview.v1",
		RepoID:    "repo-001",
		Feature:   "auth",
		Summary:   "Authentication feature",
		Status:    "in_progress",
		CommitIDs: []string{"c1", "c2"},
		UpdatedAt: now,
	}

	require.NoError(t, db.PutOverview(ctx, overview))

	got, err := db.GetOverview(ctx, "repo-001", "auth")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "auth", got.Feature)
	assert.Equal(t, "Authentication feature", got.Summary)
	assert.Len(t, got.CommitIDs, 2)
}

func TestStory14_AppendListActivity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		entry := &schema.ActivityEntry{
			ID:      fmt.Sprintf("act-%d", i),
			RepoID:  "repo-001",
			Feature: "auth",
			Type:    "commit",
			Summary: fmt.Sprintf("Activity %d", i),
			Actor:   "dev",
			Ts:      now.Add(time.Duration(i) * time.Minute),
		}
		require.NoError(t, db.AppendActivity(ctx, entry))
	}

	entries, err := db.ListActivity(ctx, "repo-001", "auth", 3)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
	// Should be reverse chronological
	assert.Equal(t, "Activity 4", entries[0].Summary)
}

func TestStory14_ContextFeatureCommand(t *testing.T) {
	root := initTestCtxDir(t)

	// Seed data
	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)

	now := time.Now().UTC()
	overview := &schema.FeatureOverview{
		Schema:    "ctx.feature_overview.v1",
		RepoID:    "",
		Feature:   "auth",
		Summary:   "Auth system",
		Status:    "active",
		UpdatedAt: now,
	}
	db.PutOverview(context.Background(), overview)
	db.Close()

	// Create a commit for the feature
	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"commit", "-m", "Auth setup", "--feature", "auth", "--root", root})
	require.NoError(t, cmd1.Execute())

	var buf bytes.Buffer
	cmd2 := cli.NewRootCmd()
	cmd2.SetOut(&buf)
	cmd2.SetArgs([]string{"context", "--feature", "auth", "--root", root})
	require.NoError(t, cmd2.Execute())

	output := buf.String()
	assert.Contains(t, output, "auth")
	assert.Contains(t, output, "Auth setup")
}

func TestStory14_ContextLogCommand(t *testing.T) {
	root := initTestCtxDir(t)

	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)

	now := time.Now().UTC()
	entry := &schema.ActivityEntry{
		ID: "act-1", RepoID: "", Feature: "auth",
		Type: "commit", Summary: "Added login", Actor: "dev", Ts: now,
	}
	db.AppendActivity(context.Background(), entry)
	db.Close()

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"context", "--feature", "auth", "--log", "5", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "Added login")
}

func TestStory14_ContextMetadataCommand(t *testing.T) {
	root := initTestCtxDir(t)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"context", "--metadata", "--root", root})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, "Configuration")
	assert.Contains(t, output, "recorder_port")
}
