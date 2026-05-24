package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/diff"
	"github.com/sugihAF/contexo/internal/server/gitstore"
)

// minimalRig wires only the routes needed by the diff/history/evolution
// tests. It uses legacy auth (bearer = "legacy-key") so we don't have to
// create users.
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
	v1.GET("/repos/:id/evolution/*path", h.Evolution)
	return store, r
}

// minimalRigWithEvolution is kept as a named alias for tests that want to be
// explicit about needing the evolution route; functionally identical to
// minimalRig now that evolution is on the default rig.
func minimalRigWithEvolution(t *testing.T) (*gitstore.Store, *gin.Engine) {
	return minimalRig(t)
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

func TestEvolution_HappyPath(t *testing.T) {
	store, r := minimalRigWithEvolution(t)
	page1 := "---\nslug: x\ntype: concept\n---\nv1 body\n"
	page2 := "---\nslug: x\ntype: concept\n---\n## Decision\nadded\n"
	page3 := "---\nslug: x\ntype: concept\nreasoning_summary: rs\n---\n## Decision\nchanged\n"
	sha1 := seedPage(t, store, "repo", "wiki/concepts/x.md", page1, "")
	sha2 := seedPage(t, store, "repo", "wiki/concepts/x.md", page2, sha1)
	seedPage(t, store, "repo", "wiki/concepts/x.md", page3, sha2)

	w := get(t, r, "/v1/repos/repo/evolution/wiki/concepts/x.md")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Path    string `json:"path"`
		Entries []struct {
			Commit struct {
				SHA     string `json:"sha"`
				Message string `json:"message"`
			} `json:"commit"`
			Diff map[string]any `json:"diff"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(resp.Entries))
	}
	// Newest first, each entry has a non-nil diff
	for i, e := range resp.Entries {
		if e.Commit.SHA == "" {
			t.Errorf("entry[%d] missing sha", i)
		}
		if e.Diff == nil {
			t.Errorf("entry[%d] missing diff", i)
		}
	}
}

func TestEvolution_PathMissing(t *testing.T) {
	_, r := minimalRigWithEvolution(t)
	w := get(t, r, "/v1/repos/empty/evolution/missing.md")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDiff_BlameAnnotatesSections(t *testing.T) {
	store, r := minimalRig(t)
	if err := store.Init("repo-blame"); err != nil {
		t.Fatalf("init repo: %v", err)
	}

	// Commit 1: "Alice" introduces ## Decision
	page1 := "---\nslug: x\ntype: concept\n---\n## Decision\nWe chose Stripe Billing.\n"
	sha1, _, err := store.Write("repo-blame", "wiki/concepts/x.md",
		[]byte(page1), "Alice", "alice@example.com", "initial decision", "")
	if err != nil || sha1 == "" {
		t.Fatalf("seed v1: err=%v sha=%q", err, sha1)
	}

	// Commit 2: "Bob" adds ## Refund handling
	page2 := page1 + "\n## Refund handling\nIssue refunds via Stripe.\n"
	sha2, _, err := store.Write("repo-blame", "wiki/concepts/x.md",
		[]byte(page2), "Bob", "bob@example.com", "add refunds", sha1)
	if err != nil {
		t.Fatalf("seed v2: %v", err)
	}

	// Commit 3: "Alice" modifies ## Decision
	page3 := strings.Replace(page2, "We chose Stripe Billing.", "We chose Stripe Billing + metered.", 1)
	_, _, err = store.Write("repo-blame", "wiki/concepts/x.md",
		[]byte(page3), "Alice", "alice@example.com", "expand decision", sha2)
	if err != nil {
		t.Fatalf("seed v3: %v", err)
	}

	// Diff parent..head (the v2→v3 hop, which modified Decision). Blame should
	// attribute Decision to Alice (commit 1, who introduced the heading) and
	// Refund handling to Bob (commit 2, who introduced that heading).
	w := get(t, r, "/v1/repos/repo-blame/diff/wiki/concepts/x.md?blame=true")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var d diff.SectionDiff
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, s := range d.Sections {
		switch s.Heading {
		case "## Decision":
			if s.IntroducedBy == nil || s.IntroducedBy.Author != "Alice" {
				t.Errorf("## Decision blame: got %+v, want Alice", s.IntroducedBy)
			}
		case "## Refund handling":
			if s.IntroducedBy == nil || s.IntroducedBy.Author != "Bob" {
				t.Errorf("## Refund handling blame: got %+v, want Bob", s.IntroducedBy)
			}
		}
	}
}

func TestDiff_PageDeletedBetweenShas(t *testing.T) {
	// Seed v1 + v2 of a page, then delete it. /diff from=v1&to=<HEAD-of-repo>
	// (which is the delete commit) should still return a diff, treating the
	// deleted side as empty so the differ emits clean removals.
	store, r := minimalRig(t)
	page := "---\nslug: x\ntype: concept\n---\n## Decision\nbody\n"
	sha1 := seedPage(t, store, "repo", "wiki/concepts/x.md", page, "")

	// Delete the file via a direct git operation (gitstore doesn't expose a
	// delete primitive in this package; shell out via exec.Command for the
	// test setup).
	deleteFileInRepo(t, store, "repo", "wiki/concepts/x.md")
	deleteSHA, _ := store.HeadSHA("repo")

	w := get(t, r, "/v1/repos/repo/diff/wiki/concepts/x.md?from="+sha1+"&to="+deleteSHA)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (graceful diff on deleted target), got %d: %s", w.Code, w.Body.String())
	}
}

func deleteFileInRepo(t *testing.T, s *gitstore.Store, repo, path string) {
	t.Helper()
	repoDir := filepath.Join(s.Root, repo)
	full := filepath.Join(repoDir, filepath.FromSlash(path))
	if err := os.Remove(full); err != nil {
		t.Fatalf("rm %s: %v", full, err)
	}
	mustGitInDir(t, repoDir, "add", "-A")
	mustGitInDir(t, repoDir, "-c", "user.email=t@e", "-c", "user.name=Tester",
		"commit", "-m", "delete "+path)
}

func mustGitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, string(out))
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
