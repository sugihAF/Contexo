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
	"github.com/sugihAF/contexo/internal/symbols"
)

func TestStory15_BlameFindsCommits(t *testing.T) {
	root := initTestCtxDir(t)

	db, err := sqlitestore.Open(filepath.Join(root, ".ctx", "index.sqlite"))
	require.NoError(t, err)

	now := time.Now().UTC()
	commit := &schema.ContextCommit{
		Schema:    "ctx.commit.v1",
		CommitID:  "blame-c1",
		Title:     "Add auth handler",
		CreatedAt: now,
		Evidence:  []schema.Evidence{{SessionID: "s1", FromTurn: 1, ToTurn: 5}},
		Changes: &schema.ChangeSet{
			Symbols: []string{symbols.EncodeSymbolKey("auth/handler.go", "HandleLogin")},
		},
	}
	require.NoError(t, db.CreateCommit(context.Background(), commit))
	db.Close()

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"blame", "auth/handler.go#HandleLogin", "--root", root})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, "blame-c1")
	assert.Contains(t, output, "Add auth handler")
	assert.Contains(t, output, "Evidence")
}

func TestStory15_SymbolKeyEncoding(t *testing.T) {
	key := symbols.EncodeSymbolKey("auth/handler.go", "HandleLogin")
	assert.Equal(t, "auth/handler.go::HandleLogin", key)

	file, sym := symbols.DecodeSymbolKey(key)
	assert.Equal(t, "auth/handler.go", file)
	assert.Equal(t, "HandleLogin", sym)
}

func TestStory15_UnknownSymbolReturnsEmpty(t *testing.T) {
	root := initTestCtxDir(t)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"blame", "nonexistent.go#NoFunc", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "No context found")
}

func TestStory15_ParseBlameArg(t *testing.T) {
	file, sym := symbols.ParseBlameArg("auth/handler.go#HandleLogin")
	assert.Equal(t, "auth/handler.go", file)
	assert.Equal(t, "HandleLogin", sym)

	file2, sym2 := symbols.ParseBlameArg("just-a-file.go")
	assert.Equal(t, "just-a-file.go", file2)
	assert.Equal(t, "", sym2)
}
