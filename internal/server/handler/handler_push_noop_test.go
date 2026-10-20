package handler

import (
	"net/http/httptest"
	"testing"

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
