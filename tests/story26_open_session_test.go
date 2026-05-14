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
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

func TestStory26_OpenSessionShowsEvidence(t *testing.T) {
	root := initTestCtxDir(t)

	// Create a commit with evidence
	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)

	now := time.Now().UTC()
	commit := &schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "open-sess-commit",
		Title:     "Test open-session",
		CreatedAt: now,
		Evidence: []schema.Evidence{
			{SessionID: "test-sess-001", FromTurn: 1, ToTurn: 5, Source: "claude_code"},
		},
	}
	require.NoError(t, db.CreateCommit(context.Background(), commit))
	db.Close()

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"open-session", "open-sess-commit", "--root", root})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, "Evidence: session test-sess-001")
	assert.Contains(t, output, "turns 1-5")
}

func TestStory26_OpenSessionNoEvidence(t *testing.T) {
	root := initTestCtxDir(t)

	// Create a commit without evidence
	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)

	now := time.Now().UTC()
	commit := &schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "no-evidence-commit",
		Title:     "No evidence",
		CreatedAt: now,
	}
	require.NoError(t, db.CreateCommit(context.Background(), commit))
	db.Close()

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"open-session", "no-evidence-commit", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "No evidence sessions")
}

func TestStory26_OpenSessionCommitNotFound(t *testing.T) {
	root := initTestCtxDir(t)

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"open-session", "nonexistent-id", "--root", root})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "commit not found")
}

func TestStory26_OpenSessionDisplaysSessionMeta(t *testing.T) {
	root := initTestCtxDir(t)

	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)

	now := time.Now().UTC()

	// Seed a session
	db.UpsertSession(context.Background(), &schema.SessionMeta{
		ID: "meta-sess", Source: "claude_code", StartedAt: now, EventCount: 3,
	})

	commit := &schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "meta-commit",
		Title:     "Has session",
		CreatedAt: now,
		Evidence: []schema.Evidence{
			{SessionID: "meta-sess", FromTurn: 0, ToTurn: 0},
		},
	}
	require.NoError(t, db.CreateCommit(context.Background(), commit))
	db.Close()

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"open-session", "meta-commit", "--root", root})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, "Evidence: session meta-sess")
}
