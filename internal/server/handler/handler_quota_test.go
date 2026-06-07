package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/quota"
)

// fakeQuota is a stand-in for the hosted policy: deny once a simple count cap
// is reached. Core's job is only to compute counts and honor the verdict.
type fakeQuota struct {
	maxRepos   int
	maxMembers int
}

func (f fakeQuota) AllowRepoCreate(_ string, owned int) error {
	if owned >= f.maxRepos {
		return &quota.LimitError{Kind: "repos", Limit: f.maxRepos, Message: "repo cap hit", UpgradeURL: "https://contexo.live/#pricing"}
	}
	return nil
}

func (f fakeQuota) AllowMemberAdd(_ string, _ []string, count int) error {
	if count >= f.maxMembers {
		return &quota.LimitError{Kind: "members", Limit: f.maxMembers, Message: "member cap hit", UpgradeURL: "https://contexo.live/#pricing"}
	}
	return nil
}

// wireCreatePaths registers the CLI/agent repo-creation routes on the rig's own
// handler, so rig.h.SetQuota governs them. setupRig doesn't register these (the
// push-author and activity tests wire their own copies on a separate handler),
// so the quota tests use this to exercise rig.h's policy.
func wireCreatePaths(rig *testRig) {
	v1 := rig.router.Group("/v1")
	v1.Use(auth.GinMiddleware(rig.resolver.Validator()))
	v1.POST("/repos/:id", rig.h.CreateRepoLegacy)
	v1.POST("/repos/:id/sync/push", rig.h.Push)
}

func TestCreateRepo_QuotaBlocksOverCapWith402(t *testing.T) {
	rig := setupRig(t)
	rig.h.SetQuota(fakeQuota{maxRepos: 2, maxMembers: 99})
	aliceToken := signIn(t, rig.router, "alice-token")

	// Owned=0 and owned=1 are under the cap of 2.
	for _, id := range []string{"repo1", "repo2"} {
		w := doJSON(t, rig.router, "POST", "/v1/repos", aliceToken, map[string]string{"id": id})
		if w.Code != http.StatusCreated {
			t.Fatalf("create %s: want 201, got %d %s", id, w.Code, w.Body.String())
		}
	}

	// The 3rd repo (owned=2) is blocked with 402 + structured body.
	w := doJSON(t, rig.router, "POST", "/v1/repos", aliceToken, map[string]string{"id": "repo3"})
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("3rd repo: want 402, got %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Error      string `json:"error"`
		Code       string `json:"code"`
		Limit      int    `json:"limit"`
		UpgradeURL string `json:"upgrade_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 402 body: %v", err)
	}
	if body.Code != "quota_repos" || body.Limit != 2 || body.UpgradeURL == "" || body.Error == "" {
		t.Errorf("unexpected 402 body: %+v", body)
	}

	// A blocked create must not leave an orphan repo on disk.
	if rig.store.Exists("repo3") {
		t.Error("repo3 was created on disk despite the quota rejection")
	}
}

func TestJoinRepo_QuotaBlocksOverCapWith402(t *testing.T) {
	rig := setupRig(t)
	// Cap members at 2 total: owner + 1 invited; the next join is blocked.
	rig.h.SetQuota(fakeQuota{maxRepos: 99, maxMembers: 2})
	aliceToken := signIn(t, rig.router, "alice-token")

	if w := doJSON(t, rig.router, "POST", "/v1/repos", aliceToken, map[string]string{"id": "team"}); w.Code != http.StatusCreated {
		t.Fatalf("create repo: %d %s", w.Code, w.Body.String())
	}
	mint := func() string {
		w := doJSON(t, rig.router, "POST", "/v1/repos/team/invite-keys", aliceToken, map[string]string{"label": "k"})
		if w.Code != http.StatusCreated {
			t.Fatalf("mint key: %d %s", w.Code, w.Body.String())
		}
		var m struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return m.Token
	}

	// Repo has 1 member (alice). Bob joins -> count 1 < 2 -> allowed (now 2).
	bobToken := signIn(t, rig.router, "bob-token")
	if w := doJSON(t, rig.router, "POST", "/v1/repos/join", bobToken, map[string]string{"key": mint()}); w.Code != http.StatusOK {
		t.Fatalf("bob join: want 200, got %d %s", w.Code, w.Body.String())
	}

	// Bob re-joining is idempotent and must NOT be blocked even though count==2.
	if w := doJSON(t, rig.router, "POST", "/v1/repos/join", bobToken, map[string]string{"key": mint()}); w.Code != http.StatusOK {
		t.Fatalf("bob re-join (idempotent): want 200, got %d %s", w.Code, w.Body.String())
	}

	// Carol joins -> count 2 >= 2 -> blocked with 402.
	rig.verifier["carol-token"] = &auth.GoogleClaims{Email: "carol@example.com", Name: "Carol", Subject: "g3", EmailVerified: true}
	carolToken := signIn(t, rig.router, "carol-token")
	w := doJSON(t, rig.router, "POST", "/v1/repos/join", carolToken, map[string]string{"key": mint()})
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("carol join: want 402, got %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Code  string `json:"code"`
		Limit int    `json:"limit"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Code != "quota_members" || body.Limit != 2 {
		t.Errorf("unexpected 402 body: %+v", body)
	}
}

func TestCreateRepo_DefaultPolicyIsUnlimited(t *testing.T) {
	// A rig with no SetQuota must behave as before (self-host/OSS = no caps).
	rig := setupRig(t)
	aliceToken := signIn(t, rig.router, "alice-token")
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		if w := doJSON(t, rig.router, "POST", "/v1/repos", aliceToken, map[string]string{"id": id}); w.Code != http.StatusCreated {
			t.Fatalf("create %s under default policy: want 201, got %d %s", id, w.Code, w.Body.String())
		}
	}
}

// A repo created via `ctx push` to a new repo id (the CLI/agent path) must be
// gated the same as the dashboard create — otherwise the cap is bypassable.
func TestPush_NewRepoRespectsQuota(t *testing.T) {
	rig := setupRig(t)
	wireCreatePaths(rig)
	rig.h.SetQuota(fakeQuota{maxRepos: 1, maxMembers: 99})
	aliceToken := signIn(t, rig.router, "alice-token")
	pushBody := map[string]any{
		"files":   []map[string]string{{"path": "wiki/concepts/a.md", "content": "# A"}},
		"message": "first",
	}

	// owned=0 -> first new repo via push is allowed (auto-creates).
	if w := doJSON(t, rig.router, "POST", "/v1/repos/repo1/sync/push", aliceToken, pushBody); w.Code != http.StatusOK {
		t.Fatalf("push repo1: want 200, got %d %s", w.Code, w.Body.String())
	}
	// owned=1 -> second new repo via push is blocked with 402.
	w := doJSON(t, rig.router, "POST", "/v1/repos/repo2/sync/push", aliceToken, pushBody)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("push repo2: want 402, got %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Code != "quota_repos" {
		t.Errorf("expected code quota_repos, got %q", body.Code)
	}
	// The blocked repo must not have been created on disk.
	if rig.store.Exists("repo2") {
		t.Error("repo2 was created despite the quota rejection")
	}
}

// Pushing again to a repo you already own is not a creation, so it must never
// be quota-checked, even at the cap.
func TestPush_ExistingRepoNotQuotaChecked(t *testing.T) {
	rig := setupRig(t)
	wireCreatePaths(rig)
	rig.h.SetQuota(fakeQuota{maxRepos: 1, maxMembers: 99})
	aliceToken := signIn(t, rig.router, "alice-token")
	body := map[string]any{
		"files":   []map[string]string{{"path": "wiki/concepts/a.md", "content": "# A"}},
		"message": "x",
	}
	if w := doJSON(t, rig.router, "POST", "/v1/repos/repo1/sync/push", aliceToken, body); w.Code != http.StatusOK {
		t.Fatalf("create repo1: %d %s", w.Code, w.Body.String())
	}
	// At the cap now (owns 1); a second push to the EXISTING repo1 must pass.
	body["files"] = []map[string]string{{"path": "wiki/concepts/b.md", "content": "# B"}}
	if w := doJSON(t, rig.router, "POST", "/v1/repos/repo1/sync/push", aliceToken, body); w.Code != http.StatusOK {
		t.Fatalf("second push to existing repo1: want 200, got %d %s", w.Code, w.Body.String())
	}
}

// The legacy POST /v1/repos/:id create path must be gated too.
func TestCreateRepoLegacy_RespectsQuota(t *testing.T) {
	rig := setupRig(t)
	wireCreatePaths(rig)
	rig.h.SetQuota(fakeQuota{maxRepos: 1, maxMembers: 99})
	aliceToken := signIn(t, rig.router, "alice-token")
	if w := doJSON(t, rig.router, "POST", "/v1/repos/legacy1", aliceToken, nil); w.Code != http.StatusCreated {
		t.Fatalf("legacy create 1: want 201, got %d %s", w.Code, w.Body.String())
	}
	w := doJSON(t, rig.router, "POST", "/v1/repos/legacy2", aliceToken, nil)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("legacy create 2: want 402, got %d %s", w.Code, w.Body.String())
	}
}
