package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/capture"
	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/sync"
)

// wp03Token is the bearer every resolution test stores in the credentials.
const wp03Token = "ctxp_wp03_mcp_trap_token"

// wp03IsolateHome points HOME and USERPROFILE (os.UserHomeDir on Windows) at
// a temp dir, so no test can read the developer's real global credentials
// and send them anywhere. t.Setenv forbids t.Parallel in the caller.
func wp03IsolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// wp03Hit is one request the trap server A received.
type wp03Hit struct {
	Method, Path, Auth string
}

// wp03Trap is the MCP copy of the cli trap pair (package cli imports mcp, so
// the helper cannot be shared): A records every request and answers it; B
// fails the test on any request. The credentials point at A and the
// workspace config at B.
type wp03Trap struct {
	A, B *httptest.Server

	mu   gosync.Mutex
	hits []wp03Hit
}

// wp03TrapServers answers every route on A generically, so the WP-2.1
// rewrites of push and pull that call new routes keep passing.
func wp03TrapServers(t *testing.T) *wp03Trap {
	t.Helper()
	return wp03TrapServersWith(t, http.HandlerFunc(wp03GenericA))
}

// wp03TrapServersWith is wp03TrapServers with A's answers served by h. A
// still records each request before delegating to h.
func wp03TrapServersWith(t *testing.T, h http.Handler) *wp03Trap {
	t.Helper()
	tr := &wp03Trap{}
	tr.A = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr.mu.Lock()
		tr.hits = append(tr.hits, wp03Hit{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization")})
		tr.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	tr.B = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("workspace server B received %s %s (Authorization %q); requests must go to the credentials' server",
			r.Method, r.URL.Path, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(tr.A.Close)
	t.Cleanup(tr.B.Close)
	return tr
}

// wp03GenericA is trap A's default answer for every route.
func wp03GenericA(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(p, "/pages"):
		_, _ = w.Write([]byte(`{"pages":[]}`))
	case r.Method == http.MethodGet && strings.Contains(p, "/pages/"):
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"page not found","code":"page_not_found"}`))
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/sync/push"):
		_, _ = w.Write([]byte(`{"pushed":[],"conflicts":[]}`))
	case r.Method == http.MethodGet && strings.HasSuffix(p, "/sync/pull"):
		_, _ = w.Write([]byte(`{"new_head":"","files":[]}`))
	default:
		_, _ = w.Write([]byte(`{}`))
	}
}

// Hits returns a copy of the requests A received so far.
func (tr *wp03Trap) Hits() []wp03Hit {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]wp03Hit(nil), tr.hits...)
}

// Reset forgets A's recorded hits, so one test can check several tools.
func (tr *wp03Trap) Reset() {
	tr.mu.Lock()
	tr.hits = nil
	tr.mu.Unlock()
}

// AssertOnlyA asserts A received at least one request carrying
// 'Bearer <token>' and reports whether it did. B fails the test by itself
// on any request.
func (tr *wp03Trap) AssertOnlyA(t *testing.T, label, token string) bool {
	t.Helper()
	for _, h := range tr.Hits() {
		if h.Auth == "Bearer "+token {
			return true
		}
	}
	t.Errorf("%s: server A received no request with 'Bearer %s'; hits: %+v", label, token, tr.Hits())
	return false
}

// wp03SplitCreds writes the workspace config {configURL, repo "test-repo"}
// and GLOBAL credentials {wp03Token, credsURL} under the isolated HOME. An
// empty credsURL leaves the credentials without a server_url.
func wp03SplitCreds(t *testing.T, root, configURL, credsURL string) {
	t.Helper()
	if err := config.Save(root, &config.Config{Version: 1, ServerURL: configURL, RepoID: "test-repo"}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := config.SaveGlobalCredentials(&config.Credentials{
		Token: wp03Token, ServerURL: credsURL,
		UserName: "wp03", UserEmail: "wp03@example.test",
	}); err != nil {
		t.Fatalf("save global creds: %v", err)
	}
}

// wp03ToolCall is one remote tool invocation.
type wp03ToolCall struct {
	name string
	args map[string]interface{}
}

// wp03RemoteToolCalls lists every tool that talks to the server, with args
// that reach the client call against setupCaptureServer's pages.
func wp03RemoteToolCalls() []wp03ToolCall {
	return []wp03ToolCall{
		{"ctx_push", map[string]interface{}{"no_distill": true}},
		{"ctx_pull", map[string]interface{}{}},
		{"ctx_history", map[string]interface{}{"slug": "foo"}},
		{"ctx_diff", map[string]interface{}{"slug": "foo"}},
		{"ctx_evolution", map[string]interface{}{"slug": "foo"}},
	}
}

// wp03ResultText returns the result's first text, or "" when there is none.
func wp03ResultText(res *ToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	return res.Content[0].Text
}

// wp03WantError asserts res is an error result whose text contains every
// one of wants.
func wp03WantError(t *testing.T, label string, res *ToolResult, wants ...string) {
	t.Helper()
	if res == nil || !res.IsError {
		t.Errorf("%s: want an error result, got %+v", label, res)
		return
	}
	text := wp03ResultText(res)
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("%s: error %q does not contain %q", label, text, w)
		}
	}
}

// setupCaptureServer builds a Server bound to a tmp project that has:
//   - .contexo/ with a concept page and an entity page
//   - credentials + config pointing at the optional httptest server
//   - optionally a pending capture buffer
//
// When pushServer is nil, no creds/server are wired — useful for tests
// that only assert the early-return branches (PUSH_PAUSED or errors).
// HOME is isolated, so the developer's global credentials are never read.
func setupCaptureServer(t *testing.T, withBuffer bool, pushServer *httptest.Server) (*Server, string) {
	t.Helper()
	wp03IsolateHome(t)
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

// TestWritePageToolDescribesEachType ensures the ctx_write_page tool tells
// the agent when to pick each page type. Without this guidance agents
// default to 'concept' and never author entity pages (observed in the
// chompchat repo: 3 concept pages, 0 entity pages).
func TestWritePageToolDescribesEachType(t *testing.T) {
	srv, _ := setupCaptureServer(t, false, nil)
	var desc string
	for _, tool := range srv.ListTools() {
		if tool.Name == "ctx_write_page" {
			desc = tool.Description
		}
	}
	if desc == "" {
		t.Fatal("ctx_write_page tool not found in ListTools()")
	}
	for _, typ := range []string{"concept", "entity", "source", "analysis"} {
		if !strings.Contains(desc, typ) {
			t.Errorf("ctx_write_page description should explain when to use type %q; got:\n%s", typ, desc)
		}
	}
}

// TestPushHandshakeNudgesEntityCoverage ensures the PUSH_PAUSED directive
// asks the agent to fill in missing entity pages before the final push,
// so named systems/libraries/services get their own pages instead of
// only being mentioned inside concept pages.
func TestPushHandshakeNudgesEntityCoverage(t *testing.T) {
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
	if !strings.Contains(text, "wiki/entities/") {
		t.Errorf("handshake should point at wiki/entities/: %s", text)
	}
	if !strings.Contains(text, `type="entity"`) {
		t.Errorf(`handshake should mention ctx_write_page(type="entity"): %s`, text)
	}
}

// TestMCPToolsUseResolvedServer: GLOBAL credentials belong to A while the
// workspace config names B. ctx_status reports A and warns about B, and
// every remote tool sends its request, with the bearer, only to A.
func TestMCPToolsUseResolvedServer(t *testing.T) {
	tr := wp03TrapServers(t)
	srv, root := setupCaptureServer(t, false, nil)
	wp03SplitCreds(t, root, tr.B.URL, tr.A.URL)

	res := srv.HandleToolCall(context.Background(), "ctx_status", map[string]interface{}{})
	if res == nil || res.IsError {
		t.Fatalf("ctx_status: want ok, got %+v", res)
	}
	status := wp03ResultText(res)
	for _, want := range []string{
		"Server: " + tr.A.URL + "\n",
		"Warning: config server_url " + tr.B.URL + " differs from your credentials' server; using " + tr.A.URL,
		"Repo: test-repo",
		"User: wp03 <wp03@example.test>",
		"Authenticated: true",
	} {
		if !strings.Contains(status, want) {
			t.Errorf("ctx_status missing %q:\n%s", want, status)
		}
	}

	for _, call := range wp03RemoteToolCalls() {
		tr.Reset()
		res := srv.HandleToolCall(context.Background(), call.name, call.args)
		if !tr.AssertOnlyA(t, call.name, wp03Token) {
			t.Logf("%s result: %+v", call.name, res)
		}
	}
}

// TestMCPToolsReportRemoteErrorAdvice: when the remote is unusable every
// remote tool fails before sending anything, with the RemoteError message
// and the command that fixes it.
func TestMCPToolsReportRemoteErrorAdvice(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, root string, tr *wp03Trap)
		wants []string
	}{
		{
			name:  "no credentials",
			setup: func(t *testing.T, root string, tr *wp03Trap) {},
			wants: []string{"not authenticated", "(run 'ctx auth login')"},
		},
		{
			name: "empty-token project credentials shadow valid global ones",
			setup: func(t *testing.T, root string, tr *wp03Trap) {
				wp03SplitCreds(t, root, tr.B.URL, tr.A.URL)
				if err := config.SaveCredentials(root, &config.Credentials{ServerURL: tr.A.URL}); err != nil {
					t.Fatalf("save project creds: %v", err)
				}
			},
			wants: []string{"not authenticated", "(run 'ctx auth login')"},
		},
		{
			name: "global credentials without server_url",
			setup: func(t *testing.T, root string, tr *wp03Trap) {
				wp03SplitCreds(t, root, tr.B.URL, "")
			},
			wants: []string{"server URL unresolved", "(run 'ctx auth login --server <url>')"},
		},
		{
			name: "credentials with server_url but no repo",
			setup: func(t *testing.T, root string, tr *wp03Trap) {
				if err := config.SaveGlobalCredentials(&config.Credentials{Token: wp03Token, ServerURL: tr.A.URL}); err != nil {
					t.Fatalf("save global creds: %v", err)
				}
			},
			wants: []string{"repo not configured", "(run 'ctx remote set-repo <id>')"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := wp03TrapServers(t)
			srv, root := setupCaptureServer(t, false, nil)
			c.setup(t, root, tr)
			for _, call := range wp03RemoteToolCalls() {
				res := srv.HandleToolCall(context.Background(), call.name, call.args)
				wp03WantError(t, call.name, res, append([]string{call.name + ": "}, c.wants...)...)
			}
			if hits := tr.Hits(); len(hits) != 0 {
				t.Errorf("nothing may be sent while the remote is unusable; A received %+v", hits)
			}
		})
	}
}

// TestMCPCorruptCredentialsIsCleanError: an unparsable credentials file is a
// clean tool error naming the tool, never a nil-pointer panic.
func TestMCPCorruptCredentialsIsCleanError(t *testing.T) {
	tr := wp03TrapServers(t)
	srv, root := setupCaptureServer(t, false, nil)
	wp03SplitCreds(t, root, tr.B.URL, tr.A.URL)
	if err := os.WriteFile(config.CredentialsPath(root), []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt creds: %v", err)
	}

	res := srv.HandleToolCall(context.Background(), "ctx_status", map[string]interface{}{})
	wp03WantError(t, "ctx_status", res, "ctx_status: credentials: parse")
	for _, call := range wp03RemoteToolCalls() {
		res := srv.HandleToolCall(context.Background(), call.name, call.args)
		wp03WantError(t, call.name, res, call.name+": credentials: parse")
	}
	if hits := tr.Hits(); len(hits) != 0 {
		t.Errorf("nothing may be sent with corrupt credentials; A received %+v", hits)
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
