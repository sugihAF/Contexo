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
	"github.com/sugihAF/contexo/internal/store/jsonl"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

func seedSession(t *testing.T, root string) {
	t.Helper()
	ctxDir := filepath.Join(root, ".ctx")
	db, err := sqlitestore.Open(filepath.Join(ctxDir, "index.sqlite"))
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UTC()
	meta := &schema.SessionMeta{
		ID:         "sess-test-001",
		Source:     "test",
		StartedAt:  now,
		EventCount: 3,
		Feature:    "auth",
	}
	require.NoError(t, db.UpsertSession(context.Background(), meta))

	// Write JSONL
	jsonlPath := filepath.Join(ctxDir, "sessions", "test", "sess-test-001.jsonl")
	w, err := jsonl.NewWriter(jsonlPath)
	require.NoError(t, err)

	for i := 1; i <= 10; i++ {
		evt := &schema.SessionEvent{
			Schema:  "ctx.session_event.v1",
			EventID: "evt-" + string(rune('0'+i)),
			Ts:      now,
			Session: schema.SessionRef{ID: "sess-test-001", Source: "test"},
			Type:    "user_message",
			Turn:    i,
			Actor:   schema.ActorRef{Role: "user"},
			Content: schema.Content{Text: "Message " + string(rune('0'+i))},
		}
		require.NoError(t, w.Append(evt))
	}
}

func TestStory12_SessionLs(t *testing.T) {
	root := initTestCtxDir(t)
	seedSession(t, root)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"session", "ls", "--root", root})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, "sess-test-001")
	assert.Contains(t, output, "test")
}

func TestStory12_SessionShow(t *testing.T) {
	root := initTestCtxDir(t)
	seedSession(t, root)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"session", "show", "sess-test-001", "--root", root})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, "Session: sess-test-001")
	assert.Contains(t, output, "user_message")
}

func TestStory12_SessionShowTurnRange(t *testing.T) {
	root := initTestCtxDir(t)
	seedSession(t, root)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"session", "show", "sess-test-001", "--turns", "5-10", "--root", root})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	assert.Contains(t, output, "Turn 5")
	assert.NotContains(t, output, "Turn 1]")
}

func TestStory12_SessionLsFilterByFeature(t *testing.T) {
	root := initTestCtxDir(t)
	seedSession(t, root)

	var buf bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"session", "ls", "--feature", "auth", "--root", root})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "sess-test-001")
}
