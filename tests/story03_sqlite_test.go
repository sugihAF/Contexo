package tests

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

func openTestDB(t *testing.T) *sqlitestore.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := sqlitestore.Open(path)
	require.NoError(t, err)
	require.NoError(t, db.Migrate())
	t.Cleanup(func() { db.Close() })
	return db
}

func TestStory03_OpenCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.sqlite")
	db, err := sqlitestore.Open(path)
	require.NoError(t, err)
	defer db.Close()
}

func TestStory03_MigrateCreatesTables(t *testing.T) {
	db := openTestDB(t)

	ctx := context.Background()
	// Verify tables exist by querying them
	_, err := db.RawDB().ExecContext(ctx, `SELECT count(*) FROM sessions`)
	assert.NoError(t, err)
	_, err = db.RawDB().ExecContext(ctx, `SELECT count(*) FROM events`)
	assert.NoError(t, err)
	_, err = db.RawDB().ExecContext(ctx, `SELECT count(*) FROM commits`)
	assert.NoError(t, err)
	_, err = db.RawDB().ExecContext(ctx, `SELECT count(*) FROM commit_files`)
	assert.NoError(t, err)
	_, err = db.RawDB().ExecContext(ctx, `SELECT count(*) FROM commit_symbols`)
	assert.NoError(t, err)
	_, err = db.RawDB().ExecContext(ctx, `SELECT count(*) FROM symbol_index`)
	assert.NoError(t, err)
	_, err = db.RawDB().ExecContext(ctx, `SELECT count(*) FROM git_links`)
	assert.NoError(t, err)
}

func TestStory03_SessionCRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Millisecond)
	meta := &schema.SessionMeta{
		ID:         "sess-001",
		Source:     "claude_code",
		StartedAt:  now,
		EventCount: 5,
		Feature:    "auth",
	}

	err := db.UpsertSession(ctx, meta)
	require.NoError(t, err)

	got, err := db.GetSession(ctx, "sess-001")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "sess-001", got.ID)
	assert.Equal(t, "claude_code", got.Source)
	assert.Equal(t, 5, got.EventCount)
}

func TestStory03_ListCommitsFilteredByFeature(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	commits := []*schema.ContextCommit{
		{Schema: "ctx.commit.v1", CommitID: "c1", Title: "Auth setup", Feature: "auth", CreatedAt: now},
		{Schema: "ctx.commit.v1", CommitID: "c2", Title: "Auth login", Feature: "auth", CreatedAt: now.Add(time.Second)},
		{Schema: "ctx.commit.v1", CommitID: "c3", Title: "DB setup", Feature: "database", CreatedAt: now.Add(2 * time.Second)},
	}
	for _, c := range commits {
		require.NoError(t, db.CreateCommit(ctx, c))
	}

	result, err := db.ListCommits(ctx, store.CommitFilter{Feature: "auth"})
	require.NoError(t, err)
	assert.Len(t, result, 2)
	for _, c := range result {
		assert.Equal(t, "auth", c.Feature)
	}
}

func TestStory03_GitLinksBidirectional(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	commit := &schema.ContextCommit{
		Schema: "ctx.commit.v1", CommitID: "c1", Title: "Test", CreatedAt: now,
	}
	require.NoError(t, db.CreateCommit(ctx, commit))

	require.NoError(t, db.LinkGit(ctx, "abc123", "c1"))

	// git SHA -> commit IDs
	commitIDs, err := db.GetByGitSHA(ctx, "abc123")
	require.NoError(t, err)
	assert.Contains(t, commitIDs, "c1")

	// commit ID -> git SHAs
	shas, err := db.GetGitSHAsByCommit(ctx, "c1")
	require.NoError(t, err)
	assert.Contains(t, shas, "abc123")
}

func TestStory03_SymbolIndexQuery(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	commit := &schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "c1",
		Title:     "Auth handler",
		CreatedAt: now,
		Changes: &schema.ChangeSet{
			Symbols: []string{"auth::Handler", "auth::Login"},
		},
	}
	require.NoError(t, db.CreateCommit(ctx, commit))

	results, err := db.GetBySymbol(ctx, "auth::Handler")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "c1", results[0].CommitID)
}
