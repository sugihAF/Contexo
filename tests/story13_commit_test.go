package tests

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/cli"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

func TestStory13_CommitCreatesEntry(t *testing.T) {
	root := initTestCtxDir(t)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"commit", "-m", "Implement auth module", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "Created context commit")

	// Verify in SQLite
	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	defer db.Close()

	commits, err := db.ListCommits(context.Background(), store.CommitFilter{})
	require.NoError(t, err)
	assert.Len(t, commits, 1)
	assert.Equal(t, "Implement auth module", commits[0].Title)
}

func TestStory13_CommitAutoSelectsSession(t *testing.T) {
	root := initTestCtxDir(t)

	// Seed a session
	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	now := time.Now().UTC()
	db.UpsertSession(context.Background(), &schema.SessionMeta{
		ID: "auto-sess", Source: "test", StartedAt: now, EventCount: 1,
	})
	db.Close()

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"commit", "-m", "Auto evidence test", "--root", root})
	require.NoError(t, cmd.Execute())

	// Check the commit has evidence
	db2, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	defer db2.Close()

	commits, _ := db2.ListCommits(context.Background(), store.CommitFilter{})
	require.Len(t, commits, 1)
	assert.Len(t, commits[0].Evidence, 1)
	assert.Equal(t, "auto-sess", commits[0].Evidence[0].SessionID)
}

func TestStory13_CommitWithSessionAndTurns(t *testing.T) {
	root := initTestCtxDir(t)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"commit", "-m", "Specific evidence", "--from-session", "my-sess", "--turns", "5-10", "--root", root})
	require.NoError(t, cmd.Execute())

	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	defer db.Close()

	commits, _ := db.ListCommits(context.Background(), store.CommitFilter{})
	require.Len(t, commits, 1)
	require.Len(t, commits[0].Evidence, 1)
	assert.Equal(t, "my-sess", commits[0].Evidence[0].SessionID)
	assert.Equal(t, 5, commits[0].Evidence[0].FromTurn)
	assert.Equal(t, 10, commits[0].Evidence[0].ToTurn)
}

func TestStory13_LogListsCommits(t *testing.T) {
	root := initTestCtxDir(t)

	// Create two commits
	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"commit", "-m", "First commit", "--feature", "auth", "--root", root})
	require.NoError(t, cmd1.Execute())

	cmd2 := cli.NewRootCmd()
	cmd2.SetArgs([]string{"commit", "-m", "Second commit", "--feature", "auth", "--root", root})
	require.NoError(t, cmd2.Execute())

	var buf bytes.Buffer
	cmd3 := cli.NewRootCmd()
	cmd3.SetOut(&buf)
	cmd3.SetArgs([]string{"log", "--root", root})
	require.NoError(t, cmd3.Execute())

	output := buf.String()
	assert.Contains(t, output, "First commit")
	assert.Contains(t, output, "Second commit")
}

func TestStory13_LogFilterByFeature(t *testing.T) {
	root := initTestCtxDir(t)

	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"commit", "-m", "Auth commit", "--feature", "auth", "--root", root})
	require.NoError(t, cmd1.Execute())

	cmd2 := cli.NewRootCmd()
	cmd2.SetArgs([]string{"commit", "-m", "DB commit", "--feature", "database", "--root", root})
	require.NoError(t, cmd2.Execute())

	var buf bytes.Buffer
	cmd3 := cli.NewRootCmd()
	cmd3.SetOut(&buf)
	cmd3.SetArgs([]string{"log", "--feature", "auth", "--root", root})
	require.NoError(t, cmd3.Execute())

	output := buf.String()
	assert.Contains(t, output, "Auth commit")
	assert.NotContains(t, output, "DB commit")
}

func TestStory13_ShowDisplaysCommit(t *testing.T) {
	root := initTestCtxDir(t)

	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"commit", "-m", "Show test", "--root", root})
	require.NoError(t, cmd1.Execute())

	// Get the commit ID
	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	commits, _ := db.ListCommits(context.Background(), store.CommitFilter{})
	db.Close()
	require.Len(t, commits, 1)

	var buf bytes.Buffer
	cmd2 := cli.NewRootCmd()
	cmd2.SetOut(&buf)
	cmd2.SetArgs([]string{"show", commits[0].CommitID, "--root", root})
	require.NoError(t, cmd2.Execute())

	assert.Contains(t, buf.String(), "Show test")
	assert.Contains(t, buf.String(), "ctx.commit.v1")
}

func TestStory13_LinkCreatesGitLink(t *testing.T) {
	root := initTestCtxDir(t)

	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"commit", "-m", "Link test", "--root", root})
	require.NoError(t, cmd1.Execute())

	var buf bytes.Buffer
	cmd2 := cli.NewRootCmd()
	cmd2.SetOut(&buf)
	cmd2.SetArgs([]string{"link", "abc123def456789012345678901234567890abcd", "--root", root})
	require.NoError(t, cmd2.Execute())

	assert.Contains(t, buf.String(), "Linked git")
}
