package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setupDiffServer is a slimmer variant of setupCaptureServer that doesn't
// scaffold pages — the differ tests only care that the slug resolves to a
// file on disk, and that the HTTP server returns the expected payload.
func TestToolHistory_Happy(t *testing.T) {
	gotPath := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/repos/test-repo/history/", func(w http.ResponseWriter, r *http.Request) {
		gotPath = strings.TrimPrefix(r.URL.Path, "/v1/repos/test-repo/history/")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"commits":[
			{"sha":"def567812345abcdef","author":"sugihAF","email":"s@e","time":"2026-05-24T00:00:00Z","message":"add fee calc"},
			{"sha":"abc123412345abcdef","author":"sugihAF","email":"s@e","time":"2026-05-14T00:00:00Z","message":"initial"}
		]}`))
	})
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	srv, projectRoot := setupCaptureServer(t, false, httpSrv)
	// setupCaptureServer wrote a concept page for slug "foo" — perfect.
	_ = projectRoot

	res := srv.HandleToolCall(context.Background(), "ctx_history", map[string]interface{}{
		"slug": "foo",
	})
	if res == nil || res.IsError {
		t.Fatalf("expected non-error result, got %+v", res)
	}
	if gotPath != "wiki/concepts/foo.md" {
		t.Errorf("server saw path %q, want wiki/concepts/foo.md", gotPath)
	}
	if !strings.Contains(res.Content[0].Text, "add fee calc") {
		t.Errorf("missing latest commit message in output: %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "def5678") {
		t.Errorf("missing shortened sha in output: %s", res.Content[0].Text)
	}
}

func TestToolHistory_SlugNotFound(t *testing.T) {
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("HTTP must NOT be called when slug doesn't resolve; got %s", r.URL.Path)
	}))
	defer httpSrv.Close()

	srv, _ := setupCaptureServer(t, false, httpSrv)
	res := srv.HandleToolCall(context.Background(), "ctx_history", map[string]interface{}{
		"slug": "nope",
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected error result, got %+v", res)
	}
	if !strings.Contains(res.Content[0].Text, "not found") {
		t.Errorf("expected 'not found' in error, got %s", res.Content[0].Text)
	}
}

func TestToolDiff_Happy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/repos/test-repo/diff/", func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Query().Get("from"), "abc1234"; got != want {
			t.Errorf("from query: got %q want %q", got, want)
		}
		if got, want := r.URL.Query().Get("to"), "def5678"; got != want {
			t.Errorf("to query: got %q want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"from_sha":"abc1234",
			"to_sha":"def5678",
			"frontmatter":{"changed":[{"field":"reasoning_summary","from":"old","to":"new"}]},
			"sections":[{"heading":"## Decision","status":"modified","line_diff":"- a\n+ b"}]
		}`))
	})
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	srv, _ := setupCaptureServer(t, false, httpSrv)

	res := srv.HandleToolCall(context.Background(), "ctx_diff", map[string]interface{}{
		"slug": "foo",
		"from": "abc1234",
		"to":   "def5678",
	})
	if res == nil || res.IsError {
		t.Fatalf("expected non-error result, got %+v", res)
	}
	out := res.Content[0].Text
	if !strings.Contains(out, "abc1234..def5678") {
		t.Errorf("missing sha range header: %s", out)
	}
	// Body is the SectionDiff JSON; confirm it round-trips.
	jsonStart := strings.Index(out, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON payload in output: %s", out)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out[jsonStart:]), &body); err != nil {
		t.Fatalf("JSON parse: %v\nin: %s", err, out[jsonStart:])
	}
	if body["from_sha"] != "abc1234" || body["to_sha"] != "def5678" {
		t.Errorf("unexpected JSON: %+v", body)
	}
}

func TestToolDiff_NoServer(t *testing.T) {
	// No pushServer wired = no creds saved.
	srv, _ := setupCaptureServer(t, false, nil)
	res := srv.HandleToolCall(context.Background(), "ctx_diff", map[string]interface{}{
		"slug": "foo",
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected error result, got %+v", res)
	}
	if !strings.Contains(res.Content[0].Text, "server not configured") {
		t.Errorf("expected 'server not configured' error, got %s", res.Content[0].Text)
	}
}
