package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/sync"
)

// wp02WireSync mounts the sync and page routes on a NEW /v1 group of the rig.
// gin panics on duplicate routes, so never combine it with
// wireSyncAndActivity (or any other helper that registers these paths) in one
// test.
func wp02WireSync(rig *testRig) {
	v1 := rig.router.Group("/v1")
	v1.Use(auth.GinMiddleware(rig.resolver.Validator()))
	v1.POST("/repos/:id/sync/push", rig.h.Push)
	v1.GET("/repos/:id/sync/pull", rig.h.Pull)
	v1.GET("/repos/:id/pages", rig.h.ListPages)
	v1.GET("/repos/:id/pages/*path", rig.h.ReadPage)
	v1.GET("/repos/:id/activity", rig.h.Activity)
}

// wp02Client starts an httptest server over the rig and returns a sync.Client
// authenticated with the legacy key, the way an old or new ctx talks to it.
func wp02Client(t *testing.T, rig *testRig) *sync.Client {
	t.Helper()
	ts := httptest.NewServer(rig.router)
	t.Cleanup(ts.Close)
	return sync.NewClient(ts.URL, "legacy-key")
}

// TestPush_PartialEditNoFalseDrift is the end-to-end regression for the
// gitstore no-op bug. The client behaves like ctx: it records each returned
// sha as the page's next parent_sha. Before the fix an unchanged page came
// back with repo HEAD, so the third push 409'd on a page nobody touched.
func TestPush_PartialEditNoFalseDrift(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	cl := wp02Client(t, rig)

	const repo = "drift-repo"
	content := map[string]string{"wiki/a.md": "alpha v1", "wiki/b.md": "beta v1"}
	parents := map[string]string{}
	push := func(step string) *sync.PushResponse {
		t.Helper()
		req := &sync.PushRequest{Message: step}
		for _, p := range []string{"wiki/a.md", "wiki/b.md"} {
			req.Files = append(req.Files, sync.PushFile{Path: p, Content: content[p], ParentSHA: parents[p]})
		}
		resp, err := cl.PushPages(repo, req)
		if err != nil {
			t.Fatalf("%s: push: %v", step, err)
		}
		if len(resp.Conflicts) != 0 {
			t.Errorf("%s: want zero conflicts, got %+v", step, resp.Conflicts)
		}
		for _, f := range resp.Pushed {
			parents[f.Path] = f.SHA
		}
		return resp
	}

	push("initial push")
	aCommit, err := rig.store.HeadSHAForPath(repo, "wiki/a.md")
	if err != nil {
		t.Fatalf("a commit: %v", err)
	}

	content["wiki/b.md"] = "beta v2"
	for _, step := range []string{"edit b", "push again unchanged"} {
		resp := push(step)
		var gotA string
		for _, f := range resp.Pushed {
			if f.Path == "wiki/a.md" {
				gotA = f.SHA
			}
		}
		if gotA != aCommit {
			t.Errorf("%s: pushed[a].sha = %s, want a's last-touch commit %s", step, gotA, aCommit)
		}
	}
}

// wp02PushFile is the wire shape of a push file including expect_absent,
// which sync.PushFile does not carry on this branch (WP-0.1 adds it).
type wp02PushFile struct {
	Path         string `json:"path"`
	Content      string `json:"content"`
	ParentSHA    string `json:"parent_sha,omitempty"`
	ExpectAbsent bool   `json:"expect_absent,omitempty"`
}

// wp02Push POSTs a push with an optional X-Contexo-Client header and decodes
// the response when it is JSON.
func wp02Push(t *testing.T, r http.Handler, repo, bearer, client string, files ...wp02PushFile) (*httptest.ResponseRecorder, sync.PushResponse) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"message": "wp02 push", "files": files})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return wp02PushRaw(t, r, repo, bearer, client, body)
}

func wp02PushRaw(t *testing.T, r http.Handler, repo, bearer, client string, body []byte) (*httptest.ResponseRecorder, sync.PushResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/repos/"+repo+"/sync/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	if client != "" {
		req.Header.Set("X-Contexo-Client", client)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp sync.PushResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

func wp02PushedSHA(resp sync.PushResponse, path string) string {
	for _, f := range resp.Pushed {
		if f.Path == path {
			return f.SHA
		}
	}
	return ""
}

// wp02UserRig is a rig with alice signed in as the owner of repo "team-repo"
// (activity is recorded only for real users, not the legacy key).
func wp02UserRig(t *testing.T) (*testRig, string) {
	t.Helper()
	rig := setupRig(t)
	wp02WireSync(rig)
	alice := signIn(t, rig.router, "alice-token")
	if w := doJSON(t, rig.router, "POST", "/v1/repos", alice, map[string]string{"id": "team-repo"}); w.Code != http.StatusCreated {
		t.Fatalf("create repo: %d %s", w.Code, w.Body.String())
	}
	return rig, alice
}

func wp02LatestActivity(t *testing.T, rig *testRig) (int, sync.ActivityDetail) {
	t.Helper()
	events, err := rig.users.ListActivity("team-repo", 1000, 0)
	if err != nil {
		t.Fatalf("list activity: %v", err)
	}
	var d sync.ActivityDetail
	if len(events) > 0 && events[0].Detail != "" {
		if err := json.Unmarshal([]byte(events[0].Detail), &d); err != nil {
			t.Fatalf("decode detail %q: %v", events[0].Detail, err)
		}
	}
	return len(events), d
}

func TestPush_NoopPagesNotInActivity(t *testing.T) {
	rig, alice := wp02UserRig(t)
	w, resp := wp02Push(t, rig.router, "team-repo", alice, "", wp02PushFile{Path: "wiki/a.md", Content: "a"})
	if w.Code != http.StatusOK {
		t.Fatalf("seed push: %d %s", w.Code, w.Body.String())
	}
	before, _ := wp02LatestActivity(t, rig)

	sha := wp02PushedSHA(resp, "wiki/a.md")
	w, _ = wp02Push(t, rig.router, "team-repo", alice, "", wp02PushFile{Path: "wiki/a.md", Content: "a", ParentSHA: sha})
	if w.Code != http.StatusOK {
		t.Fatalf("no-op push: %d %s", w.Code, w.Body.String())
	}
	if after, _ := wp02LatestActivity(t, rig); after != before {
		t.Errorf("an all-no-op push recorded activity: %d -> %d rows", before, after)
	}
}

func TestPush_PartialPushActivityListsOnlyChanged(t *testing.T) {
	rig, alice := wp02UserRig(t)
	_, resp := wp02Push(t, rig.router, "team-repo", alice, "",
		wp02PushFile{Path: "wiki/a.md", Content: "a"}, wp02PushFile{Path: "wiki/b.md", Content: "b"})

	w, _ := wp02Push(t, rig.router, "team-repo", alice, "",
		wp02PushFile{Path: "wiki/a.md", Content: "a", ParentSHA: wp02PushedSHA(resp, "wiki/a.md")},
		wp02PushFile{Path: "wiki/b.md", Content: "b2", ParentSHA: wp02PushedSHA(resp, "wiki/b.md")})
	if w.Code != http.StatusOK {
		t.Fatalf("partial push: %d %s", w.Code, w.Body.String())
	}
	_, d := wp02LatestActivity(t, rig)
	if len(d.Paths) != 1 || d.Paths[0] != "wiki/b.md" {
		t.Errorf("activity paths = %v, want only the changed [wiki/b.md]", d.Paths)
	}
}

func TestPush_ActivityRecordsClient(t *testing.T) {
	rig, alice := wp02UserRig(t)
	w, _ := wp02Push(t, rig.router, "team-repo", alice, "contexo-vscode/0.2.0", wp02PushFile{Path: "wiki/a.md", Content: "a"})
	if w.Code != http.StatusOK {
		t.Fatalf("push: %d %s", w.Code, w.Body.String())
	}
	_, d := wp02LatestActivity(t, rig)
	if d.Client != "contexo-vscode/0.2.0" {
		t.Errorf("activity client = %q, want contexo-vscode/0.2.0", d.Client)
	}
	if len(d.Paths) != 1 || d.Paths[0] != "wiki/a.md" {
		t.Errorf("activity paths = %v, want [wiki/a.md]", d.Paths)
	}
}

func TestPush_ActivityClientIsSanitized(t *testing.T) {
	rig, alice := wp02UserRig(t)
	hostile := "evil\x1b[31m" + strings.Repeat("x", 300)
	wp02Push(t, rig.router, "team-repo", alice, hostile, wp02PushFile{Path: "wiki/a.md", Content: "a"})
	_, d := wp02LatestActivity(t, rig)
	if d.Client == "" || len(d.Client) > 128 {
		t.Errorf("stored client has %d bytes, want 1..128", len(d.Client))
	}
	for _, r := range d.Client {
		if r < 0x20 || r == 0x7f {
			t.Errorf("stored client %q contains control character %U", d.Client, r)
		}
	}
}

func TestPush_NewHeadIsRepoHead(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	_, r1 := wp02Push(t, rig.router, "head-repo", "legacy-key", "", wp02PushFile{Path: "wiki/a.md", Content: "a"})
	wp02Push(t, rig.router, "head-repo", "legacy-key", "", wp02PushFile{Path: "wiki/b.md", Content: "b"})
	head, _ := rig.store.HeadSHA("head-repo")
	aSHA := wp02PushedSHA(r1, "wiki/a.md")

	w, resp := wp02Push(t, rig.router, "head-repo", "legacy-key", "", wp02PushFile{Path: "wiki/a.md", Content: "a", ParentSHA: aSHA})
	if w.Code != http.StatusOK {
		t.Fatalf("no-op push: %d %s", w.Code, w.Body.String())
	}
	if resp.NewHead != head {
		t.Errorf("new_head = %s, want repo HEAD %s (not the no-op file's sha)", resp.NewHead, head)
	}
	if got := wp02PushedSHA(resp, "wiki/a.md"); got != aSHA {
		t.Errorf("pushed[a].sha = %s, want a's last-touch commit %s", got, aSHA)
	}
}

func TestPush_ExpectAbsent409(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	_, seed := wp02Push(t, rig.router, "race-repo", "legacy-key", "", wp02PushFile{Path: "wiki/a.md", Content: "teammate"})
	current := wp02PushedSHA(seed, "wiki/a.md")

	w, resp := wp02Push(t, rig.router, "race-repo", "legacy-key", "", wp02PushFile{Path: "wiki/a.md", Content: "mine", ExpectAbsent: true})
	if w.Code != http.StatusConflict {
		t.Fatalf("expect_absent on an existing path: %d %s, want 409", w.Code, w.Body.String())
	}
	if len(resp.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want one", resp.Conflicts)
	}
	c := resp.Conflicts[0]
	if c.Path != "wiki/a.md" || c.CurrentSHA != current || string(c.CurrentContent) != "teammate" || c.ExpectedParentSHA != "" {
		t.Errorf("conflict = %+v, want current_sha %s, current_content teammate, expected_parent_sha \"\"", c, current)
	}
	if !strings.Contains(w.Body.String(), `"expected_parent_sha":""`) {
		t.Errorf("body lacks \"expected_parent_sha\":\"\": %s", w.Body.String())
	}
	if content, sha, _ := rig.store.Read("race-repo", "wiki/a.md"); string(content) != "teammate" || sha != current {
		t.Errorf("server copy changed to %q@%s", content, sha)
	}
}

func TestPush_ExpectAbsentOnAbsentPathWrites(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	w, resp := wp02Push(t, rig.router, "race-repo", "legacy-key", "", wp02PushFile{Path: "wiki/new.md", Content: "fresh", ExpectAbsent: true})
	if w.Code != http.StatusOK || len(resp.Conflicts) != 0 {
		t.Fatalf("expect_absent on an absent path: %d %s", w.Code, w.Body.String())
	}
	if got, _ := rig.store.HeadSHAForPath("race-repo", "wiki/new.md"); got == "" || wp02PushedSHA(resp, "wiki/new.md") != got {
		t.Errorf("pushed sha %q, path commit %q", wp02PushedSHA(resp, "wiki/new.md"), got)
	}
}

func TestPush_ExpectAbsentNonBoolIs400(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	body := []byte(`{"files":[{"path":"wiki/a.md","content":"x","expect_absent":"yes"}]}`)
	if w, _ := wp02PushRaw(t, rig.router, "race-repo", "legacy-key", "", body); w.Code != http.StatusBadRequest {
		t.Errorf("non-bool expect_absent: %d %s, want 400", w.Code, w.Body.String())
	}
	if rig.store.Exists("race-repo") {
		if _, _, err := rig.store.Read("race-repo", "wiki/a.md"); err == nil {
			t.Errorf("a rejected push wrote the page")
		}
	}
}

// TestPush_OversizeBodyIs413: the router's MaxBody caps the body with
// http.MaxBytesReader; a push over the cap is 413, not a generic 400.
func TestPush_OversizeBodyIs413(t *testing.T) {
	rig := setupRig(t)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64)
		c.Next()
	})
	v1 := r.Group("/v1")
	v1.Use(auth.GinMiddleware(rig.resolver.Validator()))
	v1.POST("/repos/:id/sync/push", rig.h.Push)

	w, _ := wp02Push(t, r, "big-repo", "legacy-key", "", wp02PushFile{Path: "wiki/a.md", Content: strings.Repeat("x", 1024)})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize push: %d %s, want 413", w.Code, w.Body.String())
	}
}
