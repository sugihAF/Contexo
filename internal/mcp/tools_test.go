package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/capture"
	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/sync"
)

// setupCaptureServer builds a Server bound to a tmp project that has:
//   - .contexo/ with a concept page and an entity page
//   - credentials + config pointing at the optional httptest server
//   - optionally a pending capture buffer
//
// When pushServer is nil, no creds/server are wired — useful for tests
// that only assert the early-return branches (PUSH_PAUSED or errors).
func setupCaptureServer(t *testing.T, withBuffer bool, pushServer *httptest.Server) (*Server, string) {
	t.Helper()
	projectRoot := t.TempDir()
	contexoDir := config.ContexoDirPath(projectRoot)
	if err := os.MkdirAll(contexoDir, 0o755); err != nil {
		t.Fatalf("mkdir contexo: %v", err)
	}

	store, err := pagestore.Open(contexoDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	concept := &schema.Page{
		Frontmatter: schema.PageFrontmatter{
			Schema: schema.PageSchemaV1, Slug: "foo", Type: schema.TypeConcept,
			Author: "sugihAF", Created: now, Updated: now, Tags: []string{"foo"},
		},
		Body: "## Decision\nUse foo.\n",
	}
	entity := &schema.Page{
		Frontmatter: schema.PageFrontmatter{
			Schema: schema.PageSchemaV1, Slug: "foo-ent", Type: schema.TypeEntity,
			Author: "sugihAF", Created: now, Updated: now, Tags: []string{"foo"},
		},
		Body: "Entity.\n",
	}
	for _, p := range []*schema.Page{concept, entity} {
		if err := store.Write(p); err != nil {
			t.Fatalf("write %s: %v", p.Frontmatter.Slug, err)
		}
	}

	if withBuffer {
		buf := capture.Open(contexoDir, "sess-1")
		if err := buf.AppendTurn(capture.TurnRecord{User: "how?", Assistant: "use Connect", Tools: []string{"Read"}}); err != nil {
			t.Fatalf("buf append 1: %v", err)
		}
		if err := buf.AppendTurn(capture.TurnRecord{User: "wait, negative balance?", Assistant: "use Billing instead"}); err != nil {
			t.Fatalf("buf append 2: %v", err)
		}
	}

	if pushServer != nil {
		if err := config.Save(projectRoot, &config.Config{Version: 1, ServerURL: pushServer.URL, RepoID: "test-repo"}); err != nil {
			t.Fatalf("save config: %v", err)
		}
		if err := config.SaveCredentials(projectRoot, &config.Credentials{
			Token: "test-token", ServerURL: pushServer.URL,
			UserName: "tester", UserEmail: "tester@example.com",
		}); err != nil {
			t.Fatalf("save creds: %v", err)
		}
	}

	return NewServer(store), projectRoot
}

func TestPushHandshakeFiresOnBufferAndConcept(t *testing.T) {
	srv, _ := setupCaptureServer(t, true, httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP push must NOT be called when handshake should pause; got %s", r.URL.Path)
	})))

	res := srv.HandleToolCall(context.Background(), "ctx_push", map[string]interface{}{
		"feature": "foo",
	})
	if res == nil || res.IsError {
		t.Fatalf("expected non-error PUSH_PAUSED, got %+v", res)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "<PUSH_PAUSED") {
		t.Errorf("missing PUSH_PAUSED directive: %s", text)
	}
	if !strings.Contains(text, "distill_done: true") {
		t.Errorf("directive should mention distill_done: %s", text)
	}
	if !strings.Contains(text, "use Billing") {
		t.Errorf("buffer content should be inlined: %s", text)
	}
}

func TestPushSkipsHandshakeWhenNoBuffer(t *testing.T) {
	called := false
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		resp, _ := json.Marshal(sync.PushResponse{NewHead: "abcd1234", Pushed: []sync.PushedFile{{Path: "wiki/concepts/foo.md", SHA: "abcd1234"}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer ps.Close()
	srv, _ := setupCaptureServer(t, false /* no buffer */, ps)

	res := srv.HandleToolCall(context.Background(), "ctx_push", map[string]interface{}{"feature": "foo"})
	if res == nil || res.IsError {
		t.Fatalf("expected ok push, got %+v", res)
	}
	if !called {
		t.Errorf("server push should have been called when no buffer is pending")
	}
}

func TestPushSkipsHandshakeWhenDisabled(t *testing.T) {
	t.Setenv("CONTEXO_DISTILL_DISABLE", "1")
	called := false
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		resp, _ := json.Marshal(sync.PushResponse{NewHead: "ff00", Pushed: []sync.PushedFile{{Path: "wiki/concepts/foo.md", SHA: "ff00"}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer ps.Close()
	srv, _ := setupCaptureServer(t, true /* buffer present */, ps)

	res := srv.HandleToolCall(context.Background(), "ctx_push", map[string]interface{}{"feature": "foo"})
	if res == nil || res.IsError {
		t.Fatalf("expected ok push: %+v", res)
	}
	if !called {
		t.Errorf("env var should bypass handshake")
	}
}

func TestPushSkipsHandshakeWhenNoDistillFlag(t *testing.T) {
	called := false
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		resp, _ := json.Marshal(sync.PushResponse{NewHead: "aa", Pushed: []sync.PushedFile{{Path: "wiki/concepts/foo.md", SHA: "aa"}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer ps.Close()
	srv, _ := setupCaptureServer(t, true, ps)

	res := srv.HandleToolCall(context.Background(), "ctx_push", map[string]interface{}{
		"feature":    "foo",
		"no_distill": true,
	})
	if res == nil || res.IsError {
		t.Fatalf("expected ok: %+v", res)
	}
	if !called {
		t.Errorf("no_distill should bypass handshake")
	}
}

func TestPushSkipsHandshakeWhenNoConceptPage(t *testing.T) {
	called := false
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		resp, _ := json.Marshal(sync.PushResponse{NewHead: "bb", Pushed: []sync.PushedFile{{Path: "wiki/entities/foo-ent.md", SHA: "bb"}}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer ps.Close()
	srv, _ := setupCaptureServer(t, true, ps)

	// Push entity-only: handshake should not fire even though buffer is non-empty.
	res := srv.HandleToolCall(context.Background(), "ctx_push", map[string]interface{}{"type": "entity"})
	if res == nil || res.IsError {
		t.Fatalf("expected ok: %+v", res)
	}
	if !called {
		t.Errorf("entity-only push should not trigger handshake")
	}
}

func TestPushDistillDoneRequiresSourceSlug(t *testing.T) {
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP push must not be called when source_slug is missing")
	}))
	defer ps.Close()
	srv, _ := setupCaptureServer(t, true, ps)
	res := srv.HandleToolCall(context.Background(), "ctx_push", map[string]interface{}{
		"feature":      "foo",
		"distill_done": true,
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected error result: %+v", res)
	}
	if !strings.Contains(res.Content[0].Text, "source_slug") {
		t.Errorf("error should mention missing source_slug: %s", res.Content[0].Text)
	}
}

func TestPushDistillDoneErrorsWhenSourcePageMissing(t *testing.T) {
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP push must not be called when source page is missing")
	}))
	defer ps.Close()
	srv, _ := setupCaptureServer(t, true, ps)
	res := srv.HandleToolCall(context.Background(), "ctx_push", map[string]interface{}{
		"feature":      "foo",
		"distill_done": true,
		"source_slug":  "nonexistent",
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected error: %+v", res)
	}
	if !strings.Contains(res.Content[0].Text, "not found") {
		t.Errorf("error should mention not found: %s", res.Content[0].Text)
	}
}

func TestPushDistillDoneLinksSourceAndArchivesBuffer(t *testing.T) {
	var receivedBody []byte
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = readAll(r.Body)
		resp, _ := json.Marshal(sync.PushResponse{
			NewHead: "deadbeef",
			Pushed: []sync.PushedFile{
				{Path: "wiki/concepts/foo.md", SHA: "deadbeef"},
				{Path: "raw/sessions/2026-05-17-foo.md", SHA: "deadbeef"},
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer ps.Close()

	srv, root := setupCaptureServer(t, true, ps)
	// Pre-create the source page that the agent would have authored.
	now := time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC)
	sourcePage := &schema.Page{
		Frontmatter: schema.PageFrontmatter{
			Schema: schema.PageSchemaV1, Slug: "2026-05-17-foo", Type: schema.TypeSource,
			Author: "tester", Created: now, Updated: now, Tags: []string{"foo"},
		},
		Body: "## Decision\nUse Billing.\n",
	}
	if err := srv.store.Write(sourcePage); err != nil {
		t.Fatalf("write source: %v", err)
	}

	res := srv.HandleToolCall(context.Background(), "ctx_push", map[string]interface{}{
		"feature":      "foo",
		"distill_done": true,
		"source_slug":  "2026-05-17-foo",
	})
	if res == nil || res.IsError {
		t.Fatalf("expected ok push: %+v", res)
	}
	if !strings.Contains(res.Content[0].Text, "Pushed") {
		t.Errorf("expected Pushed in result: %s", res.Content[0].Text)
	}

	// The push payload must include BOTH the concept page (with sources:
	// patched to include the source slug) and the source page itself.
	var req sync.PushRequest
	if err := json.Unmarshal(receivedBody, &req); err != nil {
		t.Fatalf("parse push body: %v", err)
	}
	pathSet := map[string]bool{}
	for _, f := range req.Files {
		pathSet[f.Path] = true
	}
	if !pathSet["wiki/concepts/foo.md"] {
		t.Errorf("concept page missing from push: %v", pathSet)
	}
	if !pathSet["raw/sessions/2026-05-17-foo.md"] {
		t.Errorf("source page missing from push: %v", pathSet)
	}

	// The concept page on disk must now carry sources: [2026-05-17-foo].
	contexoDir := config.ContexoDirPath(root)
	data, err := os.ReadFile(filepath.Join(contexoDir, "wiki", "concepts", "foo.md"))
	if err != nil {
		t.Fatalf("read concept: %v", err)
	}
	patched, err := schema.ParsePage(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(patched.Frontmatter.Sources) != 1 || patched.Frontmatter.Sources[0] != "2026-05-17-foo" {
		t.Errorf("concept sources not patched: %v", patched.Frontmatter.Sources)
	}

	// The buffer must have been archived.
	buf := capture.Open(contexoDir, "sess-1")
	if buf.Exists() {
		t.Errorf("buffer should have been archived after successful distill_done push")
	}
}

func TestCaptureSessionReturnsTemplateAndBuffer(t *testing.T) {
	srv, _ := setupCaptureServer(t, true, nil)
	res := srv.HandleToolCall(context.Background(), "ctx_capture_session", map[string]interface{}{})
	if res == nil || res.IsError {
		t.Fatalf("expected ok: %+v", res)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "CAPTURE_TEMPLATE") {
		t.Errorf("missing template tag: %s", text)
	}
	if !strings.Contains(text, "## Decision") {
		t.Errorf("missing template section: %s", text)
	}
	if !strings.Contains(text, "use Billing") {
		t.Errorf("buffer content should be inlined: %s", text)
	}
}

func TestCaptureSessionEmptyBuffer(t *testing.T) {
	srv, _ := setupCaptureServer(t, false /* no buffer */, nil)
	res := srv.HandleToolCall(context.Background(), "ctx_capture_session", map[string]interface{}{})
	if res == nil || !res.IsError {
		t.Errorf("expected error when no buffer: %+v", res)
	}
}

// readAll is a tiny helper that avoids importing io for one use.
func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}
