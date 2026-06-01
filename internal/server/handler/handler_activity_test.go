package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/sync"
)

// doGet issues an authenticated GET with optional extra headers (used to send
// X-Contexo-Client on pulls).
func doGet(t *testing.T, r http.Handler, path, bearer string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// wireSyncAndActivity adds the push/pull/activity routes to the rig under a /v1
// group running the same auth middleware, so the handlers see a real user_id.
func wireSyncAndActivity(rig *testRig) {
	h := New(rig.store, rig.users, rig.signer, rig.verifier)
	v1 := rig.router.Group("/v1")
	v1.Use(auth.GinMiddleware(rig.resolver.Validator()))
	v1.POST("/repos/:id/sync/push", h.Push)
	v1.GET("/repos/:id/sync/pull", h.Pull)
	v1.GET("/repos/:id/activity", h.Activity)
}

func TestE2E_ActivityFeed(t *testing.T) {
	rig := setupRig(t)
	wireSyncAndActivity(rig)
	aliceToken := signIn(t, rig.router, "alice-token")
	doJSON(t, rig.router, "POST", "/v1/repos", aliceToken, map[string]string{"id": "team-repo"})

	// Alice pushes a page -> records a "push".
	push := sync.PushRequest{
		Message: "add demo",
		Files:   []sync.PushFile{{Path: "wiki/concepts/demo.md", Content: "hello"}},
	}
	w := doJSON(t, rig.router, "POST", "/v1/repos/team-repo/sync/push", aliceToken, push)
	if w.Code != http.StatusOK {
		t.Fatalf("push: %d %s", w.Code, w.Body.String())
	}

	w = doJSON(t, rig.router, "GET", "/v1/repos/team-repo/activity", aliceToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("activity: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("alice@example.com")) ||
		!bytes.Contains(w.Body.Bytes(), []byte(`"push"`)) {
		t.Errorf("expected alice push in activity, got %s", w.Body.String())
	}
	// Push detail should carry the pushed path.
	if !bytes.Contains(w.Body.Bytes(), []byte("wiki/concepts/demo.md")) {
		t.Errorf("expected push detail to include the pushed path, got %s", w.Body.String())
	}

	// Bob joins, then pulls -> receives alice's page -> records a "pull".
	w = doJSON(t, rig.router, "POST", "/v1/repos/team-repo/invite-keys", aliceToken, map[string]string{"label": "for-bob"})
	var mint struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &mint)
	bobToken := signIn(t, rig.router, "bob-token")
	doJSON(t, rig.router, "POST", "/v1/repos/join", bobToken, map[string]string{"key": mint.Token})

	w = doJSON(t, rig.router, "GET", "/v1/repos/team-repo/sync/pull", bobToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("pull: %d %s", w.Code, w.Body.String())
	}
	var pull sync.PullResponse
	_ = json.Unmarshal(w.Body.Bytes(), &pull)
	if len(pull.Files) == 0 {
		t.Fatalf("expected bob's pull to return alice's file")
	}

	w = doJSON(t, rig.router, "GET", "/v1/repos/team-repo/activity", aliceToken, nil)
	body := w.Body.Bytes()
	if !bytes.Contains(body, []byte("bob@example.com")) || !bytes.Contains(body, []byte(`"pull"`)) {
		t.Errorf("expected bob pull in activity, got %s", body)
	}
	// Newest first: bob's pull should appear before alice's push.
	idxBob := bytes.Index(body, []byte("bob@example.com"))
	idxAlice := bytes.Index(body, []byte("alice@example.com"))
	if idxBob >= 0 && idxAlice >= 0 && idxBob > idxAlice {
		t.Errorf("expected bob's pull before alice's push (reverse-chron), got %s", body)
	}

	// A no-op pull (already up to date) must NOT add an event.
	before := activityCount(t, rig, "team-repo")
	w = doJSON(t, rig.router, "GET", "/v1/repos/team-repo/sync/pull?since="+pull.NewHead, bobToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("noop pull: %d %s", w.Code, w.Body.String())
	}
	after := activityCount(t, rig, "team-repo")
	if after != before {
		t.Errorf("no-op pull should not record activity: before=%d after=%d", before, after)
	}
}

func activityCount(t *testing.T, rig *testRig, repoID string) int {
	t.Helper()
	events, err := rig.users.ListActivity(repoID, 1000, 0)
	if err != nil {
		t.Fatalf("list activity: %v", err)
	}
	return len(events)
}

func TestE2E_ActivityRecordsPullClient(t *testing.T) {
	rig := setupRig(t)
	wireSyncAndActivity(rig)
	aliceToken := signIn(t, rig.router, "alice-token")
	doJSON(t, rig.router, "POST", "/v1/repos", aliceToken, map[string]string{"id": "team-repo"})
	doJSON(t, rig.router, "POST", "/v1/repos/team-repo/sync/push", aliceToken, sync.PushRequest{
		Message: "seed", Files: []sync.PushFile{{Path: "wiki/concepts/x.md", Content: "x"}},
	})

	// Bob joins and pulls, announcing its client via X-Contexo-Client.
	w := doJSON(t, rig.router, "POST", "/v1/repos/team-repo/invite-keys", aliceToken, map[string]string{"label": "b"})
	var mint struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &mint)
	bobToken := signIn(t, rig.router, "bob-token")
	doJSON(t, rig.router, "POST", "/v1/repos/join", bobToken, map[string]string{"key": mint.Token})

	w = doGet(t, rig.router, "/v1/repos/team-repo/sync/pull", bobToken, map[string]string{"X-Contexo-Client": "claude-code"})
	if w.Code != http.StatusOK {
		t.Fatalf("pull: %d %s", w.Code, w.Body.String())
	}

	w = doGet(t, rig.router, "/v1/repos/team-repo/activity", aliceToken, nil)
	if !bytes.Contains(w.Body.Bytes(), []byte("claude-code")) {
		t.Errorf("expected pull detail to record the client, got %s", w.Body.String())
	}
}

func TestE2E_ActivityPaging(t *testing.T) {
	rig := setupRig(t)
	wireSyncAndActivity(rig)
	aliceToken := signIn(t, rig.router, "alice-token")
	doJSON(t, rig.router, "POST", "/v1/repos", aliceToken, map[string]string{"id": "team-repo"})

	for _, name := range []string{"a", "b", "c"} {
		doJSON(t, rig.router, "POST", "/v1/repos/team-repo/sync/push", aliceToken, sync.PushRequest{
			Message: "p", Files: []sync.PushFile{{Path: "wiki/concepts/" + name + ".md", Content: name}},
		})
	}

	var page struct {
		Events []json.RawMessage `json:"events"`
		Total  int               `json:"total"`
	}
	w := doGet(t, rig.router, "/v1/repos/team-repo/activity?limit=2", aliceToken, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if page.Total != 3 {
		t.Errorf("total = %d, want 3", page.Total)
	}
	if len(page.Events) != 2 {
		t.Errorf("limit=2 returned %d events", len(page.Events))
	}

	w = doGet(t, rig.router, "/v1/repos/team-repo/activity?limit=2&offset=2", aliceToken, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Events) != 1 {
		t.Errorf("offset=2 returned %d events, want 1", len(page.Events))
	}
}
