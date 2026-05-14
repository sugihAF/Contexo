package tests

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/cli"
	"github.com/sugihAF/contexo/internal/store"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

func TestStory22_CommitWithSummaryFlags(t *testing.T) {
	root := initTestCtxDir(t)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{
		"commit", "-m", "Auth feature",
		"--summary", "Added JWT authentication",
		"--summary", "Configured middleware",
		"--feature", "auth",
		"--root", root,
	})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, buf.String(), "Created context commit")

	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	defer db.Close()

	commits, err := db.ListCommits(context.Background(), store.CommitFilter{})
	require.NoError(t, err)
	require.Len(t, commits, 1)
	assert.Len(t, commits[0].Summary, 2)
	assert.Equal(t, "Added JWT authentication", commits[0].Summary[0])
	assert.Equal(t, "Configured middleware", commits[0].Summary[1])
}

func TestStory22_CommitWithDecisionFlags(t *testing.T) {
	root := initTestCtxDir(t)

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{
		"commit", "-m", "Design choices",
		"--decision", "Use JWT:Stateless and scalable",
		"--decision", "Rate limit:Prevent abuse",
		"--root", root,
	})
	require.NoError(t, cmd.Execute())

	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	defer db.Close()

	commits, _ := db.ListCommits(context.Background(), store.CommitFilter{})
	require.Len(t, commits, 1)
	require.Len(t, commits[0].Decisions, 2)
	assert.Equal(t, "Use JWT", commits[0].Decisions[0].Description)
	assert.Equal(t, "Stateless and scalable", commits[0].Decisions[0].Rationale)
}

func TestStory22_CommitWithNextStepFlags(t *testing.T) {
	root := initTestCtxDir(t)

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{
		"commit", "-m", "With next steps",
		"--next-step", "Add tests",
		"--next-step", "Deploy to staging",
		"--root", root,
	})
	require.NoError(t, cmd.Execute())

	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	defer db.Close()

	commits, _ := db.ListCommits(context.Background(), store.CommitFilter{})
	require.Len(t, commits, 1)
	assert.Len(t, commits[0].NextSteps, 2)
	assert.Equal(t, "Add tests", commits[0].NextSteps[0])
}

func TestStory22_CommitWithAuthorFlag(t *testing.T) {
	root := initTestCtxDir(t)

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{
		"commit", "-m", "With author",
		"--author", "John:claude-code",
		"--root", root,
	})
	require.NoError(t, cmd.Execute())

	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	defer db.Close()

	commits, _ := db.ListCommits(context.Background(), store.CommitFilter{})
	require.Len(t, commits, 1)
	assert.Equal(t, "John", commits[0].Author.Name)
	assert.Equal(t, "claude-code", commits[0].Author.Tool)
}

func TestStory22_CommitWithBranchFlag(t *testing.T) {
	root := initTestCtxDir(t)

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{
		"commit", "-m", "With branch",
		"--branch", "feature/onboarding",
		"--root", root,
	})
	require.NoError(t, cmd.Execute())

	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	defer db.Close()

	commits, _ := db.ListCommits(context.Background(), store.CommitFilter{})
	require.Len(t, commits, 1)
	assert.Equal(t, "feature/onboarding", commits[0].Branch)
}

func TestStory22_CommitAllFlagsCombined(t *testing.T) {
	root := initTestCtxDir(t)

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{
		"commit", "-m", "Full commit",
		"--feature", "auth",
		"--summary", "JWT auth",
		"--decision", "Use JWT:Scalable",
		"--next-step", "Add tests",
		"--author", "Dev:codex",
		"--branch", "feature/auth",
		"--root", root,
	})
	require.NoError(t, cmd.Execute())

	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	defer db.Close()

	commits, _ := db.ListCommits(context.Background(), store.CommitFilter{})
	require.Len(t, commits, 1)
	c := commits[0]
	assert.Equal(t, "Full commit", c.Title)
	assert.Equal(t, "auth", c.Feature)
	assert.Len(t, c.Summary, 1)
	assert.Len(t, c.Decisions, 1)
	assert.Len(t, c.NextSteps, 1)
	assert.Equal(t, "Dev", c.Author.Name)
	assert.Equal(t, "codex", c.Author.Tool)
	assert.Equal(t, "feature/auth", c.Branch)
}
