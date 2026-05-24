package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/diff"
	"github.com/sugihAF/contexo/internal/server/gitstore"
)

// minimalRig wires only the routes needed by the diff/history tests. It uses
// legacy auth (bearer = "legacy-key") so we don't have to create users.
func minimalRig(t *testing.T) (*gitstore.Store, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	store, err := gitstore.Open(filepath.Join(dir, "repos"))
	if err != nil {
		t.Fatalf("gitstore: %v", err)
	}
	h := New(store, nil, nil, nil)
	resolver := auth.NewResolver(nil, nil, "legacy-key")
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(auth.GinMiddleware(resolver.Validator()))
	v1.GET("/repos/:id/diff/*path", h.Diff)
	v1.GET("/repos/:id/history/*path", h.History)
	return store, r
}

func get(t *testing.T, r http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Authorization", "Bearer legacy-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func seedPage(t *testing.T, s *gitstore.Store, repo, path, body string, parent string) string {
	t.Helper()
	if !s.Exists(repo) {
		if err := s.Init(repo); err != nil {
			t.Fatalf("init repo: %v", err)
		}
	}
	sha, conflict, err := s.Write(repo, path, []byte(body), "Tester", "t@e", "msg", parent)
	if err != nil || conflict != nil {
		t.Fatalf("seed write: err=%v conflict=%+v", err, conflict)
	}
	return sha
}

func TestDiff_ExplicitFromTo(t *testing.T) {
	store, r := minimalRig(t)
	page1 := "---\nslug: x\ntype: concept\n---\n## Decision\nold\n"
	page2 := "---\nslug: x\ntype: concept\nreasoning_summary: now-has-summary\n---\n## Decision\nnew\n"
	sha1 := seedPage(t, store, "repo", "wiki/concepts/x.md", page1, "")
	sha2 := seedPage(t, store, "repo", "wiki/concepts/x.md", page2, sha1)

	w := get(t, r, "/v1/repos/repo/diff/wiki/concepts/x.md?from="+sha1+"&to="+sha2)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var d diff.SectionDiff
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.FromSHA != sha1 || d.ToSHA != sha2 {
		t.Errorf("sha mismatch: %+v", d)
	}
	if len(d.Frontmatter.Added) != 1 || d.Frontmatter.Added[0].Field != "reasoning_summary" {
		t.Errorf("expected reasoning_summary added, got %+v", d.Frontmatter)
	}
	if len(d.Sections) != 1 || d.Sections[0].Status != diff.StatusModified {
		t.Errorf("expected 1 modified section, got %+v", d.Sections)
	}
}

func TestDiff_DefaultsToParentHead(t *testing.T) {
	store, r := minimalRig(t)
	page1 := "---\nslug: x\ntype: concept\n---\nv1\n"
	page2 := "---\nslug: x\ntype: concept\n---\nv2\n"
	seedPage(t, store, "repo", "wiki/concepts/x.md", page1, "")
	sha2 := seedPage(t, store, "repo", "wiki/concepts/x.md", page2, "")

	// No ?from/?to → most recent change
	w := get(t, r, "/v1/repos/repo/diff/wiki/concepts/x.md")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var d diff.SectionDiff
	_ = json.Unmarshal(w.Body.Bytes(), &d)
	if d.ToSHA != sha2 {
		t.Errorf("to should default to head: got %q want %q", d.ToSHA, sha2)
	}
	if d.FromSHA == "" {
		t.Errorf("from should default to parent of to; got empty")
	}
	if d.Preamble == nil || d.Preamble.Status != diff.StatusModified {
		t.Errorf("expected modified preamble, got %+v", d.Preamble)
	}
}

func TestDiff_PathMissing(t *testing.T) {
	store, r := minimalRig(t)
	if err := store.Init("repo"); err != nil {
		t.Fatalf("init: %v", err)
	}
	w := get(t, r, "/v1/repos/repo/diff/wiki/concepts/nope.md")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDiff_UnknownSha(t *testing.T) {
	store, r := minimalRig(t)
	page := "---\nslug: x\ntype: concept\n---\nbody\n"
	seedPage(t, store, "repo", "wiki/concepts/x.md", page, "")
	w := get(t, r, "/v1/repos/repo/diff/wiki/concepts/x.md?from=deadbeef0123456789abcdef0123456789abcdef&to=deadbeef0123456789abcdef0123456789abcde0")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDiff_NoParentForFirstCommit(t *testing.T) {
	store, r := minimalRig(t)
	page := "---\nslug: x\ntype: concept\n---\nonly version\n"
	sha := seedPage(t, store, "repo", "wiki/concepts/x.md", page, "")
	w := get(t, r, "/v1/repos/repo/diff/wiki/concepts/x.md?to="+sha)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 (no parent), got %d: %s", w.Code, w.Body.String())
	}
}

func TestHistory_ReturnsCommits(t *testing.T) {
	store, r := minimalRig(t)
	page := "---\nslug: x\ntype: concept\n---\nbody\n"
	sha1 := seedPage(t, store, "repo", "wiki/concepts/x.md", page, "")
	seedPage(t, store, "repo", "wiki/concepts/x.md", page+"\nedit\n", sha1)

	w := get(t, r, "/v1/repos/repo/history/wiki/concepts/x.md")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Commits []gitstore.CommitMeta `json:"commits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Commits) != 2 {
		t.Errorf("expected 2 commits, got %d", len(resp.Commits))
	}
}
