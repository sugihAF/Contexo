package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/diff"
	syncpkg "github.com/sugihAF/contexo/internal/sync"
)

func seedSyncState(t *testing.T, hubRoot, relPath, sha string) {
	t.Helper()
	state, err := syncpkg.LoadState(hubRoot)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.PageSHAs == nil {
		state.PageSHAs = map[string]string{}
	}
	state.PageSHAs[relPath] = sha
	if err := syncpkg.SaveState(hubRoot, state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	// The drift cache lives on the Server struct — calling code that
	// previously read this path won't pick up the new state until either the
	// TTL expires or a fresh Server is constructed. Tests construct fresh
	// servers per case, so this is fine.
}

func TestDrift_NoticeRendersWhenServerAhead(t *testing.T) {
	// HTTP server: ReadPage returns a "server has moved" sha; PageDiff returns
	// a small diff. The MCP read path should prepend a DRIFT_NOTICE block to
	// the page bytes the agent receives.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/repos/test-repo/pages/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Page-SHA", "def567812345abcdef")
		_, _ = w.Write([]byte("---\nslug: foo\ntype: concept\n---\n## Decision\nupdated body\n"))
	})
	mux.HandleFunc("/v1/repos/test-repo/diff/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"from_sha":"abc1234",
			"to_sha":"def5678",
			"sections":[
				{"heading":"## Decision","status":"modified"},
				{"heading":"## Refund handling","status":"added"}
			]
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mcpServer, _ := setupCaptureServer(t, false, srv)

	// Seed the sync state so the page has a known "local sha" baseline that
	// the drift check will compare against. setupCaptureServer wrote a
	// concept page for slug "foo" — its RelPath is wiki/concepts/foo.md.
	seedSyncState(t, mcpServer.store.Root, "wiki/concepts/foo.md", "abc1234")

	data, mime, err := mcpServer.HandleResourceRead(context.Background(), "ctx://wiki/foo")
	if err != nil {
		t.Fatalf("HandleResourceRead: %v", err)
	}
	if mime != "text/markdown" {
		t.Errorf("mime: %q", mime)
	}
	s := string(data)
	if !strings.HasPrefix(s, "<DRIFT_NOTICE>") {
		t.Fatalf("expected DRIFT_NOTICE prefix, got:\n%s", s[:min(200, len(s))])
	}
	if !strings.Contains(s, "abc1234") {
		t.Errorf("notice should reference local sha")
	}
	if !strings.Contains(s, "def5678") {
		t.Errorf("notice should reference server sha")
	}
	if !strings.Contains(s, "## Refund handling") {
		t.Errorf("notice should include the changed sections")
	}
	// Page body still present after the notice.
	if !strings.Contains(s, "## Decision") {
		t.Errorf("original page body missing from result")
	}
}

func TestDrift_NoNoticeWhenInSync(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/repos/test-repo/pages/", func(w http.ResponseWriter, r *http.Request) {
		// Server returns the same sha the local state believes is current.
		w.Header().Set("X-Page-SHA", "samesha12345abcdef")
		_, _ = w.Write([]byte("---\nslug: foo\ntype: concept\n---\nbody\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mcpServer, _ := setupCaptureServer(t, false, srv)
	seedSyncState(t, mcpServer.store.Root, "wiki/concepts/foo.md", "samesha12345abcdef")

	data, _, err := mcpServer.HandleResourceRead(context.Background(), "ctx://wiki/foo")
	if err != nil {
		t.Fatalf("HandleResourceRead: %v", err)
	}
	if strings.Contains(string(data), "DRIFT_NOTICE") {
		t.Errorf("should not emit DRIFT_NOTICE when in sync; got:\n%s", string(data))
	}
}

func TestDrift_SilentWhenNoCredsConfigured(t *testing.T) {
	// No HTTP server, no creds. Drift check should silently no-op.
	mcpServer, _ := setupCaptureServer(t, false, nil)
	data, _, err := mcpServer.HandleResourceRead(context.Background(), "ctx://wiki/foo")
	if err != nil {
		t.Fatalf("HandleResourceRead: %v", err)
	}
	if strings.Contains(string(data), "DRIFT_NOTICE") {
		t.Errorf("should not emit DRIFT_NOTICE when unconfigured")
	}
}

func TestDrift_NoNoticeForNeverPulledPage(t *testing.T) {
	// HTTP returns 404 to simulate a never-pushed page. Drift check should
	// not invent a notice; the page is locally-authored, not drifted.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	mcpServer, _ := setupCaptureServer(t, false, srv)
	// Don't seed any sync state — this page has never been pulled.
	data, _, err := mcpServer.HandleResourceRead(context.Background(), "ctx://wiki/foo")
	if err != nil {
		t.Fatalf("HandleResourceRead: %v", err)
	}
	if strings.Contains(string(data), "DRIFT_NOTICE") {
		t.Errorf("should not emit DRIFT_NOTICE for never-pulled pages")
	}
}

func TestDrift_CachedWithinTTL(t *testing.T) {
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/repos/test-repo/pages/", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-Page-SHA", "samesha")
		_, _ = w.Write([]byte("---\nslug: foo\ntype: concept\n---\nbody\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mcpServer, _ := setupCaptureServer(t, false, srv)
	seedSyncState(t, mcpServer.store.Root, "wiki/concepts/foo.md", "samesha")

	// Read 3 times in quick succession; the drift cache should fold these into
	// a single HTTP call (within the TTL).
	for i := 0; i < 3; i++ {
		if _, _, err := mcpServer.HandleResourceRead(context.Background(), "ctx://wiki/foo"); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 cached HTTP call, got %d", calls)
	}
}

func TestRenderDriftNotice_CompactSummary(t *testing.T) {
	d := &diff.SectionDiff{
		Sections: []diff.SectionChange{
			{Heading: "## Decision", Status: diff.StatusModified},
			{Heading: "## Refund handling", Status: diff.StatusAdded},
		},
	}
	notice := renderDriftNotice("wiki/concepts/foo.md", "abc1234", "def5678", d)
	for _, want := range []string{
		"<DRIFT_NOTICE>",
		"abc1234",
		"def5678",
		"~ ## Decision",
		"+ ## Refund handling",
		"ctx pull",
		"</DRIFT_NOTICE>",
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice missing %q. Full:\n%s", want, notice)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
