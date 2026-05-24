package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/sync"
)

// TestPush_AuthorFallsBackToAuthenticatedUser exercises the fix for the
// "unknown" attribution bug. When the client doesn't send AuthorName/Email
// (older CLI, manual PAT paste, etc.) the server should look up the
// authenticated user's identity and use it, rather than baking "unknown"
// into the git commit.
func TestPush_AuthorFallsBackToAuthenticatedUser(t *testing.T) {
	rig := setupRig(t)
	wirePushAndTimeline(rig)

	aliceToken := signIn(t, rig.router, "alice-token")

	// Push as Alice with EMPTY AuthorName/Email — simulates an older CLI
	// that doesn't round-trip the identity through credentials.json.
	pushBody := sync.PushRequest{
		AuthorName:  "",
		AuthorEmail: "",
		Message:     "test push without author",
		Files: []sync.PushFile{{
			Path:    "wiki/concepts/test.md",
			Content: "---\nslug: test\ntype: concept\n---\nhello\n",
		}},
	}
	w := doJSON(t, rig.router, "POST", "/v1/repos/test-repo/sync/push", aliceToken, pushBody)
	if w.Code != http.StatusOK {
		t.Fatalf("push: %d %s", w.Code, w.Body.String())
	}

	// Inspect the resulting commit — it should be attributed to Alice (her
	// stored name + email from the users table), NOT to "unknown".
	w = doJSON(t, rig.router, "GET", "/v1/repos/test-repo/timeline?limit=5", aliceToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("timeline: %d %s", w.Code, w.Body.String())
	}
	var tl struct {
		Commits []gitstore.CommitMeta `json:"commits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tl); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tl.Commits) == 0 {
		t.Fatal("no commits in timeline")
	}
	top := tl.Commits[0]
	if top.Author == "unknown" {
		t.Errorf("commit author still 'unknown' — fallback didn't fire: %+v", top)
	}
	if top.Author != "Alice" {
		t.Errorf("expected author 'Alice', got %q (full commit: %+v)", top.Author, top)
	}
	if top.Email != "alice@example.com" {
		t.Errorf("expected email 'alice@example.com', got %q", top.Email)
	}
}

// TestPush_AuthorRespectsClientSentValue ensures the new fallback doesn't
// override what the client explicitly sent — newer CLIs that DO round-trip
// identity should still control the attribution.
func TestPush_AuthorRespectsClientSentValue(t *testing.T) {
	rig := setupRig(t)
	wirePushAndTimeline(rig)

	aliceToken := signIn(t, rig.router, "alice-token")

	pushBody := sync.PushRequest{
		AuthorName:  "Custom Name",
		AuthorEmail: "custom@example.com",
		Message:     "test push with explicit author",
		Files: []sync.PushFile{{
			Path:    "wiki/concepts/test2.md",
			Content: "---\nslug: test2\ntype: concept\n---\nhi\n",
		}},
	}
	w := doJSON(t, rig.router, "POST", "/v1/repos/test-repo2/sync/push", aliceToken, pushBody)
	if w.Code != http.StatusOK {
		t.Fatalf("push: %d %s", w.Code, w.Body.String())
	}

	w = doJSON(t, rig.router, "GET", "/v1/repos/test-repo2/timeline?limit=5", aliceToken, nil)
	var tl struct {
		Commits []gitstore.CommitMeta `json:"commits"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tl)
	if len(tl.Commits) == 0 || tl.Commits[0].Author != "Custom Name" {
		t.Errorf("expected explicit 'Custom Name', got %+v", tl.Commits)
	}
}

// wirePushAndTimeline adds the Push + Timeline routes to the rig's gin
// engine under a /v1 group that runs the same auth middleware setupRig
// already uses for /me, /repos, etc. Without this, routes registered
// directly on rig.router skip the auth check entirely, leaving uid empty
// in the handler — which masks exactly the path this test cares about.
func wirePushAndTimeline(rig *testRig) {
	h := New(rig.store, rig.users, rig.signer, rig.verifier)
	v1 := rig.router.Group("/v1")
	v1.Use(auth.GinMiddleware(rig.resolver.Validator()))
	v1.POST("/repos/:id/sync/push", h.Push)
	v1.GET("/repos/:id/timeline", h.Timeline)
	_ = gin.TestMode // touch the import so removing wirePushAndTimeline doesn't break the build
}