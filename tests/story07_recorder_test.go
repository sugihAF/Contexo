package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sugihAF/contexo/internal/recorder"
	"github.com/sugihAF/contexo/internal/schema"
	boltdbstore "github.com/sugihAF/contexo/internal/store/boltdb"
	"github.com/sugihAF/contexo/internal/store/jsonl"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

func setupRecorder(t *testing.T) (*recorder.Recorder, *recorder.HTTPServer, string) {
	t.Helper()
	dir := t.TempDir()
	ctxDir := filepath.Join(dir, ".ctx")

	db, err := sqlitestore.Open(filepath.Join(ctxDir, "index.sqlite"))
	require.NoError(t, err)
	require.NoError(t, db.Migrate())

	blobs, err := boltdbstore.New(filepath.Join(ctxDir, "blobs.db"), filepath.Join(ctxDir, "blobs"))
	require.NoError(t, err)

	rec := recorder.New(ctxDir, db, blobs)
	srv := recorder.NewHTTPServer(rec, 0) // port 0 = random

	// Use port 0 — need to start with a listener on random port
	require.NoError(t, srv.Start())

	t.Cleanup(func() {
		srv.Stop()
		blobs.Close()
		db.Close()
	})

	return rec, srv, ctxDir
}

func postEvent(t *testing.T, addr string, event *schema.SessionEvent) *http.Response {
	t.Helper()
	data, err := json.Marshal(event)
	require.NoError(t, err)

	resp, err := http.Post(fmt.Sprintf("http://%s/event", addr), "application/json", bytes.NewReader(data))
	require.NoError(t, err)
	return resp
}

func TestStory07_HTTPStartAndHealth(t *testing.T) {
	_, srv, _ := setupRecorder(t)

	resp, err := http.Get(fmt.Sprintf("http://%s/health", srv.Addr()))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestStory07_PostValidEvent(t *testing.T) {
	_, srv, _ := setupRecorder(t)

	event := &schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: "evt-001",
		Ts:      time.Now().UTC(),
		Session: schema.SessionRef{ID: "sess-001", Source: "test"},
		Type:    "user_message",
		Turn:    1,
		Content: schema.Content{Text: "Hello"},
	}

	resp := postEvent(t, srv.Addr(), event)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestStory07_EventWrittenToJSONL(t *testing.T) {
	_, srv, ctxDir := setupRecorder(t)

	event := &schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: "evt-002",
		Ts:      time.Now().UTC(),
		Session: schema.SessionRef{ID: "sess-002", Source: "test"},
		Type:    "user_message",
		Turn:    1,
		Content: schema.Content{Text: "Test message"},
	}

	resp := postEvent(t, srv.Addr(), event)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Read JSONL file
	path := filepath.Join(ctxDir, "sessions", "test", "sess-002.jsonl")
	reader := jsonl.NewReader(path)
	events, err := reader.ReadAll()
	require.NoError(t, err)
	assert.Len(t, events, 1)
	assert.Equal(t, "evt-002", events[0].EventID)
}

func TestStory07_EventIndexedInSQLite(t *testing.T) {
	_, srv, ctxDir := setupRecorder(t)

	event := &schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: "evt-003",
		Ts:      time.Now().UTC(),
		Session: schema.SessionRef{ID: "sess-003", Source: "test"},
		Type:    "assistant_message",
		Turn:    2,
		Content: schema.Content{Text: "Response"},
	}

	resp := postEvent(t, srv.Addr(), event)
	resp.Body.Close()

	db, err := sqlitestore.Open(filepath.Join(ctxDir, "index.sqlite"))
	require.NoError(t, err)
	defer db.Close()

	session, err := db.GetSession(context.Background(), "sess-003")
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, "test", session.Source)
}

func TestStory07_RedactionRunsBeforePersistence(t *testing.T) {
	_, srv, ctxDir := setupRecorder(t)

	event := &schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: "evt-004",
		Ts:      time.Now().UTC(),
		Session: schema.SessionRef{ID: "sess-004", Source: "test"},
		Type:    "user_message",
		Turn:    1,
		Content: schema.Content{Text: "My key is AKIAIOSFODNN7EXAMPLE"},
	}

	resp := postEvent(t, srv.Addr(), event)
	resp.Body.Close()

	path := filepath.Join(ctxDir, "sessions", "test", "sess-004.jsonl")
	reader := jsonl.NewReader(path)
	events, err := reader.ReadAll()
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Contains(t, events[0].Content.Text, "[REDACTED:aws_key]")
	assert.NotContains(t, events[0].Content.Text, "AKIAIOSFODNN7EXAMPLE")
}

func TestStory07_LargeContentStoredAsBlob(t *testing.T) {
	rec, srv, ctxDir := setupRecorder(t)
	rec.BlobThreshold = 100 // lower threshold for testing

	largeText := strings.Repeat("x", 200)
	event := &schema.SessionEvent{
		Schema:  "ctx.session_event.v1",
		EventID: "evt-005",
		Ts:      time.Now().UTC(),
		Session: schema.SessionRef{ID: "sess-005", Source: "test"},
		Type:    "assistant_message",
		Turn:    1,
		Content: schema.Content{Text: largeText},
	}

	resp := postEvent(t, srv.Addr(), event)
	resp.Body.Close()

	path := filepath.Join(ctxDir, "sessions", "test", "sess-005.jsonl")
	reader := jsonl.NewReader(path)
	events, err := reader.ReadAll()
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.True(t, strings.HasPrefix(events[0].Content.Text, "[blob:"))
}

func TestStory07_InvalidJSONReturns400(t *testing.T) {
	_, srv, _ := setupRecorder(t)

	resp, err := http.Post(
		fmt.Sprintf("http://%s/event", srv.Addr()),
		"application/json",
		bytes.NewReader([]byte(`{invalid json`)),
	)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
