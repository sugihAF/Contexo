package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	gosync "sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/userstore"
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

	// Format characters (bidi overrides, zero-width) would let a member make
	// the name render as another client in everyone's activity feed.
	spoof := "ctx‮edocsv-oxetnoc​⁦x⁩"
	wp02Push(t, rig.router, "team-repo", alice, spoof, wp02PushFile{Path: "wiki/b.md", Content: "b"})
	_, d = wp02LatestActivity(t, rig)
	if d.Client != "ctxedocsv-oxetnocx" {
		t.Errorf("stored client %q, want the format characters dropped (ctxedocsv-oxetnocx)", d.Client)
	}

	// Line/paragraph separators and private-use runes are not graphic either:
	// U+2028 would render the name as two lines in the feed.
	wp02Push(t, rig.router, "team-repo", alice, "ctx-mcp contexo-vscode/0.2.0 ", wp02PushFile{Path: "wiki/c.md", Content: "c"})
	_, d = wp02LatestActivity(t, rig)
	if d.Client != "ctx-mcpcontexo-vscode/0.2.0" {
		t.Errorf("stored client %q, want only graphic runes kept (ctx-mcpcontexo-vscode/0.2.0)", d.Client)
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

// wp02UserID is the userstore id of a signed-in user.
func wp02UserID(t *testing.T, rig *testRig, email string) string {
	t.Helper()
	u, err := rig.users.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("user %s: %v", email, err)
	}
	return u.ID
}

// wp02Role is uid's role in repo, or "" when uid is not a member.
func wp02Role(t *testing.T, rig *testRig, repo, uid string) string {
	t.Helper()
	role, err := rig.users.GetRole(repo, uid)
	if errors.Is(err, userstore.ErrNotFound) {
		return ""
	}
	if err != nil {
		t.Fatalf("role of %s in %s: %v", uid, repo, err)
	}
	return role
}

// wp02WireLegacyCreate mounts the deprecated POST /v1/repos/:id on a new /v1
// group of the rig (setupRig does not register it).
func wp02WireLegacyCreate(rig *testRig) {
	v1 := rig.router.Group("/v1")
	v1.Use(auth.GinMiddleware(rig.resolver.Validator()))
	v1.POST("/repos/:id", rig.h.CreateRepoLegacy)
}

// wp02LegacyCreate is POST /v1/repos/<repo> (the deprecated create) as bearer.
func wp02LegacyCreate(t *testing.T, rig *testRig, repo, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, rig.router, http.MethodPost, "/v1/repos/"+repo, bearer, nil)
}

// wp02WantLegacyCreated asserts the deprecated create's 201 {"repo_id":repo}.
func wp02WantLegacyCreated(t *testing.T, label string, w *httptest.ResponseRecorder, repo string) {
	t.Helper()
	var body struct {
		RepoID string `json:"repo_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusCreated || body.RepoID != repo {
		t.Errorf("%s: %d %s, want 201 {\"repo_id\":%q}", label, w.Code, w.Body.String(), repo)
	}
}

// wp02WantRepoExists asserts the 409 {"error":"repo already exists"} that a
// signed-in caller gets for an existing repo they are not a member of.
func wp02WantRepoExists(t *testing.T, label string, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "repo already exists") {
		t.Errorf("%s: %d %s, want 409 repo already exists", label, w.Code, w.Body.String())
	}
}

// wp02RepoIDRule is the 400 error text for a new repo id outside
// repoIDPattern, the text POST /v1/repos has always used.
const wp02RepoIDRule = "id must match [A-Za-z0-9][A-Za-z0-9_-]{0,63}"

// wp02WantRepoIDRule asserts the 400 {"error": wp02RepoIDRule} for a new,
// non-canonical repo id.
func wp02WantRepoIDRule(t *testing.T, label string, w *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusBadRequest || body.Error != wp02RepoIDRule {
		t.Errorf("%s: %d %s, want 400 {\"error\":%q}", label, w.Code, w.Body.String(), wp02RepoIDRule)
	}
}

// wp02Race runs fns in goroutines released together and waits for all of
// them. fns must not call t.Fatal.
func wp02Race(fns ...func()) {
	var wg gosync.WaitGroup
	start := make(chan struct{})
	for _, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn()
		}()
	}
	close(start)
	wg.Wait()
}

// TestCreateRepoLegacy_NoOwnerTakeover: the deprecated POST /v1/repos/:id
// must not make a signed-in caller an owner of a repo someone else owns. It
// used to: the quota check ran only for a new repo, Init was a no-op on an
// existing one, and AddMember(owner) ran unconditionally. It then still did
// through the orphan claim, for an alias of the repo id (gitstore keeps only
// the [A-Za-z0-9_-] characters of an id, so "alice.repo" is alice-repo's
// directory but matches none of its membership rows).
func TestCreateRepoLegacy_NoOwnerTakeover(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	wp02WireLegacyCreate(rig)

	// Alice signs in first, so she is the one who would claim pre-existing
	// orphans at sign-in; "orphan" is created only after both sign-ins.
	alice := signIn(t, rig.router, "alice-token")
	if w := doJSON(t, rig.router, http.MethodPost, "/v1/repos", alice, map[string]string{"id": "alice-repo"}); w.Code != http.StatusCreated {
		t.Fatalf("alice creates alice-repo: %d %s", w.Code, w.Body.String())
	}
	if w, _ := wp02Push(t, rig.router, "alice-repo", alice, "", wp02PushFile{Path: "wiki/secret.md", Content: "alice only"}); w.Code != http.StatusOK {
		t.Fatalf("alice pushes: %d %s", w.Code, w.Body.String())
	}
	bob := signIn(t, rig.router, "bob-token")
	aliceID := wp02UserID(t, rig, "alice@example.com")
	bobID := wp02UserID(t, rig, "bob@example.com")
	create := func(repo, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		return wp02LegacyCreate(t, rig, repo, bearer)
	}
	wantCreated := func(label string, w *httptest.ResponseRecorder, repo string) {
		t.Helper()
		wp02WantLegacyCreated(t, label, w, repo)
	}
	// wantNoAccess: bob holds no row for repo and cannot read alice's page
	// through it.
	wantNoAccess := func(label, repo string) {
		t.Helper()
		if r := wp02Role(t, rig, repo, bobID); r != "" {
			t.Errorf("%s: bob has role %q in %q", label, r, repo)
		}
		if owners, err := rig.users.RepoOwners(repo); err != nil || len(owners) != 0 {
			t.Errorf("%s: %q has owners %v (err %v), want none", label, repo, owners, err)
		}
		if w := doJSON(t, rig.router, http.MethodGet, "/v1/repos/"+repo+"/pages/wiki/secret.md", bob, nil); w.Code != http.StatusForbidden {
			t.Errorf("%s: bob reads alice's page through %q: %d %s, want 403", label, repo, w.Code, w.Body.String())
		}
	}

	// Bob cannot take over Alice's repo, and gains no access through trying.
	wp02WantRepoExists(t, "bob creates alice-repo", create("alice-repo", bob))
	if ok, err := rig.users.IsMember("alice-repo", bobID); err != nil || ok {
		t.Errorf("bob is a member of alice-repo after the legacy create (member=%v err=%v)", ok, err)
	}
	if w := doJSON(t, rig.router, http.MethodGet, "/v1/repos/alice-repo/pages/wiki/secret.md", bob, nil); w.Code != http.StatusForbidden {
		t.Errorf("bob reads alice's page: %d %s, want 403", w.Code, w.Body.String())
	}

	// Nor through an alias of its id. An existing repo is never held to the
	// id pattern, so an alias answers 409 like the id itself (400 would be
	// acceptable too); it must never be a 201.
	for _, alias := range []string{"alice-repo.", "alice.-repo", "al.ice-repo", "ALICE-REPO"} {
		if !rig.store.Exists(alias) {
			// Only "ALICE-REPO", on a case-sensitive filesystem, where it is
			// a different repo, not an alias.
			t.Logf("%q is not an alias of alice-repo on this filesystem", alias)
			continue
		}
		w := create(alias, bob)
		if w.Code != http.StatusConflict && w.Code != http.StatusBadRequest {
			t.Errorf("bob creates alias %q of alice-repo: %d %s, want 409 (or 400)", alias, w.Code, w.Body.String())
		}
		wantNoAccess("alias "+alias, alias)
	}
	if r := wp02Role(t, rig, "alice-repo", bobID); r != "" {
		t.Errorf("bob has role %q in alice-repo after the alias attempts", r)
	}

	// A NEW id outside the pattern is refused before anything is created.
	wp02WantRepoIDRule(t, "bob creates new bob.new", create("bob.new", bob))
	if rig.store.Exists("bob.new") || rig.store.Exists("bobnew") {
		t.Errorf("a refused bob.new left a repo on disk")
	}
	if r := wp02Role(t, rig, "bob.new", bobID); r != "" {
		t.Errorf("bob has role %q in the refused bob.new", r)
	}

	// Alice re-creating her own repo stays idempotent and she stays owner.
	wantCreated("alice re-creates alice-repo", create("alice-repo", alice), "alice-repo")
	if r := wp02Role(t, rig, "alice-repo", aliceID); r != userstore.RoleOwner {
		t.Errorf("alice's role in alice-repo = %q, want owner", r)
	}

	// An existing repo with no owner is NOT claimed through this deprecated
	// route (orphans are claimed through POST /v1/repos).
	if err := rig.store.Init("orphan"); err != nil {
		t.Fatalf("init orphan: %v", err)
	}
	wp02WantRepoExists(t, "bob creates orphan", create("orphan", bob))
	if r := wp02Role(t, rig, "orphan", bobID); r != "" {
		t.Errorf("bob's role in orphan = %q, want none", r)
	}
	if owners, err := rig.users.RepoOwners("orphan"); err != nil || len(owners) != 0 {
		t.Errorf("orphan owners = %v (err %v), want none", owners, err)
	}

	// A new repo is created with the caller as owner.
	wantCreated("bob creates bob-new", create("bob-new", bob), "bob-new")
	if r := wp02Role(t, rig, "bob-new", bobID); r != userstore.RoleOwner || !rig.store.Exists("bob-new") {
		t.Errorf("bob-new: bob's role %q exists=%v, want owner on disk", r, rig.store.Exists("bob-new"))
	}

	// The legacy key re-inits an existing repo without touching membership.
	wantCreated("legacy key on alice-repo", create("alice-repo", "legacy-key"), "alice-repo")

	// A non-owner member re-creating is a 201 that leaves the role alone.
	if err := rig.users.AddMember("alice-repo", bobID, userstore.RoleMember); err != nil {
		t.Fatalf("add bob as member: %v", err)
	}
	wantCreated("member bob re-creates alice-repo", create("alice-repo", bob), "alice-repo")
	if r := wp02Role(t, rig, "alice-repo", bobID); r != userstore.RoleMember {
		t.Errorf("bob's role in alice-repo = %q, want member", r)
	}
	if owners, err := rig.users.RepoOwners("alice-repo"); err != nil || len(owners) != 1 || owners[0] != aliceID {
		t.Errorf("alice-repo owners = %v (err %v), want only alice %s", owners, err, aliceID)
	}
}

// TestCreateRepoLegacy_ConcurrentCreateOneOwner: two users creating the same
// new repo at once must not both become its owner. Checking Exists and then
// calling Init let every racer see "new" and add itself as owner; only the
// caller that Create reports as the creator may.
func TestCreateRepoLegacy_ConcurrentCreateOneOwner(t *testing.T) {
	rig := setupRig(t)
	wp02WireLegacyCreate(rig)
	alice := signIn(t, rig.router, "alice-token")
	bob := signIn(t, rig.router, "bob-token")
	ids := map[string]string{
		alice: wp02UserID(t, rig, "alice@example.com"),
		bob:   wp02UserID(t, rig, "bob@example.com"),
	}

	for i := 0; i < 20; i++ {
		repo := fmt.Sprintf("race-create-%02d", i)
		got := map[string]*httptest.ResponseRecorder{}
		var mu gosync.Mutex
		create := func(bearer string) func() {
			return func() {
				w := doJSON(t, rig.router, http.MethodPost, "/v1/repos/"+repo, bearer, nil)
				mu.Lock()
				got[bearer] = w
				mu.Unlock()
			}
		}
		wp02Race(create(alice), create(bob))

		owners, err := rig.users.RepoOwners(repo)
		if err != nil || len(owners) != 1 {
			t.Errorf("%s: owners %v (err %v), want exactly one; alice %d %s, bob %d %s", repo, owners, err,
				got[alice].Code, got[alice].Body.String(), got[bob].Code, got[bob].Body.String())
			continue
		}
		for bearer, uid := range ids {
			w := got[bearer]
			if uid == owners[0] {
				wp02WantLegacyCreated(t, repo+" winner", w, repo)
				continue
			}
			wp02WantRepoExists(t, repo+" loser", w)
			if r := wp02Role(t, rig, repo, uid); r != "" {
				t.Errorf("%s: the loser has role %q", repo, r)
			}
		}
	}
}

// TestCreateRepoLegacy_ExistingDottedRepoMember pins the compatibility
// ruling: an EXISTING repo is never held to the id pattern. A self-host or
// hosted data root may hold a repo (and its membership rows) under an id
// such as "team.notes" from before the pattern; its member keeps getting the
// idempotent 201, while a non-member still gets 409.
func TestCreateRepoLegacy_ExistingDottedRepoMember(t *testing.T) {
	rig := setupRig(t)
	wp02WireLegacyCreate(rig)
	alice := signIn(t, rig.router, "alice-token")
	bob := signIn(t, rig.router, "bob-token")
	aliceID := wp02UserID(t, rig, "alice@example.com")
	bobID := wp02UserID(t, rig, "bob@example.com")
	if err := rig.store.Init("team.notes"); err != nil {
		t.Fatalf("init team.notes: %v", err)
	}
	if err := rig.users.AddMember("team.notes", aliceID, userstore.RoleOwner); err != nil {
		t.Fatalf("alice owns team.notes: %v", err)
	}

	wp02WantLegacyCreated(t, "member alice re-creates team.notes", wp02LegacyCreate(t, rig, "team.notes", alice), "team.notes")
	if members, err := rig.users.ListRepoMembers("team.notes"); err != nil || len(members) != 1 || members[0].UserID != aliceID || members[0].Role != userstore.RoleOwner {
		t.Errorf("team.notes members = %+v (err %v), want only alice as owner", members, err)
	}

	wp02WantRepoExists(t, "non-member bob creates team.notes", wp02LegacyCreate(t, rig, "team.notes", bob))
	if r := wp02Role(t, rig, "team.notes", bobID); r != "" {
		t.Errorf("bob's role in team.notes = %q, want none", r)
	}
}

// TestPush_ExistingDottedRepoStillWorks pins the compatibility ruling for
// push: an EXISTING repo goes straight to the membership check whatever its
// raw id, so a repo created under "team.notes" before the id pattern keeps
// taking its member's pushes and keeps refusing everyone else's.
func TestPush_ExistingDottedRepoStillWorks(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	alice := signIn(t, rig.router, "alice-token")
	bob := signIn(t, rig.router, "bob-token")
	aliceID := wp02UserID(t, rig, "alice@example.com")
	bobID := wp02UserID(t, rig, "bob@example.com")
	if err := rig.store.Init("team.notes"); err != nil {
		t.Fatalf("init team.notes: %v", err)
	}
	if err := rig.users.AddMember("team.notes", aliceID, userstore.RoleOwner); err != nil {
		t.Fatalf("alice owns team.notes: %v", err)
	}

	w, resp := wp02Push(t, rig.router, "team.notes", alice, "", wp02PushFile{Path: "wiki/a.md", Content: "dotted"})
	if w.Code != http.StatusOK {
		t.Fatalf("member alice pushes to team.notes: %d %s, want 200", w.Code, w.Body.String())
	}
	if content, sha, err := rig.store.Read("team.notes", "wiki/a.md"); err != nil || string(content) != "dotted" || sha != wp02PushedSHA(resp, "wiki/a.md") {
		t.Errorf("team.notes wiki/a.md = %q@%s (err %v), want dotted@%s", content, sha, err, wp02PushedSHA(resp, "wiki/a.md"))
	}

	w, _ = wp02Push(t, rig.router, "team.notes", bob, "", wp02PushFile{Path: "wiki/b.md", Content: "intruder"})
	if w.Code != http.StatusForbidden {
		t.Errorf("non-member bob pushes to team.notes: %d %s, want 403", w.Code, w.Body.String())
	}
	if _, _, err := rig.store.Read("team.notes", "wiki/b.md"); err == nil {
		t.Errorf("a refused push wrote wiki/b.md")
	}
	if r := wp02Role(t, rig, "team.notes", bobID); r != "" {
		t.Errorf("bob's role in team.notes = %q, want none", r)
	}
}

// TestPush_NewRepoIDMustBeCanonical: a signed-in user's first push to a new
// id outside the pattern used to create the sanitized directory ("my.repo"
// -> myrepo) under an owner row for the raw id. Whoever later created
// "myrepo" through POST /v1/repos claimed it as an orphan, while the first
// pusher kept access through the alias. Now it is a 400 before any write.
func TestPush_NewRepoIDMustBeCanonical(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	alice := signIn(t, rig.router, "alice-token")
	bob := signIn(t, rig.router, "bob-token")
	aliceID := wp02UserID(t, rig, "alice@example.com")

	for _, repo := range []string{"my.repo", "my-repo.", "my.-repo"} {
		w, _ := wp02Push(t, rig.router, repo, alice, "", wp02PushFile{Path: "wiki/a.md", Content: "squat"})
		wp02WantRepoIDRule(t, "alice pushes to new "+repo, w)
		if rig.store.Exists(repo) {
			t.Errorf("a refused push to %q left a repo on disk", repo)
		}
		if r := wp02Role(t, rig, repo, aliceID); r != "" {
			t.Errorf("a refused push to %q gave alice role %q", repo, r)
		}
	}
	if repos, err := rig.store.ListRepos(); err != nil || len(repos) != 0 {
		t.Errorf("repos on disk = %v (err %v), want none", repos, err)
	}
	if ms, err := rig.users.ListUserRepos(aliceID); err != nil || len(ms) != 0 {
		t.Errorf("alice's memberships = %+v (err %v), want none", ms, err)
	}

	// The victim's canonical repo is then really theirs: alice has no way in
	// through the alias she tried.
	if w := doJSON(t, rig.router, http.MethodPost, "/v1/repos", bob, map[string]string{"id": "myrepo"}); w.Code != http.StatusCreated {
		t.Fatalf("bob creates myrepo: %d %s", w.Code, w.Body.String())
	}
	if w, _ := wp02Push(t, rig.router, "my.repo", alice, "", wp02PushFile{Path: "wiki/a.md", Content: "squat"}); w.Code != http.StatusForbidden {
		t.Errorf("alice pushes to bob's myrepo through my.repo: %d %s, want 403", w.Code, w.Body.String())
	}

	// The legacy key keeps today's behaviour: any id, created on first push.
	if w, _ := wp02Push(t, rig.router, "legacy.repo", "legacy-key", "", wp02PushFile{Path: "wiki/a.md", Content: "legacy"}); w.Code != http.StatusOK {
		t.Errorf("legacy key pushes to new legacy.repo: %d %s, want 200", w.Code, w.Body.String())
	}
	if !rig.store.Exists("legacy.repo") {
		t.Errorf("legacy.repo was not created")
	}
}

// TestPush_ConcurrentFirstPushOneOwner: two users' first pushes to the same
// new repo, at once. Checking Exists and then calling Init let both see "new",
// both become owner and both commit. Now only the push that created the repo
// is its owner and commits; the other is not a member (403) and writes
// nothing.
func TestPush_ConcurrentFirstPushOneOwner(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	alice := signIn(t, rig.router, "alice-token")
	bob := signIn(t, rig.router, "bob-token")
	type pusher struct{ bearer, uid, page string }
	pushers := []pusher{
		{alice, wp02UserID(t, rig, "alice@example.com"), "wiki/alice.md"},
		{bob, wp02UserID(t, rig, "bob@example.com"), "wiki/bob.md"},
	}

	for i := 0; i < 20; i++ {
		repo := fmt.Sprintf("race-push-%02d", i)
		got := make([]*httptest.ResponseRecorder, len(pushers))
		fns := make([]func(), len(pushers))
		for j, p := range pushers {
			body := map[string]any{"message": "first", "files": []wp02PushFile{{Path: p.page, Content: p.page}}}
			fns[j] = func() {
				got[j] = doJSON(t, rig.router, http.MethodPost, "/v1/repos/"+repo+"/sync/push", p.bearer, body)
			}
		}
		wp02Race(fns...)

		owners, err := rig.users.RepoOwners(repo)
		if err != nil || len(owners) != 1 {
			t.Errorf("%s: owners %v (err %v), want exactly one; alice %d %s, bob %d %s", repo, owners, err,
				got[0].Code, got[0].Body.String(), got[1].Code, got[1].Body.String())
			continue
		}
		for j, p := range pushers {
			w := got[j]
			_, _, readErr := rig.store.Read(repo, p.page)
			if p.uid == owners[0] {
				var resp sync.PushResponse
				_ = json.Unmarshal(w.Body.Bytes(), &resp)
				if w.Code != http.StatusOK || readErr != nil || wp02PushedSHA(resp, p.page) == "" {
					t.Errorf("%s: winner %s: %d %s, page read err %v, want 200 and the page committed", repo, p.page, w.Code, w.Body.String(), readErr)
				}
				continue
			}
			if w.Code != http.StatusForbidden {
				t.Errorf("%s: loser %s: %d %s, want 403", repo, p.page, w.Code, w.Body.String())
			}
			if readErr == nil {
				t.Errorf("%s: the loser's %s was committed", repo, p.page)
			}
			if r := wp02Role(t, rig, repo, p.uid); r != "" {
				t.Errorf("%s: the loser has role %q", repo, r)
			}
		}
	}
}

// TestPush_FailedCreateLeavesNoOwnerRow: an owner row exists only for a repo
// its owner actually created. A row left for an id nobody created would grant
// access to whoever creates that id later.
func TestPush_FailedCreateLeavesNoOwnerRow(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	alice := signIn(t, rig.router, "alice-token")
	aliceID := wp02UserID(t, rig, "alice@example.com")
	// A plain file where the repo directory would go makes creation fail.
	if err := os.WriteFile(filepath.Join(rig.store.Root, "blocked"), []byte("x"), 0o644); err != nil {
		t.Fatalf("block repo dir: %v", err)
	}
	w, _ := wp02Push(t, rig.router, "blocked", alice, "", wp02PushFile{Path: "wiki/a.md", Content: "a"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("push into an uncreatable repo: %d %s, want 500", w.Code, w.Body.String())
	}
	if r := wp02Role(t, rig, "blocked", aliceID); r != "" {
		t.Errorf("failed creation left alice with role %q on 'blocked'", r)
	}
}

// TestPush_StaleRowsPurgedOnCreate: membership rows for an id that has no
// repo (left by a crash between adding a creator's owner row and creating the
// repo, or by an operator deleting a repo directory) must not grant access to
// the repo whoever creates that id next.
func TestPush_StaleRowsPurgedOnCreate(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	alice := signIn(t, rig.router, "alice-token")
	bob := signIn(t, rig.router, "bob-token")
	aliceID := wp02UserID(t, rig, "alice@example.com")
	bobID := wp02UserID(t, rig, "bob@example.com")
	if err := rig.users.AddMember("ghost", bobID, userstore.RoleOwner); err != nil {
		t.Fatalf("seed stale row: %v", err)
	}

	w, _ := wp02Push(t, rig.router, "ghost", alice, "", wp02PushFile{Path: "wiki/secret.md", Content: "alice only"})
	if w.Code != http.StatusOK {
		t.Fatalf("alice's first push: %d %s", w.Code, w.Body.String())
	}
	if owners, err := rig.users.RepoOwners("ghost"); err != nil || len(owners) != 1 || owners[0] != aliceID {
		t.Errorf("owners %v (err %v), want only alice", owners, err)
	}
	if r := wp02Role(t, rig, "ghost", bobID); r != "" {
		t.Errorf("bob kept role %q from the stale row", r)
	}
	if w := doGet(t, rig.router, "/v1/repos/ghost/pages/wiki/secret.md", bob, nil); w.Code != http.StatusForbidden {
		t.Errorf("bob reads alice's page: %d, want 403", w.Code)
	}
}

// TestPush_SameUserConcurrentFirstPushAllSucceed: one user's parallel first
// pushes to a new repo (the editor extension and an agent, say) must all
// succeed. createOwnedRepo used to create the repo before adding the owner
// row, so a push that lost the create race could check membership in between
// and get 403 from its own owner's repo. Now the owner row exists before the
// repo does, and repo creation is serialized.
func TestPush_SameUserConcurrentFirstPushAllSucceed(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	wp02WireLegacyCreate(rig)
	alice := signIn(t, rig.router, "alice-token")
	aliceID := wp02UserID(t, rig, "alice@example.com")

	for i := 0; i < 20; i++ {
		repo := fmt.Sprintf("same-user-%02d", i)
		const n = 3
		got := make([]*httptest.ResponseRecorder, n+1)
		fns := make([]func(), n+1)
		for j := 0; j < n; j++ {
			page := fmt.Sprintf("wiki/p%d.md", j)
			body := map[string]any{"message": "first", "files": []wp02PushFile{{Path: page, Content: page}}}
			fns[j] = func() {
				got[j] = doJSON(t, rig.router, http.MethodPost, "/v1/repos/"+repo+"/sync/push", alice, body)
			}
		}
		fns[n] = func() {
			got[n] = doJSON(t, rig.router, http.MethodPost, "/v1/repos/"+repo, alice, nil)
		}
		wp02Race(fns...)

		for j := 0; j < n; j++ {
			if got[j].Code != http.StatusOK {
				t.Errorf("%s: push %d: %d %s, want 200", repo, j, got[j].Code, got[j].Body.String())
			}
		}
		if got[n].Code != http.StatusCreated {
			t.Errorf("%s: legacy create: %d %s, want 201", repo, got[n].Code, got[n].Body.String())
		}
		if owners, err := rig.users.RepoOwners(repo); err != nil || len(owners) != 1 || owners[0] != aliceID {
			t.Errorf("%s: owners %v (err %v), want only alice", repo, owners, err)
		}
	}
}
