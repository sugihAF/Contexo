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

func TestStory23_LinkWithCommitFlag(t *testing.T) {
	root := initTestCtxDir(t)

	// Create two commits
	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"commit", "-m", "First commit", "--root", root})
	require.NoError(t, cmd1.Execute())

	cmd2 := cli.NewRootCmd()
	cmd2.SetArgs([]string{"commit", "-m", "Second commit", "--root", root})
	require.NoError(t, cmd2.Execute())

	// Get both commit IDs
	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)

	commits, _ := db.ListCommits(context.Background(), store.CommitFilter{})
	require.Len(t, commits, 2)
	db.Close()

	// The first commit created is at index 1 (list is DESC by created_at)
	firstCommitID := commits[1].CommitID

	// Link with --commit flag pointing to the first (not latest) commit
	var buf bytes.Buffer
	cmd3 := cli.NewRootCmd()
	cmd3.SetOut(&buf)
	cmd3.SetArgs([]string{"link", "abc123def4567890", "--commit", firstCommitID, "--root", root})
	require.NoError(t, cmd3.Execute())

	output := buf.String()
	assert.Contains(t, output, "Linked git")
	assert.Contains(t, output, shortIDForTest(firstCommitID))

	// Verify the link is to the first commit, not the latest
	db2, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	defer db2.Close()

	linkedIDs, err := db2.GetByGitSHA(context.Background(), "abc123def4567890")
	require.NoError(t, err)
	require.Len(t, linkedIDs, 1)
	assert.Equal(t, firstCommitID, linkedIDs[0])
}

func TestStory23_LinkWithoutCommitFlagUsesLatest(t *testing.T) {
	root := initTestCtxDir(t)

	// Create two commits
	cmd1 := cli.NewRootCmd()
	cmd1.SetArgs([]string{"commit", "-m", "First", "--root", root})
	require.NoError(t, cmd1.Execute())

	cmd2 := cli.NewRootCmd()
	cmd2.SetArgs([]string{"commit", "-m", "Second", "--root", root})
	require.NoError(t, cmd2.Execute())

	// Get the latest commit ID
	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	commits, _ := db.ListCommits(context.Background(), store.CommitFilter{Limit: 1})
	latestID := commits[0].CommitID
	db.Close()

	// Link without --commit flag
	cmd3 := cli.NewRootCmd()
	cmd3.SetArgs([]string{"link", "deadbeef12345678", "--root", root})
	require.NoError(t, cmd3.Execute())

	// Verify link goes to latest commit
	db2, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)
	defer db2.Close()

	linkedIDs, _ := db2.GetByGitSHA(context.Background(), "deadbeef12345678")
	require.Len(t, linkedIDs, 1)
	assert.Equal(t, latestID, linkedIDs[0])
}

func shortIDForTest(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
