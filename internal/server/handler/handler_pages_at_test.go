package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/auth"
)

// wp02AtFixture is a legacy-key rig with the sync and page routes mounted and
// repo "at-repo" holding wiki/a.md@c1 ("alpha v1"), wiki/b.md@c2 ("beta") and
// a later edit of wiki/a.md@c3 ("alpha v2").
type wp02AtFixture struct {
	rig        *testRig
	c1, c2, c3 string
}

func wp02AtRig(t *testing.T) wp02AtFixture {
	t.Helper()
	rig := setupRig(t)
	wp02WireSync(rig)
	if err := rig.store.Init("at-repo"); err != nil {
		t.Fatalf("init: %v", err)
	}
	write := func(path, body, parent string) string {
		t.Helper()
		sha, conflict, err := rig.store.Write("at-repo", path, []byte(body), "Tester", "t@example.com", "msg", parent)
		if err != nil || conflict != nil {
			t.Fatalf("write %s: err=%v conflict=%+v", path, err, conflict)
		}
		return sha
	}
	f := wp02AtFixture{rig: rig}
	f.c1 = write("wiki/a.md", "alpha v1", "")
	f.c2 = write("wiki/b.md", "beta", "")
	f.c3 = write("wiki/a.md", "alpha v2", f.c1)
	return f
}

// wp02AtURL is the page route for repo/page with ?at=<at> (always present,
// even when at is empty).
func wp02AtURL(repo, page, at string) string {
	return "/v1/repos/" + repo + "/pages/" + page + "?" + url.Values{"at": {at}}.Encode()
}

func wp02Get(t *testing.T, rig *testRig, path string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, rig.router, http.MethodGet, path, "legacy-key", nil)
}

// wp02WantError asserts a JSON error body {"error","code"} with the given
// status and code, and returns the error text.
func wp02WantError(t *testing.T, label string, w *httptest.ResponseRecorder, status int, code string) string {
	t.Helper()
	if w.Code != status {
		t.Errorf("%s: status %d body %s, want %d", label, w.Code, w.Body.String(), status)
		return ""
	}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Errorf("%s: body %q is not JSON: %v", label, w.Body.String(), err)
		return ""
	}
	if body.Code != code || body.Error == "" {
		t.Errorf("%s: body %s, want an error with code %q", label, w.Body.String(), code)
	}
	if w.Header().Get("X-Contexo-At") != "" {
		t.Errorf("%s: an error response carries X-Contexo-At %q", label, w.Header().Get("X-Contexo-At"))
	}
	return body.Error
}

func TestReadPageAtSha(t *testing.T) {
	f := wp02AtRig(t)
	cases := []struct{ at, want, wantSHA string }{
		{f.c1, "alpha v1", f.c1},
		// A later commit that did not touch a.md still reports a's own commit.
		{f.c2, "alpha v1", f.c1},
		{f.c2[:7], "alpha v1", f.c1},
		{strings.ToUpper(f.c2[:7]), "alpha v1", f.c1},
		{f.c3, "alpha v2", f.c3},
	}
	for _, tc := range cases {
		w := wp02Get(t, f.rig, wp02AtURL("at-repo", "wiki/a.md", tc.at))
		if w.Code != http.StatusOK {
			t.Errorf("at %s: status %d body %s, want 200", tc.at, w.Code, w.Body.String())
			continue
		}
		if got := w.Body.String(); got != tc.want {
			t.Errorf("at %s: body %q, want %q", tc.at, got, tc.want)
		}
		if got := w.Header().Get("X-Contexo-At"); got != tc.at {
			t.Errorf("at %s: X-Contexo-At %q, want the requested string verbatim", tc.at, got)
		}
		if got := w.Header().Get("X-Page-SHA"); got != tc.wantSHA || len(got) != 40 {
			t.Errorf("at %s: X-Page-SHA %q, want the full path commit %s", tc.at, got, tc.wantSHA)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
			t.Errorf("at %s: Content-Type %q, want text/markdown", tc.at, ct)
		}
	}
}

func TestReadPageAtUnknownSha(t *testing.T) {
	f := wp02AtRig(t)
	blob, err := exec.Command("git", "-C", filepath.Join(f.rig.store.Root, "at-repo"), "rev-parse", f.c1+":wiki/a.md").Output()
	if err != nil {
		t.Fatalf("blob id: %v", err)
	}
	for _, at := range []string{
		strings.Repeat("deadbeef", 5),
		strings.Repeat("ab", 32),        // 64 hex: a SHA-256-length id
		strings.TrimSpace(string(blob)), // an object that is not a commit
	} {
		w := wp02Get(t, f.rig, wp02AtURL("at-repo", "wiki/a.md", at))
		wp02WantError(t, "at "+at, w, http.StatusNotFound, "sha_not_found")
	}
}

func TestReadPageAtInvalidSha(t *testing.T) {
	f := wp02AtRig(t)
	for _, at := range []string{"zz", "--all", "-G.", "", strings.Repeat("a", 65), "HEAD", "main", "abc", f.c1 + "^", f.c1[:7] + "~1"} {
		w := wp02Get(t, f.rig, wp02AtURL("at-repo", "wiki/a.md", at))
		if msg := wp02WantError(t, "at "+at, w, http.StatusBadRequest, "invalid_sha"); msg != "" && msg != "invalid sha" {
			t.Errorf("at %q: error %q, want \"invalid sha\"", at, msg)
		}
	}
	// The sha is validated before the repo is touched.
	w := wp02Get(t, f.rig, wp02AtURL("ghost-repo", "wiki/a.md", "--all"))
	wp02WantError(t, "missing repo, flag sha", w, http.StatusBadRequest, "invalid_sha")
}

func TestReadPageAtPathAbsent(t *testing.T) {
	f := wp02AtRig(t)
	cases := []struct{ page, at string }{
		{"wiki/b.md", f.c1}, // written after c1
		{"wiki/none.md", f.c3},
		{"wiki/none.md", f.c3[:7]},
	}
	for _, tc := range cases {
		w := wp02Get(t, f.rig, wp02AtURL("at-repo", tc.page, tc.at))
		wp02WantError(t, tc.page+"@"+tc.at, w, http.StatusNotFound, "sha_not_found")
	}
}

func TestReadPageAtRepoMissing(t *testing.T) {
	f := wp02AtRig(t)
	w := wp02Get(t, f.rig, wp02AtURL("ghost-repo", "wiki/a.md", f.c1))
	if msg := wp02WantError(t, "?at on a missing repo", w, http.StatusNotFound, "repo_not_found"); msg != "" && msg != "repo not found" {
		t.Errorf("error %q, want \"repo not found\"", msg)
	}
}

func TestReadPageWithoutAtUnchanged(t *testing.T) {
	f := wp02AtRig(t)
	w := wp02Get(t, f.rig, "/v1/repos/at-repo/pages/wiki/a.md")
	if w.Code != http.StatusOK || w.Body.String() != "alpha v2" {
		t.Fatalf("HEAD read: %d %q, want 200 alpha v2", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Page-SHA"); got != f.c3 {
		t.Errorf("X-Page-SHA %q, want %s", got, f.c3)
	}
	if _, ok := w.Header()["X-Contexo-At"]; ok {
		t.Errorf("a read without ?at carries X-Contexo-At %q", w.Header().Get("X-Contexo-At"))
	}

	w = wp02Get(t, f.rig, "/v1/repos/at-repo/pages/wiki/none.md")
	if msg := wp02WantError(t, "missing page", w, http.StatusNotFound, "page_not_found"); msg != "" && msg != "page not found" {
		t.Errorf("missing page error %q, want \"page not found\" (old clients match it)", msg)
	}
	w = wp02Get(t, f.rig, "/v1/repos/ghost-repo/pages/wiki/a.md")
	if msg := wp02WantError(t, "missing repo", w, http.StatusNotFound, "repo_not_found"); msg != "" && msg != "repo not found" {
		t.Errorf("missing repo error %q, want \"repo not found\" (old clients match it)", msg)
	}
}

func TestReadPageUnsafePathIs400(t *testing.T) {
	f := wp02AtRig(t)
	for _, page := range []string{"wiki/-x.md", "-x.md", ".git/config", "wiki/.git/HEAD"} {
		w := wp02Get(t, f.rig, "/v1/repos/at-repo/pages/"+page)
		wp02WantError(t, page+" without ?at", w, http.StatusBadRequest, "invalid_path")
		w = wp02Get(t, f.rig, wp02AtURL("at-repo", page, f.c1))
		wp02WantError(t, page+" with ?at", w, http.StatusBadRequest, "invalid_path")
	}
	// A missing page path is an invalid path too, with or without ?at.
	w := wp02Get(t, f.rig, "/v1/repos/at-repo/pages/")
	wp02WantError(t, "empty path without ?at", w, http.StatusBadRequest, "invalid_path")
	w = wp02Get(t, f.rig, "/v1/repos/at-repo/pages/?at="+f.c1)
	wp02WantError(t, "empty path with ?at", w, http.StatusBadRequest, "invalid_path")
}

// TestHistoryUnsafePathIs400: History answers an unsafe page path with 400
// invalid_path (it was a 500, LogPath's ErrUnsafePath was unmapped) and a
// missing path with 400 invalid_path (it had no code), like the page routes.
func TestHistoryUnsafePathIs400(t *testing.T) {
	f := wp02AtRig(t)
	v1 := f.rig.router.Group("/v1")
	v1.Use(auth.GinMiddleware(f.rig.resolver.Validator()))
	v1.GET("/repos/:id/history/*path", f.rig.h.History)

	for _, page := range []string{"wiki/-x.md", "-x.md", ".git/config", "wiki/.git/HEAD", "wiki/a%01.md"} {
		w := wp02Get(t, f.rig, "/v1/repos/at-repo/history/"+page)
		if msg := wp02WantError(t, "history of "+page, w, http.StatusBadRequest, "invalid_path"); msg != "" && msg != "invalid page path" {
			t.Errorf("history of %s: error %q, want \"invalid page path\"", page, msg)
		}
	}
	w := wp02Get(t, f.rig, "/v1/repos/at-repo/history/")
	if msg := wp02WantError(t, "history of an empty path", w, http.StatusBadRequest, "invalid_path"); msg != "" && msg != "missing path" {
		t.Errorf("empty path: error %q, want \"missing path\" (old clients match it)", msg)
	}

	// A safe path still lists its own commits.
	w = wp02Get(t, f.rig, "/v1/repos/at-repo/history/wiki/a.md")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), f.c3) || !strings.Contains(w.Body.String(), f.c1) || strings.Contains(w.Body.String(), f.c2) {
		t.Errorf("history of wiki/a.md: %d %s, want 200 with c3 and c1 only", w.Code, w.Body.String())
	}
}

// TestReadPageDirectoryIs404: "wiki" is a tree, not a page. It used to come
// back as a 200 with git's tree listing (?at) or an empty body (HEAD).
func TestReadPageDirectoryIs404(t *testing.T) {
	f := wp02AtRig(t)
	w := wp02Get(t, f.rig, "/v1/repos/at-repo/pages/wiki")
	wp02WantError(t, "directory at HEAD", w, http.StatusNotFound, "page_not_found")
	w = wp02Get(t, f.rig, wp02AtURL("at-repo", "wiki", f.c1))
	wp02WantError(t, "directory at c1", w, http.StatusNotFound, "sha_not_found")
}

// TestReadPageDeletedIs404: a page removed from the repo (out of band, e.g. by
// a self-hoster) is 404 at HEAD and at the deleting commit, not a 200 with an
// empty body.
func TestReadPageDeletedIs404(t *testing.T) {
	f := wp02AtRig(t)
	dir := filepath.Join(f.rig.store.Root, "at-repo")
	for _, args := range [][]string{{"rm", "-q", "--", "wiki/a.md"}, {"commit", "-q", "-m", "delete a"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	del, err := f.rig.store.HeadSHA("at-repo")
	if err != nil {
		t.Fatalf("head: %v", err)
	}

	w := wp02Get(t, f.rig, "/v1/repos/at-repo/pages/wiki/a.md")
	wp02WantError(t, "deleted at HEAD", w, http.StatusNotFound, "page_not_found")
	w = wp02Get(t, f.rig, wp02AtURL("at-repo", "wiki/a.md", del))
	wp02WantError(t, "deleted at the deleting commit", w, http.StatusNotFound, "sha_not_found")
	if w = wp02Get(t, f.rig, wp02AtURL("at-repo", "wiki/a.md", f.c3)); w.Code != http.StatusOK || w.Body.String() != "alpha v2" {
		t.Errorf("before the deletion: %d %q, want 200 alpha v2", w.Code, w.Body.String())
	}
}

// TestReadPageEmptyPageIs200: an empty page is present, with and without ?at.
func TestReadPageEmptyPageIs200(t *testing.T) {
	f := wp02AtRig(t)
	sha, _, err := f.rig.store.Write("at-repo", "wiki/empty.md", nil, "Tester", "t@example.com", "msg", "")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, u := range []string{"/v1/repos/at-repo/pages/wiki/empty.md", wp02AtURL("at-repo", "wiki/empty.md", sha)} {
		w := wp02Get(t, f.rig, u)
		if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("X-Page-SHA") != sha {
			t.Errorf("%s: %d %q X-Page-SHA %q, want 200, empty body, %s", u, w.Code, w.Body.String(), w.Header().Get("X-Page-SHA"), sha)
		}
	}
}

func TestListPagesEmptyRepoReturnsArray(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	if err := rig.store.Init("empty-repo"); err != nil {
		t.Fatalf("init: %v", err)
	}
	w := wp02Get(t, rig, "/v1/repos/empty-repo/pages")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"pages":[]}` {
		t.Errorf("empty repo listing: %d %s, want 200 {\"pages\":[]}", w.Code, w.Body.String())
	}
}

func TestListPagesRepoMissingHasCode(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	w := wp02Get(t, rig, "/v1/repos/ghost-repo/pages")
	if msg := wp02WantError(t, "listing a missing repo", w, http.StatusNotFound, "repo_not_found"); msg != "" && msg != "repo not found" {
		t.Errorf("error %q, want \"repo not found\"", msg)
	}
}

// TestRepoNotFoundBodiesHaveCode: Pull, Timeline and History keep their
// "repo not found" text and gain code repo_not_found.
func TestRepoNotFoundBodiesHaveCode(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	v1 := rig.router.Group("/v1")
	v1.Use(auth.GinMiddleware(rig.resolver.Validator()))
	v1.GET("/repos/:id/timeline", rig.h.Timeline)
	v1.GET("/repos/:id/history/*path", rig.h.History)

	for _, u := range []string{
		"/v1/repos/ghost-repo/sync/pull",
		"/v1/repos/ghost-repo/timeline",
		"/v1/repos/ghost-repo/history/wiki/a.md",
	} {
		w := wp02Get(t, rig, u)
		if msg := wp02WantError(t, u, w, http.StatusNotFound, "repo_not_found"); msg != "" && msg != "repo not found" {
			t.Errorf("%s: error %q, want \"repo not found\"", u, msg)
		}
	}
}
