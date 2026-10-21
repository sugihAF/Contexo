package handler

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	sqlite "modernc.org/sqlite"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/userstore"
	"github.com/sugihAF/contexo/quota"
)

// wp07User is a signed-in test user: bearer token and userstore id.
type wp07User struct {
	bearer, id string
}

// wp07SignIn signs in the named users ("alice", "bob", "carol", "dave") in
// order and returns them by name; carol and dave are added to the rig's fake
// verifier first (setupRig only knows alice and bob). The first sign-in
// adopts every ownerless repo on disk, so every test signs its users in
// before it creates any repo.
func wp07SignIn(t *testing.T, rig *testRig, names ...string) map[string]wp07User {
	t.Helper()
	extra := map[string]*auth.GoogleClaims{
		"carol": {Email: "carol@example.com", Name: "Carol", Subject: "g3", EmailVerified: true},
		"dave":  {Email: "dave@example.com", Name: "Dave", Subject: "g4", EmailVerified: true},
	}
	out := make(map[string]wp07User, len(names))
	for _, n := range names {
		if c, ok := extra[n]; ok {
			rig.verifier[n+"-token"] = c
		}
		bearer := signIn(t, rig.router, n+"-token")
		out[n] = wp07User{bearer: bearer, id: wp02UserID(t, rig, n+"@example.com")}
	}
	return out
}

// wp07Create is the dashboard's create: POST /v1/repos {"id": id}.
func wp07Create(t *testing.T, rig *testRig, id, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, rig.router, http.MethodPost, "/v1/repos", bearer, map[string]string{"id": id})
}

// wp07FoldsCase reports whether the filesystem holding dir is
// case-insensitive (macOS and Windows by default; Linux is not). It creates
// and removes a probe directory inside dir.
func wp07FoldsCase(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "wp07CaseProbe")
	if err := os.Mkdir(probe, 0o755); err != nil {
		t.Fatalf("case probe: %v", err)
	}
	defer os.Remove(probe)
	_, err := os.Stat(filepath.Join(dir, "WP07CASEPROBE"))
	return err == nil
}

// wp07WantCreated asserts the dashboard's 201 body, byte for byte.
func wp07WantCreated(t *testing.T, label string, w *httptest.ResponseRecorder, id string) {
	t.Helper()
	want := `{"id":"` + id + `","role":"owner"}`
	if w.Code != http.StatusCreated || w.Body.String() != want {
		t.Errorf("%s: %d %s, want 201 %s", label, w.Code, w.Body.String(), want)
	}
}

// wp07WantRepoExists asserts the 409 {"error":"repo already exists"}, byte
// for byte.
func wp07WantRepoExists(t *testing.T, label string, w *httptest.ResponseRecorder) {
	t.Helper()
	const want = `{"error":"repo already exists"}`
	if w.Code != http.StatusConflict || w.Body.String() != want {
		t.Errorf("%s: %d %s, want 409 %s", label, w.Code, w.Body.String(), want)
	}
}

// wp07WantStatus asserts a status code.
func wp07WantStatus(t *testing.T, label string, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Errorf("%s: %d %s, want %d", label, w.Code, w.Body.String(), status)
	}
}

// wp07WantMembers asserts that repo's membership rows are exactly want
// (user id -> role).
func wp07WantMembers(t *testing.T, label string, rig *testRig, repo string, want map[string]string) {
	t.Helper()
	ms, err := rig.users.ListRepoMembers(repo)
	if err != nil {
		t.Errorf("%s: members of %q: %v", label, repo, err)
		return
	}
	got := make(map[string]string, len(ms))
	for _, m := range ms {
		got[m.UserID] = m.Role
	}
	if len(ms) != len(want) || !maps.Equal(got, want) {
		t.Errorf("%s: members of %q = %+v, want %v", label, repo, ms, want)
	}
}

// wp07WantKeys asserts how many invite keys repo has.
func wp07WantKeys(t *testing.T, label string, rig *testRig, repo string, n int) {
	t.Helper()
	keys, err := rig.users.ListInviteKeys(repo)
	if err != nil || len(keys) != n {
		t.Errorf("%s: %q has keys %+v (err %v), want %d", label, repo, keys, err, n)
	}
}

// wp07WantResolves asserts that the raw invite key resolves to repo, or,
// with repo "", that it is unknown.
func wp07WantResolves(t *testing.T, label string, rig *testRig, raw, repo string) {
	t.Helper()
	got, err := rig.users.ResolveInviteKey(raw)
	if repo == "" {
		if !errors.Is(err, userstore.ErrNotFound) {
			t.Errorf("%s: key resolves to %q (err %v), want ErrNotFound", label, got, err)
		}
		return
	}
	if err != nil || got != repo {
		t.Errorf("%s: key resolves to %q (err %v), want %q", label, got, err, repo)
	}
}

// wp07WantActivity asserts how many activity rows repo has.
func wp07WantActivity(t *testing.T, label string, rig *testRig, repo string, n int) {
	t.Helper()
	got, err := rig.users.CountActivity(repo)
	if err != nil || got != n {
		t.Errorf("%s: %q has %d activity rows (err %v), want %d", label, repo, got, err, n)
	}
}

// wp07Mint mints an invite key for repo over HTTP as bearer and returns the
// raw key.
func wp07Mint(t *testing.T, rig *testRig, repo, bearer string) string {
	t.Helper()
	w := doJSON(t, rig.router, http.MethodPost, "/v1/repos/"+repo+"/invite-keys", bearer, map[string]string{"label": "wp07"})
	var body struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusCreated || body.Token == "" {
		t.Fatalf("mint a key for %q: %d %s", repo, w.Code, w.Body.String())
	}
	return body.Token
}

// wp07Join is POST /v1/repos/join {"key": key} as bearer.
func wp07Join(t *testing.T, rig *testRig, key, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, rig.router, http.MethodPost, "/v1/repos/join", bearer, map[string]string{"key": key})
}

// wp07Get is an authenticated GET.
func wp07Get(t *testing.T, rig *testRig, path, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(t, rig.router, http.MethodGet, path, bearer, nil)
}

// wp07HideTable renames a userstore table so every query that reads it
// fails, and renames it back when the test ends (or when the returned func
// is called).
func wp07HideTable(t *testing.T, rig *testRig, table string) (restore func()) {
	t.Helper()
	hidden := "wp07_hidden_" + table
	if _, err := rig.users.DB().Exec(`ALTER TABLE ` + table + ` RENAME TO ` + hidden); err != nil {
		t.Fatalf("hide %s: %v", table, err)
	}
	restored := false
	restore = func() {
		if restored {
			return
		}
		restored = true
		if _, err := rig.users.DB().Exec(`ALTER TABLE ` + hidden + ` RENAME TO ` + table); err != nil {
			t.Errorf("restore %s: %v", table, err)
		}
	}
	t.Cleanup(restore)
	return restore
}

// wp07QuotaHook is a quota.Policy that calls hook from AllowRepoCreate and
// answers err (nil allows every create). A create checks the quota under
// createRepoMu, after its existence checks and before gitstore.Create, so
// hook runs at that point of the critical section, on the request's
// goroutine.
type wp07QuotaHook struct {
	hook func()
	err  error
}

func (q wp07QuotaHook) AllowRepoCreate(string, int) error {
	q.hook()
	return q.err
}

func (wp07QuotaHook) AllowMemberAdd(string, []string, int) error { return nil }

// wp07LockProbeFunc names the SQL function that reports whether createRepoMu
// is held when SQLite calls it: 1 when it is, else 0. A trigger calls it in
// the middle of the statement that fires the trigger, on the goroutine that
// runs that statement. It is registered with the driver at init, before any
// test opens a userstore, so every connection of every rig has it.
const wp07LockProbeFunc = "wp07_create_repo_mu_held"

func init() {
	sqlite.MustRegisterScalarFunction(wp07LockProbeFunc, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		if createRepoMu.TryLock() {
			createRepoMu.Unlock()
			return int64(0), nil
		}
		return int64(1), nil
	})
}

// wp07MustPush pushes one page as bearer and fails the test unless it is
// committed (200).
func wp07MustPush(t *testing.T, rig *testRig, repo, bearer, page, content string) {
	t.Helper()
	if w, _ := wp02Push(t, rig.router, repo, bearer, "", wp02PushFile{Path: page, Content: content}); w.Code != http.StatusOK {
		t.Fatalf("push %s to %q: %d %s", page, repo, w.Code, w.Body.String())
	}
}

// TestCreateRepoConcurrentSameIDOneOwner: POST /v1/repos checked Exists, ran
// Init and then added the caller as owner, with nothing serializing it, so
// concurrent creates of one new id each became an owner (or claimed the
// "orphan" another racer had just made). Exactly one request may create the
// repo; every other one gets 409 and no row.
func TestCreateRepoConcurrentSameIDOneOwner(t *testing.T) {
	rig := setupRig(t)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	idOf := map[string]string{}
	var callers []string
	for _, n := range []string{"alice", "bob", "carol", "dave"} {
		idOf[us[n].bearer] = us[n].id
		callers = append(callers, us[n].bearer, us[n].bearer)
	}

	race := func(repo string, callers []string) (winner string) {
		t.Helper()
		got := make([]*httptest.ResponseRecorder, len(callers))
		fns := make([]func(), len(callers))
		for i, bearer := range callers {
			fns[i] = func() { got[i] = wp07Create(t, rig, repo, bearer) }
		}
		wp02Race(fns...)
		created := 0
		for i, w := range got {
			if w.Code == http.StatusCreated {
				created++
				winner = idOf[callers[i]]
				wp07WantCreated(t, repo+" winner", w, repo)
				continue
			}
			wp07WantRepoExists(t, fmt.Sprintf("%s caller %d", repo, i), w)
		}
		if created != 1 {
			t.Errorf("%s: %d creates answered 201, want exactly 1", repo, created)
		}
		return winner
	}

	for i := 0; i < 5; i++ {
		repo := fmt.Sprintf("race-dash-%02d", i)
		winner := race(repo, callers)
		if owners, err := rig.users.RepoOwners(repo); err != nil || len(owners) != 1 || owners[0] != winner {
			t.Errorf("%s: owners %v (err %v), want only the 201 caller %s", repo, owners, err, winner)
		}
		if ms, err := rig.users.ListRepoMembers(repo); err != nil || len(ms) != 1 {
			t.Errorf("%s: members %+v (err %v), want exactly one row", repo, ms, err)
		}
	}

	// One user creating the same new repo four times at once (a double
	// click, two tabs): one 201, the rest 409, one owner row.
	alice := us["alice"]
	race("race-dash-same", []string{alice.bearer, alice.bearer, alice.bearer, alice.bearer})
	wp07WantMembers(t, "same user", rig, "race-dash-same", map[string]string{alice.id: userstore.RoleOwner})
}

// TestCreateRepoConcurrentMixedRoutesOneOwner: POST /v1/repos racing the
// other create routes (a signed-in first push, the deprecated POST
// /v1/repos/:id, and a legacy-key push, which creates without an owner).
// At most one request owns the new repo, and only the one that created it.
func TestCreateRepoConcurrentMixedRoutesOneOwner(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	wp02WireLegacyCreate(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol")
	alice, bob, carol := us["alice"], us["bob"], us["carol"]

	for i := 0; i < 10; i++ {
		repo := fmt.Sprintf("race-mixed-%02d", i)
		// Stagger alice's create (0-16ms), and for the second half of the
		// ids the legacy-key push (10ms), so her request lands before
		// another route's creation, inside it (repo on disk, owner row not
		// yet added) or after it. The rules below hold for every order.
		aliceDelay := time.Duration(i%5) * 4 * time.Millisecond
		keyDelay := time.Duration(i/5) * 10 * time.Millisecond
		var create, push, legacy, keyPush *httptest.ResponseRecorder
		wp02Race(
			func() {
				time.Sleep(aliceDelay)
				create = wp07Create(t, rig, repo, alice.bearer)
			},
			func() {
				push, _ = wp02Push(t, rig.router, repo, bob.bearer, "", wp02PushFile{Path: "wiki/bob.md", Content: "bob"})
			},
			func() { legacy = wp02LegacyCreate(t, rig, repo, carol.bearer) },
			func() {
				time.Sleep(keyDelay)
				keyPush, _ = wp02Push(t, rig.router, repo, "legacy-key", "", wp02PushFile{Path: "wiki/key.md", Content: "key"})
			},
		)

		wp07WantStatus(t, repo+": legacy-key push", keyPush, http.StatusOK)
		owners, err := rig.users.RepoOwners(repo)
		if err != nil || len(owners) > 1 {
			t.Errorf("%s: owners %v (err %v), want at most one; create %d %s, push %d %s, legacy %d %s", repo, owners, err,
				create.Code, create.Body.String(), push.Code, push.Body.String(), legacy.Code, legacy.Body.String())
			continue
		}
		owner := ""
		if len(owners) == 1 {
			owner = owners[0]
		}
		if ms, err := rig.users.ListRepoMembers(repo); err != nil || len(ms) != len(owners) {
			t.Errorf("%s: members %+v (err %v), want only the owner %v", repo, ms, err, owners)
		}
		if owner == alice.id {
			wp07WantCreated(t, repo+": winning POST /v1/repos", create, repo)
		} else {
			wp07WantRepoExists(t, repo+": losing POST /v1/repos", create)
		}
		_, _, bobPageErr := rig.store.Read(repo, "wiki/bob.md")
		if owner == bob.id {
			if push.Code != http.StatusOK || bobPageErr != nil {
				t.Errorf("%s: winning push: %d %s (page err %v), want 200 and the page committed", repo, push.Code, push.Body.String(), bobPageErr)
			}
		} else {
			wp07WantStatus(t, repo+": losing push", push, http.StatusForbidden)
			if bobPageErr == nil {
				t.Errorf("%s: the losing push committed its page", repo)
			}
		}
		if owner == carol.id {
			wp02WantLegacyCreated(t, repo+": winning legacy create", legacy, repo)
		} else {
			wp02WantRepoExists(t, repo+": losing legacy create", legacy)
		}
	}
}

// TestCreateRepoNeverClaimsExistingRepo: POST /v1/repos used to adopt any
// existing repo without an owner row ("orphan claim"): a repo the legacy key
// created, or one whose owner row had not been added yet. It is now a 409
// for every existing repo, whoever asks, and nothing changes.
func TestCreateRepoNeverClaimsExistingRepo(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol")
	alice, bob, carol := us["alice"], us["bob"], us["carol"]

	// (a) An ownerless repo on disk (created after the first sign-in, so
	// nobody adopted it).
	if err := rig.store.Init("orphan"); err != nil {
		t.Fatalf("init orphan: %v", err)
	}
	wp07WantRepoExists(t, "bob creates orphan", wp07Create(t, rig, "orphan", bob.bearer))
	wp07WantMembers(t, "orphan", rig, "orphan", map[string]string{})

	// (b) A repo the legacy key created by pushing.
	wp07MustPush(t, rig, "key-repo", "legacy-key", "wiki/a.md", "legacy")
	wp07WantRepoExists(t, "bob creates key-repo", wp07Create(t, rig, "key-repo", bob.bearer))
	wp07WantMembers(t, "key-repo", rig, "key-repo", map[string]string{})

	// (c) alice's repo with a member, two keys and activity.
	wp07WantCreated(t, "alice creates alice-repo", wp07Create(t, rig, "alice-repo", alice.bearer), "alice-repo")
	k1 := wp07Mint(t, rig, "alice-repo", alice.bearer)
	wp07WantStatus(t, "bob joins alice-repo", wp07Join(t, rig, k1, bob.bearer), http.StatusOK)
	k2 := wp07Mint(t, rig, "alice-repo", alice.bearer)
	wp07MustPush(t, rig, "alice-repo", alice.bearer, "wiki/secret.md", "alice only")
	activity, err := rig.users.CountActivity("alice-repo")
	if err != nil || activity == 0 {
		t.Fatalf("alice-repo activity = %d (err %v), want some", activity, err)
	}
	unchanged := func(label string) {
		t.Helper()
		wp07WantMembers(t, label, rig, "alice-repo", map[string]string{alice.id: userstore.RoleOwner, bob.id: userstore.RoleMember})
		wp07WantKeys(t, label, rig, "alice-repo", 2)
		wp07WantResolves(t, label+": k1", rig, k1, "alice-repo")
		wp07WantResolves(t, label+": k2", rig, k2, "alice-repo")
		wp07WantActivity(t, label, rig, "alice-repo", activity)
		if r := wp02Role(t, rig, "alice-repo", carol.id); r != "" {
			t.Errorf("%s: carol has role %q in alice-repo", label, r)
		}
	}
	wp07WantRepoExists(t, "carol creates alice-repo", wp07Create(t, rig, "alice-repo", carol.bearer))
	unchanged("after carol's create")
	wp07WantStatus(t, "carol reads alice's page", wp07Get(t, rig, "/v1/repos/alice-repo/pages/wiki/secret.md", carol.bearer), http.StatusForbidden)
	wp07WantRepoExists(t, "alice re-creates alice-repo", wp07Create(t, rig, "alice-repo", alice.bearer))
	unchanged("after alice's create")
	wp07WantRepoExists(t, "member bob creates alice-repo", wp07Create(t, rig, "alice-repo", bob.bearer))
	unchanged("after bob's create")
}

// TestCreateRepoAliasOfExistingRefused: gitstore keeps only the
// [A-Za-z0-9_-] characters of an id, and a case-insensitive filesystem
// (macOS, Windows self-host) folds case, so several ids can name one
// directory while membership rows use the raw id. POST /v1/repos must never
// make a caller the owner of another id's directory.
func TestCreateRepoAliasOfExistingRefused(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "bob")
	alice, bob := us["alice"], us["bob"]

	// (a) A dotted repo from before the id pattern: "my.repo" lives in
	// directory "myrepo", so the canonical id "myrepo" is its alias.
	wp07MustPush(t, rig, "my.repo", "legacy-key", "wiki/a.md", "dotted secret")
	if err := rig.users.AddMember("my.repo", alice.id, userstore.RoleOwner); err != nil {
		t.Fatalf("alice owns my.repo: %v", err)
	}
	wp07WantRepoExists(t, "bob creates myrepo", wp07Create(t, rig, "myrepo", bob.bearer))
	wp07WantMembers(t, "myrepo", rig, "myrepo", map[string]string{})
	wp07WantStatus(t, "bob reads my.repo's page via myrepo", wp07Get(t, rig, "/v1/repos/myrepo/pages/wiki/a.md", bob.bearer), http.StatusForbidden)
	wp02WantRepoIDRule(t, "bob creates my.repo", wp07Create(t, rig, "my.repo", bob.bearer))
	wp07WantMembers(t, "my.repo", rig, "my.repo", map[string]string{alice.id: userstore.RoleOwner})
	wp07WantStatus(t, "alice reads her dotted repo", wp07Get(t, rig, "/v1/repos/my.repo/pages/wiki/a.md", alice.bearer), http.StatusOK)

	// (b) Non-canonical aliases of a canonical repo are refused by the id
	// pattern before anything else, and create nothing.
	wp07WantCreated(t, "alice creates alice-repo", wp07Create(t, rig, "alice-repo", alice.bearer), "alice-repo")
	before, err := rig.store.ListRepos()
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	for _, alias := range []string{"alice.-repo", "alice-repo.", "al.ice-repo"} {
		wp02WantRepoIDRule(t, "bob creates "+alias, wp07Create(t, rig, alias, bob.bearer))
		wp07WantMembers(t, alias, rig, alias, map[string]string{})
	}
	if after, err := rig.store.ListRepos(); err != nil || !slices.Equal(after, before) {
		t.Errorf("repos on disk %v (err %v), want unchanged %v", after, err, before)
	}
	wp07WantMembers(t, "alice-repo", rig, "alice-repo", map[string]string{alice.id: userstore.RoleOwner})

	// (c) Case variants.
	wp07WantCreated(t, "alice creates Case-Repo", wp07Create(t, rig, "Case-Repo", alice.bearer), "Case-Repo")
	wp07MustPush(t, rig, "Case-Repo", alice.bearer, "wiki/case.md", "alice case secret")
	if wp07FoldsCase(t, filepath.Dir(rig.store.Root)) {
		// One directory: every case variant is an existing repo.
		for _, v := range []string{"case-repo", "CASE-REPO"} {
			wp07WantRepoExists(t, "bob creates "+v, wp07Create(t, rig, v, bob.bearer))
			wp07WantMembers(t, v, rig, v, map[string]string{})
		}
		wp07WantStatus(t, "bob reads Case-Repo via case-repo", wp07Get(t, rig, "/v1/repos/case-repo/pages/wiki/case.md", bob.bearer), http.StatusForbidden)
	} else {
		// Case-sensitive: "case-repo" is a different, new repo.
		wp07WantCreated(t, "bob creates case-repo", wp07Create(t, rig, "case-repo", bob.bearer), "case-repo")
		wp07WantStatus(t, "bob reads Case-Repo", wp07Get(t, rig, "/v1/repos/Case-Repo/pages/wiki/case.md", bob.bearer), http.StatusForbidden)
		w := wp07Get(t, rig, "/v1/repos/case-repo/pages/wiki/case.md", bob.bearer)
		wp02WantError(t, "bob reads his own case-repo", w, http.StatusNotFound, "page_not_found")
		if strings.Contains(w.Body.String(), "alice case secret") {
			t.Errorf("bob read alice's page through his case-repo: %s", w.Body.String())
		}
	}
	wp07WantMembers(t, "Case-Repo", rig, "Case-Repo", map[string]string{alice.id: userstore.RoleOwner})
}

// TestCreateRepoPurgesStaleRowsOnRecreate: membership rows, invite keys and
// activity of a repo whose directory an operator deleted used to survive and
// grant access to whoever created that id next. A definitive creation now
// drops them before the new owner's row is added.
func TestCreateRepoPurgesStaleRowsOnRecreate(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol")
	alice, bob, carol := us["alice"], us["bob"], us["carol"]

	wp07WantCreated(t, "alice creates x", wp07Create(t, rig, "x", alice.bearer), "x")
	k1 := wp07Mint(t, rig, "x", alice.bearer)
	wp07WantStatus(t, "bob joins x", wp07Join(t, rig, k1, bob.bearer), http.StatusOK)
	k2 := wp07Mint(t, rig, "x", alice.bearer)
	wp07MustPush(t, rig, "x", alice.bearer, "wiki/old.md", "old content")
	if n, err := rig.users.CountActivity("x"); err != nil || n == 0 {
		t.Fatalf("x activity = %d (err %v), want some", n, err)
	}
	if err := os.RemoveAll(filepath.Join(rig.store.Root, "x")); err != nil {
		t.Fatalf("operator deletes x: %v", err)
	}

	wp07WantCreated(t, "carol creates x", wp07Create(t, rig, "x", carol.bearer), "x")
	wp07WantMembers(t, "x", rig, "x", map[string]string{carol.id: userstore.RoleOwner})
	wp07WantKeys(t, "x", rig, "x", 0)
	wp07WantResolves(t, "stale k2", rig, k2, "")
	wp07WantStatus(t, "bob joins with stale k2", wp07Join(t, rig, k2, bob.bearer), http.StatusNotFound)
	for name, u := range map[string]wp07User{"alice": alice, "bob": bob} {
		wp07WantStatus(t, name+" lists x", wp07Get(t, rig, "/v1/repos/x/pages", u.bearer), http.StatusForbidden)
		w, _ := wp02Push(t, rig.router, "x", u.bearer, "", wp02PushFile{Path: "wiki/" + name + ".md", Content: name})
		wp07WantStatus(t, name+" pushes to x", w, http.StatusForbidden)
	}
	if events, err := rig.users.ListActivity("x", 100, 0); err != nil || len(events) != 0 {
		t.Errorf("x activity = %+v (err %v), want none", events, err)
	}
	if n, err := rig.users.CountOwnedRepos(carol.id); err != nil || n != 1 {
		t.Errorf("carol owns %d repos (err %v), want 1", n, err)
	}

	// The former owner re-creating her own deleted repo starts clean too.
	wp07WantCreated(t, "alice creates y", wp07Create(t, rig, "y", alice.bearer), "y")
	ky := wp07Mint(t, rig, "y", alice.bearer)
	wp07WantStatus(t, "bob joins y", wp07Join(t, rig, ky, bob.bearer), http.StatusOK)
	if err := os.RemoveAll(filepath.Join(rig.store.Root, "y")); err != nil {
		t.Fatalf("operator deletes y: %v", err)
	}
	wp07WantCreated(t, "alice re-creates y", wp07Create(t, rig, "y", alice.bearer), "y")
	wp07WantMembers(t, "y", rig, "y", map[string]string{alice.id: userstore.RoleOwner})
}

// TestCreateRepoPurgesStaleAliasRowsOnRecreate: rows of another id that
// names the new repo's directory (a dotted id, or a case variant on a
// case-insensitive filesystem) are stale too once the directory is created
// anew, and must not reach the new repo. Rows of an id with its own live
// directory are never touched.
func TestCreateRepoPurgesStaleAliasRowsOnRecreate(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "carol", "dave")
	alice, carol, dave := us["alice"], us["carol"], us["dave"]
	folds := wp07FoldsCase(t, filepath.Dir(rig.store.Root))

	// (a) "my.repo" (directory myrepo) with an owner, a key and activity.
	wp07MustPush(t, rig, "my.repo", "legacy-key", "wiki/a.md", "dotted")
	if err := rig.users.AddMember("my.repo", alice.id, userstore.RoleOwner); err != nil {
		t.Fatalf("alice owns my.repo: %v", err)
	}
	_, raw, err := rig.users.MintInviteKey("my.repo", alice.id, "k")
	if err != nil {
		t.Fatalf("mint my.repo key: %v", err)
	}
	if err := rig.users.RecordActivity("my.repo", alice.id, "push", ""); err != nil {
		t.Fatalf("my.repo activity: %v", err)
	}
	// A live near-miss id ("my-repo" keeps its dash) must stay untouched.
	wp07WantCreated(t, "dave creates my-repo", wp07Create(t, rig, "my-repo", dave.bearer), "my-repo")
	_, daveKey, err := rig.users.MintInviteKey("my-repo", dave.id, "d")
	if err != nil {
		t.Fatalf("mint my-repo key: %v", err)
	}
	if err := rig.users.RecordActivity("my-repo", dave.id, "push", ""); err != nil {
		t.Fatalf("my-repo activity: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(rig.store.Root, "myrepo")); err != nil {
		t.Fatalf("operator deletes myrepo: %v", err)
	}

	wp07WantCreated(t, "carol creates myrepo", wp07Create(t, rig, "myrepo", carol.bearer), "myrepo")
	wp07MustPush(t, rig, "myrepo", carol.bearer, "wiki/secret.md", "carol secret")
	wp07WantStatus(t, "alice lists via my.repo", wp07Get(t, rig, "/v1/repos/my.repo/pages", alice.bearer), http.StatusForbidden)
	wp07WantStatus(t, "alice reads via my.repo", wp07Get(t, rig, "/v1/repos/my.repo/pages/wiki/secret.md", alice.bearer), http.StatusForbidden)
	w, _ := wp02Push(t, rig.router, "my.repo", alice.bearer, "", wp02PushFile{Path: "wiki/alice.md", Content: "alice"})
	wp07WantStatus(t, "alice pushes via my.repo", w, http.StatusForbidden)
	wp07WantResolves(t, "stale my.repo key", rig, raw, "")
	wp07WantMembers(t, "my.repo", rig, "my.repo", map[string]string{})
	if events, err := rig.users.ListActivity("my.repo", 100, 0); err != nil || len(events) != 0 {
		t.Errorf("my.repo activity = %+v (err %v), want none", events, err)
	}
	wp07WantMembers(t, "myrepo", rig, "myrepo", map[string]string{carol.id: userstore.RoleOwner})
	wp07WantMembers(t, "my-repo", rig, "my-repo", map[string]string{dave.id: userstore.RoleOwner})
	wp07WantKeys(t, "my-repo", rig, "my-repo", 1)
	wp07WantResolves(t, "my-repo key", rig, daveKey, "my-repo")
	wp07WantActivity(t, "my-repo", rig, "my-repo", 1)

	// (b) A deleted case variant: an alias only where the filesystem folds
	// case.
	wp07WantCreated(t, "alice creates Team-X", wp07Create(t, rig, "Team-X", alice.bearer), "Team-X")
	kx := wp07Mint(t, rig, "Team-X", alice.bearer)
	wp07MustPush(t, rig, "Team-X", alice.bearer, "wiki/x.md", "alice x")
	if err := os.RemoveAll(filepath.Join(rig.store.Root, "Team-X")); err != nil {
		t.Fatalf("operator deletes Team-X: %v", err)
	}
	wp07WantCreated(t, "carol creates team-x", wp07Create(t, rig, "team-x", carol.bearer), "team-x")
	if folds {
		wp07WantStatus(t, "alice lists via Team-X", wp07Get(t, rig, "/v1/repos/Team-X/pages", alice.bearer), http.StatusForbidden)
		wp07WantMembers(t, "Team-X", rig, "Team-X", map[string]string{})
		wp07WantKeys(t, "Team-X", rig, "Team-X", 0)
		wp07WantResolves(t, "stale Team-X key", rig, kx, "")
		wp07WantActivity(t, "Team-X", rig, "Team-X", 0)
	} else {
		wp07WantMembers(t, "Team-X", rig, "Team-X", map[string]string{alice.id: userstore.RoleOwner})
		wp07WantKeys(t, "Team-X", rig, "Team-X", 1)
		wp07WantResolves(t, "Team-X key", rig, kx, "Team-X")
	}
	wp07WantStatus(t, "alice lists team-x", wp07Get(t, rig, "/v1/repos/team-x/pages", alice.bearer), http.StatusForbidden)
	wp07WantMembers(t, "team-x", rig, "team-x", map[string]string{carol.id: userstore.RoleOwner})

	// (c) A LIVE case variant is never purged.
	wp07WantCreated(t, "alice creates Team-Y", wp07Create(t, rig, "Team-Y", alice.bearer), "Team-Y")
	ky := wp07Mint(t, rig, "Team-Y", alice.bearer)
	wp07MustPush(t, rig, "Team-Y", alice.bearer, "wiki/y.md", "alice y")
	if folds {
		wp07WantRepoExists(t, "carol creates team-y", wp07Create(t, rig, "team-y", carol.bearer))
		wp07WantMembers(t, "team-y", rig, "team-y", map[string]string{})
		wp07WantStatus(t, "carol reads via team-y", wp07Get(t, rig, "/v1/repos/team-y/pages/wiki/y.md", carol.bearer), http.StatusForbidden)
	} else {
		wp07WantCreated(t, "carol creates team-y", wp07Create(t, rig, "team-y", carol.bearer), "team-y")
	}
	wp07WantMembers(t, "Team-Y", rig, "Team-Y", map[string]string{alice.id: userstore.RoleOwner})
	wp07WantKeys(t, "Team-Y", rig, "Team-Y", 1)
	wp07WantResolves(t, "Team-Y key", rig, ky, "Team-Y")
	if w := wp07Get(t, rig, "/v1/repos/Team-Y/pages/wiki/y.md", alice.bearer); w.Code != http.StatusOK || w.Body.String() != "alice y" {
		t.Errorf("alice reads her Team-Y page: %d %q, want 200 %q", w.Code, w.Body.String(), "alice y")
	}
}

// TestCreateRepoDashboardShapeUnchanged pins what the dashboard
// (contexo-web createRepo) relies on: the 201 body and its content type, the
// listing, and every error status and body of POST /v1/repos.
func TestCreateRepoDashboardShapeUnchanged(t *testing.T) {
	rig := setupRig(t)
	alice := wp07SignIn(t, rig, "alice")["alice"]

	w := wp07Create(t, rig, "team-repo", alice.bearer)
	wp07WantCreated(t, "create", w, "team-repo")
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("create Content-Type %q, want application/json; charset=utf-8", ct)
	}
	w = wp07Get(t, rig, "/v1/repos", alice.bearer)
	var list struct {
		Repos []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"repos"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != http.StatusOK || len(list.Repos) != 1 ||
		list.Repos[0].ID != "team-repo" || list.Repos[0].Role != userstore.RoleOwner {
		t.Errorf("list: %d %s (err %v), want team-repo as owner", w.Code, w.Body.String(), err)
	}
	wp07WantRepoExists(t, "create again", wp07Create(t, rig, "team-repo", alice.bearer))

	wantBody := func(label string, w *httptest.ResponseRecorder, status int, body string) {
		t.Helper()
		if w.Code != status || w.Body.String() != body {
			t.Errorf("%s: %d %s, want %d %s", label, w.Code, w.Body.String(), status, body)
		}
	}
	const idRequired = `{"error":"id required"}`
	wantBody("{}", doJSON(t, rig.router, http.MethodPost, "/v1/repos", alice.bearer, map[string]string{}), http.StatusBadRequest, idRequired)
	wantBody(`{"id":""}`, wp07Create(t, rig, "", alice.bearer), http.StatusBadRequest, idRequired)
	req := httptest.NewRequest(http.MethodPost, "/v1/repos", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+alice.bearer)
	w = httptest.NewRecorder()
	rig.router.ServeHTTP(w, req)
	wantBody("invalid JSON", w, http.StatusBadRequest, idRequired)
	wantBody("bad.id", wp07Create(t, rig, "bad.id", alice.bearer), http.StatusBadRequest,
		`{"error":"id must match [A-Za-z0-9][A-Za-z0-9_-]{0,63}"}`)
	wantBody("legacy key", wp07Create(t, rig, "legacy-made", "legacy-key"), http.StatusForbidden, `{"error":"user session required"}`)

	rig.h.SetQuota(fakeQuota{maxRepos: 1, maxMembers: 99})
	w = wp07Create(t, rig, "second-repo", alice.bearer)
	var quotaBody struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &quotaBody)
	if w.Code != http.StatusPaymentRequired || quotaBody.Code != "quota_repos" {
		t.Errorf("over quota: %d %s, want 402 quota_repos", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(rig.store.Root, "second-repo")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused create left a directory (stat err %v)", err)
	}
}

// TestCreateRepoStatErrorNeverClaimsOrPurges: when the repo directory cannot
// be stat'ed (EACCES here; EMFILE or EIO in production), Exists reports false
// for a live repo. The create must fail and leave its rows alone: treating
// the repo as new would purge a live repo's members and keys.
func TestCreateRepoStatErrorNeverClaimsOrPurges(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions enforced for the test user")
	}
	rig := setupRig(t)
	us := wp07SignIn(t, rig, "alice", "bob", "carol")
	alice, bob, carol := us["alice"], us["bob"], us["carol"]
	wp07WantCreated(t, "alice creates team-repo", wp07Create(t, rig, "team-repo", alice.bearer), "team-repo")
	k1 := wp07Mint(t, rig, "team-repo", alice.bearer)
	wp07WantStatus(t, "bob joins", wp07Join(t, rig, k1, bob.bearer), http.StatusOK)
	k2 := wp07Mint(t, rig, "team-repo", alice.bearer)

	dir := filepath.Join(rig.store.Root, "team-repo")
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if rig.store.Exists("team-repo") {
		t.Skip("stat still succeeds under chmod 000 on this filesystem")
	}
	w := wp07Create(t, rig, "team-repo", carol.bearer)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod back: %v", err)
	}
	wp07WantStatus(t, "carol creates an unreadable team-repo", w, http.StatusInternalServerError)
	wp07WantMembers(t, "team-repo", rig, "team-repo", map[string]string{alice.id: userstore.RoleOwner, bob.id: userstore.RoleMember})
	wp07WantResolves(t, "k2", rig, k2, "team-repo")
}

// TestCreateRepoOwnerRowNotInsertedFailsClosed: if a row for the creator
// already exists when its owner row is inserted (it appeared after the
// purge), the create must not report an ownership it did not establish.
// A trigger re-adds the creator as a member while the stale rows are purged.
func TestCreateRepoOwnerRowNotInsertedFailsClosed(t *testing.T) {
	rig := setupRig(t)
	us := wp07SignIn(t, rig, "carol", "dave")
	carol, dave := us["carol"], us["dave"]
	// A stale row of an operator-deleted "z" (no directory).
	if err := rig.users.AddMember("z", dave.id, userstore.RoleMember); err != nil {
		t.Fatalf("seed z: %v", err)
	}
	trigger := fmt.Sprintf(`CREATE TRIGGER wp07_reinsert AFTER DELETE ON repo_members WHEN OLD.repo_id = 'z'
BEGIN INSERT OR IGNORE INTO repo_members (repo_id, user_id, role, added_at) VALUES ('z', '%s', 'member', 0); END`, carol.id)
	if _, err := rig.users.DB().Exec(trigger); err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	wp07WantStatus(t, "carol creates z", wp07Create(t, rig, "z", carol.bearer), http.StatusInternalServerError)
	if r := wp02Role(t, rig, "z", carol.id); r != userstore.RoleMember {
		t.Errorf("carol's role in z = %q, want member (never owner)", r)
	}
	if owners, err := rig.users.RepoOwners("z"); err != nil || len(owners) != 0 {
		t.Errorf("z owners %v (err %v), want none", owners, err)
	}
}

// TestCreateRepoPurgeErrorIs500: when the stale rows cannot be purged, the
// create fails before any owner row is added, and the purge is all or
// nothing across the new id and the aliases of its directory: one PurgeRepo
// call, so a failure on either id leaves the rows of both.
func TestCreateRepoPurgeErrorIs500(t *testing.T) {
	for _, tc := range []struct {
		create  string
		stale   []string // ids with stale rows: the new id, then its alias
		blocked string   // the id whose activity deletes fail
	}{
		{create: "w", stale: []string{"w"}, blocked: "w"},
		{create: "myw", stale: []string{"myw", "my.w"}, blocked: "myw"},
		{create: "myw", stale: []string{"myw", "my.w"}, blocked: "my.w"},
	} {
		t.Run(tc.create+"/blocked="+tc.blocked, func(t *testing.T) {
			rig := setupRig(t)
			us := wp07SignIn(t, rig, "carol", "dave")
			carol, dave := us["carol"], us["dave"]
			// Stale rows of operator-deleted repos (no directory).
			raws := map[string]string{}
			for _, id := range tc.stale {
				if err := rig.users.AddMember(id, dave.id, userstore.RoleOwner); err != nil {
					t.Fatalf("seed %s: %v", id, err)
				}
				_, raw, err := rig.users.MintInviteKey(id, dave.id, "k")
				if err != nil {
					t.Fatalf("seed %s key: %v", id, err)
				}
				raws[id] = raw
				if err := rig.users.RecordActivity(id, dave.id, "push", ""); err != nil {
					t.Fatalf("seed %s activity: %v", id, err)
				}
			}
			trigger := fmt.Sprintf(`CREATE TRIGGER wp07_block BEFORE DELETE ON repo_activity WHEN OLD.repo_id = '%s'
BEGIN SELECT RAISE(ABORT, 'blocked'); END`, tc.blocked)
			if _, err := rig.users.DB().Exec(trigger); err != nil {
				t.Fatalf("install trigger: %v", err)
			}

			wp07WantStatus(t, "carol creates "+tc.create, wp07Create(t, rig, tc.create, carol.bearer), http.StatusInternalServerError)
			if r := wp02Role(t, rig, tc.create, carol.id); r != "" {
				t.Errorf("carol's role in %s = %q, want none", tc.create, r)
			}
			for _, id := range tc.stale {
				wp07WantMembers(t, id+" (rolled back)", rig, id, map[string]string{dave.id: userstore.RoleOwner})
				wp07WantResolves(t, id+" key (rolled back)", rig, raws[id], id)
				wp07WantActivity(t, id+" (rolled back)", rig, id, 1)
			}
		})
	}
}

// TestCreateRepoListsKnownIDsOutsideCreateRepoMu: createRepoMu serializes
// every repo creation in the process (POST /v1/repos, push auto-create, the
// legacy create) and the re-check of a push by a non-member, so the listing
// of the known repo ids, a read of three tables, must not run while it is
// held, and an id that exists must be refused without waiting for it. The
// test holds createRepoMu: a create of a new id whose listing fails (a table
// it reads is hidden) and a create of an existing id must both answer.
func TestCreateRepoListsKnownIDsOutsideCreateRepoMu(t *testing.T) {
	rig := setupRig(t)
	us := wp07SignIn(t, rig, "alice", "carol")
	alice, carol := us["alice"], us["carol"]
	wp07WantCreated(t, "alice creates taken", wp07Create(t, rig, "taken", alice.bearer), "taken")
	restore := wp07HideTable(t, rig, "repo_activity")

	fresh := make(chan *httptest.ResponseRecorder, 1)
	taken := make(chan *httptest.ResponseRecorder, 1)
	var wFresh, wTaken *httptest.ResponseRecorder
	func() {
		createRepoMu.Lock()
		defer createRepoMu.Unlock()
		go func() { fresh <- wp07Create(t, rig, "fresh", carol.bearer) }()
		go func() { taken <- wp07Create(t, rig, "taken", carol.bearer) }()
		deadline := time.After(10 * time.Second)
		for wFresh == nil || wTaken == nil {
			select {
			case wFresh = <-fresh:
			case wTaken = <-taken:
			case <-deadline:
				return
			}
		}
	}()
	if wFresh == nil {
		t.Errorf("a create of a new id waited for createRepoMu before listing the known repo ids")
		wFresh = <-fresh
	}
	if wTaken == nil {
		t.Errorf("a create of an existing id waited for createRepoMu")
		wTaken = <-taken
	}
	restore()

	wp07WantStatus(t, "carol creates fresh, listing fails", wFresh, http.StatusInternalServerError)
	if _, err := os.Stat(filepath.Join(rig.store.Root, "fresh")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a create whose listing failed left a directory (stat err %v)", err)
	}
	wp07WantRepoExists(t, "carol creates taken", wTaken)
	wp07WantMembers(t, "taken", rig, "taken", map[string]string{alice.id: userstore.RoleOwner})
	if r := wp02Role(t, rig, "fresh", carol.id); r != "" {
		t.Errorf("carol's role in fresh = %q, want none", r)
	}
}

// wp07WaitsForLockIn reports whether some goroutine is blocked in a
// sync.Mutex Lock called directly from a function whose name contains fn.
// createRepoAsOwner takes one mutex itself, createRepoMu, so for fn
// "(*Handler).createRepoAsOwner" it tells a test that holds createRepoMu
// that a create has passed every step before the lock and is waiting for it.
func wp07WaitsForLockIn(fn string) bool {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	for n == len(buf) {
		buf = make([]byte, 2*len(buf))
		n = runtime.Stack(buf, true)
	}
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		// Function lines alternate with tab-indented file:line lines.
		var funcs []string
		for _, line := range strings.Split(g, "\n")[1:] {
			if line != "" && !strings.HasPrefix(line, "\t") {
				funcs = append(funcs, line)
			}
		}
		for i := 0; i+1 < len(funcs); i++ {
			if strings.HasPrefix(funcs[i], "sync.(*Mutex).Lock(") && strings.Contains(funcs[i+1], fn) {
				return true
			}
		}
	}
	return false
}

// TestCreateRepoRechecksExistenceUnderCreateRepoMu: a create checks whether
// the repo exists without createRepoMu, so refusing an existing id never
// waits, and must check again once it holds the lock. A repo made while it
// waited (by the create that held the lock, or by a creator outside it) is
// a 409 "repo already exists" before the quota is checked. Without the
// second check gitstore.Create's created=false still keeps the repo
// unclaimed, but a caller at their repo cap would get 402 quota_repos (the
// dashboard's upgrade prompt), and a failing owned-repo count a 500, for an
// id that exists. The test holds createRepoMu until carol's create of a new
// id waits for it, makes the repo, then lets the create go on, under a quota
// that refuses every create.
func TestCreateRepoRechecksExistenceUnderCreateRepoMu(t *testing.T) {
	rig := setupRig(t)
	carol := wp07SignIn(t, rig, "carol")["carol"]
	quotaCalls := 0
	rig.h.SetQuota(wp07QuotaHook{
		hook: func() { quotaCalls++ },
		err:  &quota.LimitError{Kind: "repos", Limit: 0, Message: "repo cap hit", UpgradeURL: "https://contexo.live/#pricing"},
	})

	done := make(chan *httptest.ResponseRecorder, 1)
	waited := func() bool {
		createRepoMu.Lock()
		defer createRepoMu.Unlock()
		go func() { done <- wp07Create(t, rig, "late", carol.bearer) }()
		for deadline := time.Now().Add(10 * time.Second); !wp07WaitsForLockIn("(*Handler).createRepoAsOwner"); time.Sleep(time.Millisecond) {
			if len(done) > 0 || time.Now().After(deadline) {
				return false
			}
		}
		if err := rig.store.Init("late"); err != nil {
			t.Errorf("init late: %v", err)
		}
		return true
	}()
	w := <-done
	if !waited {
		t.Fatalf("carol's create of a new id never waited for createRepoMu; it answered %d %s", w.Code, w.Body.String())
	}

	wp07WantRepoExists(t, "carol creates late, made while she waited", w)
	if quotaCalls != 0 {
		t.Errorf("the quota was checked %d times for an id that existed under createRepoMu, want 0", quotaCalls)
	}
	if r := wp02Role(t, rig, "late", carol.id); r != "" {
		t.Errorf("carol's role in late = %q, want none", r)
	}
	if owners, err := rig.users.RepoOwners("late"); err != nil || len(owners) != 0 {
		t.Errorf("late owners %v (err %v), want none", owners, err)
	}
}

// TestCreateRepoHoldsCreateRepoMu: gitstore.Create's created flag alone
// gives one owner per id, so the one-owner races pass without createRepoMu.
// What the lock adds is that the quota count, the creation, the purge and
// the owner row are one step for every other creation and for a push's
// re-check. The races that show it are timing-based:
// TestCreateRepoConcurrentDistinctIDsQuotaExact needs a second create to
// count its quota inside the window, and
// TestCreateRepoCreatorsPushWaitsForOwnerRow is deterministic only up to the
// purge (a release between the purge and the owner row lets the woken push
// race the insert, which the insert nearly always wins). Pinned directly and
// deterministically here, step by step: the quota hook, and triggers on the
// purge's deletes and on the owner row's insert, record whether createRepoMu
// is held when they run. A create that released it before the owner row
// exists would let another create pass the quota on a count without this
// repo, and refuse the creator's own push 403.
func TestCreateRepoHoldsCreateRepoMu(t *testing.T) {
	rig := setupRig(t)
	us := wp07SignIn(t, rig, "alice", "bob")
	alice, bob := us["alice"], us["bob"]
	// Stale rows of "locked" from before it had a directory, for the purge.
	if err := rig.users.AddMember("locked", bob.id, userstore.RoleMember); err != nil {
		t.Fatalf("seed locked: %v", err)
	}
	if _, _, err := rig.users.MintInviteKey("locked", bob.id, "k"); err != nil {
		t.Fatalf("seed locked key: %v", err)
	}
	if err := rig.users.RecordActivity("locked", bob.id, "push", ""); err != nil {
		t.Fatalf("seed locked activity: %v", err)
	}
	db := rig.users.DB()
	stmts := []string{`CREATE TABLE wp07_lock_probe (step TEXT NOT NULL, held INTEGER NOT NULL)`}
	for _, table := range []string{"repo_members", "repo_invite_keys", "repo_activity"} {
		stmts = append(stmts, `CREATE TRIGGER wp07_probe_purge_`+table+` BEFORE DELETE ON `+table+`
BEGIN INSERT INTO wp07_lock_probe VALUES ('purge `+table+` ' || OLD.repo_id, `+wp07LockProbeFunc+`()); END`)
	}
	stmts = append(stmts, `CREATE TRIGGER wp07_probe_owner_row AFTER INSERT ON repo_members
BEGIN INSERT INTO wp07_lock_probe VALUES ('insert repo_members ' || NEW.repo_id || ' ' || NEW.role, `+wp07LockProbeFunc+`()); END`)
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("install the lock probe: %v", err)
		}
	}
	calls, held := 0, 0
	rig.h.SetQuota(wp07QuotaHook{hook: func() {
		calls++
		if createRepoMu.TryLock() {
			createRepoMu.Unlock()
			return
		}
		held++
	}})

	wp07WantCreated(t, "alice creates locked", wp07Create(t, rig, "locked", alice.bearer), "locked")
	if calls != 1 || held != 1 {
		t.Errorf("quota checked %d times, %d of them under createRepoMu; want 1 and 1", calls, held)
	}
	rows, err := db.Query(`SELECT step, held FROM wp07_lock_probe ORDER BY rowid`)
	if err != nil {
		t.Fatalf("read the lock probe: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var step string
		var locked int
		if err := rows.Scan(&step, &locked); err != nil {
			t.Fatalf("scan the lock probe: %v", err)
		}
		got = append(got, fmt.Sprintf("%s held=%d", step, locked))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the lock probe: %v", err)
	}
	want := []string{
		"purge repo_members locked held=1",
		"purge repo_invite_keys locked held=1",
		"purge repo_activity locked held=1",
		"insert repo_members locked owner held=1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("statements of the create and whether createRepoMu was held:\n got %q\nwant %q", got, want)
	}
	wp07WantMembers(t, "locked", rig, "locked", map[string]string{alice.id: userstore.RoleOwner})
}

// TestCreateRepoConcurrentDistinctIDsQuotaExact: one user one repo short of
// the cap creating several new ids at once gets exactly one 201 and 402 for
// the rest. Without createRepoMu every create would count "owns 0" before
// any owner row existed and all of them would pass the quota.
func TestCreateRepoConcurrentDistinctIDsQuotaExact(t *testing.T) {
	rig := setupRig(t)
	alice := wp07SignIn(t, rig, "alice")["alice"]
	rig.h.SetQuota(fakeQuota{maxRepos: 1, maxMembers: 99})
	const n = 6
	ids := make([]string, n)
	got := make([]*httptest.ResponseRecorder, n)
	fns := make([]func(), n)
	for i := range fns {
		ids[i] = fmt.Sprintf("quota-race-%d", i)
		fns[i] = func() { got[i] = wp07Create(t, rig, ids[i], alice.bearer) }
	}
	wp02Race(fns...)

	created := 0
	for i, w := range got {
		if w.Code == http.StatusCreated {
			created++
			wp07WantCreated(t, ids[i], w, ids[i])
			continue
		}
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != http.StatusPaymentRequired || body.Code != "quota_repos" {
			t.Errorf("%s: %d %s, want 201 or 402 quota_repos", ids[i], w.Code, w.Body.String())
		}
		if _, err := os.Stat(filepath.Join(rig.store.Root, ids[i])); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: a refused create left a directory (stat err %v)", ids[i], err)
		}
	}
	if created != 1 {
		t.Errorf("%d creates answered 201, want exactly 1", created)
	}
	if owned, err := rig.users.CountOwnedRepos(alice.id); err != nil || owned != 1 {
		t.Errorf("alice owns %d repos (err %v), want 1", owned, err)
	}
}

// TestCreateRepoCreatorsOwnPushesNotRefused: the dashboard's create and the
// same user's extension or agent pushing to the new id at once. Whichever
// request creates the repo, the creator's pushes are all committed: a push
// that finds the repo before its owner row exists re-checks under
// createRepoMu, which the create holds until the purge and the owner row
// are done. This is the realistic race and does not hit the window on every
// run. TestCreateRepoCreatorsPushWaitsForOwnerRow pins deterministically that
// the push waits while the create is held before its purge, and
// TestCreateRepoHoldsCreateRepoMu that the lock covers the owner row.
func TestCreateRepoCreatorsOwnPushesNotRefused(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	alice := wp07SignIn(t, rig, "alice")["alice"]

	for i := 0; i < 20; i++ {
		repo := fmt.Sprintf("own-race-%02d", i)
		const n = 3
		pushes := make([]*httptest.ResponseRecorder, n)
		var create *httptest.ResponseRecorder
		fns := []func(){func() { create = wp07Create(t, rig, repo, alice.bearer) }}
		for j := 0; j < n; j++ {
			page := fmt.Sprintf("wiki/p%d.md", j)
			fns = append(fns, func() {
				pushes[j], _ = wp02Push(t, rig.router, repo, alice.bearer, "", wp02PushFile{Path: page, Content: page})
			})
		}
		wp02Race(fns...)

		for j, w := range pushes {
			wp07WantStatus(t, fmt.Sprintf("%s: push %d", repo, j), w, http.StatusOK)
		}
		if create.Code != http.StatusCreated {
			wp07WantRepoExists(t, repo+": create that lost to a push", create)
		} else {
			wp07WantCreated(t, repo+": create", create, repo)
		}
		wp07WantMembers(t, repo, rig, repo, map[string]string{alice.id: userstore.RoleOwner})
	}
}

// TestCreateRepoCreatorsPushWaitsForOwnerRow is the deterministic form of
// the race above. A write transaction the test holds on the userstore stops
// the create between gitstore.Create and its purge (PurgeRepo waits for the
// write lock), so the new repo's .git exists and its owner row does not.
// The creator's push in that window finds the repo, is not a member yet,
// and must wait for createRepoMu, which the create holds until its purge
// and owner row are done, instead of being refused 403 on its own repo.
// It fails if the create does not take createRepoMu or releases it before
// its purge commits. It does not catch a release between the purge and the
// owner row: the woken push then races the creator's insert, which nearly
// always lands first. TestCreateRepoHoldsCreateRepoMu pins that the lock is
// held through the owner row.
func TestCreateRepoCreatorsPushWaitsForOwnerRow(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	alice := wp07SignIn(t, rig, "alice")["alice"]
	const repo = "held-create"

	ctx := context.Background()
	conn, err := rig.users.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("userstore conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold the userstore write lock: %v", err)
	}
	held := true
	release := func() {
		if held {
			held = false
			if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
				t.Errorf("release the userstore write lock: %v", err)
			}
		}
	}
	defer release()

	createDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { createDone <- wp07Create(t, rig, repo, alice.bearer) }()
	for deadline := time.Now().Add(10 * time.Second); !rig.store.Exists(repo); time.Sleep(time.Millisecond) {
		if len(createDone) > 0 || time.Now().After(deadline) {
			release()
			w := <-createDone
			t.Fatalf("the create answered %d %s without its repo existing while it was held", w.Code, w.Body.String())
		}
	}

	pushDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w, _ := wp02Push(t, rig.router, repo, alice.bearer, "", wp02PushFile{Path: "wiki/a.md", Content: "a"})
		pushDone <- w
	}()
	select {
	case <-time.After(300 * time.Millisecond):
	case w := <-pushDone:
		t.Errorf("the creator's push answered %d %s while the create had no owner row yet; want it to wait for the create", w.Code, w.Body.String())
		pushDone <- w
	}
	release()

	wp07WantCreated(t, "alice creates "+repo, <-createDone, repo)
	wp07WantStatus(t, "alice's push during the create", <-pushDone, http.StatusOK)
	wp07WantMembers(t, repo, rig, repo, map[string]string{alice.id: userstore.RoleOwner})
}

// TestCreateRepoCreatedElsewhereNeverClaimed: a repo that appears between
// the existence checks and gitstore.Create, made by a creator outside
// createRepoMu (the legacy key, another server process on the same data
// root), makes Create report created=false. The create is then a 409 that
// claims nothing and purges nothing. Here the quota check, which runs in
// that gap, creates the repo.
func TestCreateRepoCreatedElsewhereNeverClaimed(t *testing.T) {
	rig := setupRig(t)
	us := wp07SignIn(t, rig, "carol", "dave")
	carol, dave := us["carol"], us["dave"]
	// Rows of "inwin" from before it had a directory.
	if err := rig.users.AddMember("inwin", dave.id, userstore.RoleMember); err != nil {
		t.Fatalf("seed inwin: %v", err)
	}
	_, raw, err := rig.users.MintInviteKey("inwin", dave.id, "k")
	if err != nil {
		t.Fatalf("seed inwin key: %v", err)
	}
	if err := rig.users.RecordActivity("inwin", dave.id, "push", ""); err != nil {
		t.Fatalf("seed inwin activity: %v", err)
	}
	fired := false
	rig.h.SetQuota(wp07QuotaHook{hook: func() {
		if fired {
			return
		}
		fired = true
		if err := rig.store.Init("inwin"); err != nil {
			t.Errorf("init inwin: %v", err)
		}
	}})

	wp07WantRepoExists(t, "carol creates inwin", wp07Create(t, rig, "inwin", carol.bearer))
	if !fired {
		t.Fatalf("the quota hook did not run between the existence check and Create")
	}
	if r := wp02Role(t, rig, "inwin", carol.id); r != "" {
		t.Errorf("carol's role in inwin = %q, want none", r)
	}
	if owners, err := rig.users.RepoOwners("inwin"); err != nil || len(owners) != 0 {
		t.Errorf("inwin owners %v (err %v), want none", owners, err)
	}
	wp07WantMembers(t, "inwin (not purged)", rig, "inwin", map[string]string{dave.id: userstore.RoleMember})
	wp07WantResolves(t, "inwin key (not purged)", rig, raw, "inwin")
	wp07WantActivity(t, "inwin (not purged)", rig, "inwin", 1)
}

// TestCreateRepoListingErrorCreatesNothing: without the known repo ids the
// aliases of the new directory cannot be found, and their stale rows would
// survive the purge and reach the new repo. A failed listing is a 500
// before anything is created. The table the listing reads is back by the
// time the create would check its quota (under createRepoMu, before
// gitstore.Create), so a create that went on after the failed listing
// would purge and add its owner row normally, and only the alias's rows
// would survive: "my.repo" would keep alice as owner of carol's directory.
func TestCreateRepoListingErrorCreatesNothing(t *testing.T) {
	rig := setupRig(t)
	us := wp07SignIn(t, rig, "alice", "carol")
	alice, carol := us["alice"], us["carol"]
	// Stale rows of "my.repo", whose directory "myrepo" an operator deleted.
	if err := rig.users.AddMember("my.repo", alice.id, userstore.RoleOwner); err != nil {
		t.Fatalf("seed my.repo: %v", err)
	}
	_, raw, err := rig.users.MintInviteKey("my.repo", alice.id, "k")
	if err != nil {
		t.Fatalf("seed my.repo key: %v", err)
	}
	if err := rig.users.RecordActivity("my.repo", alice.id, "push", ""); err != nil {
		t.Fatalf("seed my.repo activity: %v", err)
	}

	restore := wp07HideTable(t, rig, "repo_invite_keys")
	quotaChecked := false
	rig.h.SetQuota(wp07QuotaHook{hook: func() {
		quotaChecked = true
		restore()
	}})
	w := wp07Create(t, rig, "myrepo", carol.bearer)
	restore()

	wp07WantStatus(t, "carol creates myrepo, listing fails", w, http.StatusInternalServerError)
	if quotaChecked {
		t.Errorf("a create whose listing failed went on to its quota check")
	}
	if _, err := os.Stat(filepath.Join(rig.store.Root, "myrepo")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a create whose listing failed made the repo (stat err %v)", err)
	}
	if r := wp02Role(t, rig, "myrepo", carol.id); r != "" {
		t.Errorf("carol's role in myrepo = %q, want none", r)
	}
	wp07WantMembers(t, "my.repo", rig, "my.repo", map[string]string{alice.id: userstore.RoleOwner})
	wp07WantResolves(t, "my.repo key", rig, raw, "my.repo")
	wp07WantActivity(t, "my.repo", rig, "my.repo", 1)
}

// TestRepoDirNameMirrorsGitstore pins repoDirName to gitstore's private
// sanitize: the directory gitstore creates for an id is repoDirName(id), and
// an id that sanitizes to nothing has no directory.
func TestRepoDirNameMirrorsGitstore(t *testing.T) {
	canonical := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	for id, want := range map[string]string{
		"my.repo":  "myrepo",
		"a b":      "ab",
		"x/y":      "xy",
		"..ab":     "ab",
		"Ab-1_":    "Ab-1_",
		"\u00fc-x": "-x",
		"a.b.c":    "abc",
	} {
		got := repoDirName(id)
		if got != want || !canonical.MatchString(got) {
			t.Errorf("repoDirName(%q) = %q, want %q", id, got, want)
		}
		store, err := gitstore.Open(filepath.Join(t.TempDir(), "repos"))
		if err != nil {
			t.Fatalf("gitstore: %v", err)
		}
		if err := store.Init(id); err != nil {
			t.Errorf("init %q: %v", id, err)
			continue
		}
		if _, err := os.Stat(filepath.Join(store.Root, got, ".git")); err != nil {
			t.Errorf("%q: no repo in directory %q: %v", id, got, err)
		}
		if repos, err := store.ListRepos(); err != nil || !slices.Equal(repos, []string{got}) {
			t.Errorf("%q: repos on disk %v (err %v), want [%s]", id, repos, err, got)
		}
	}
	for _, id := range []string{"", "...", "\u00fc"} {
		if got := repoDirName(id); got != "" {
			t.Errorf("repoDirName(%q) = %q, want empty", id, got)
		}
		store, err := gitstore.Open(filepath.Join(t.TempDir(), "repos"))
		if err != nil {
			t.Fatalf("gitstore: %v", err)
		}
		if err := store.Init(id); err == nil {
			t.Errorf("gitstore created a repo for %q", id)
		}
	}
}

// TestRepoAliases: the known ids that name the directory of a new repo id
// are the ids that sanitize to it exactly ("my.repo" for "myrepo") and, only
// where the filesystem folds case, its case variants. Near misses ("my-repo"
// keeps its dash, "myrepo2", "amyrepo") never are.
func TestRepoAliases(t *testing.T) {
	rig := setupRig(t)
	if err := rig.store.Init("myrepo"); err != nil {
		t.Fatalf("init myrepo: %v", err)
	}
	known := []string{"", "...", "MYREPO", "My.Repo", "MyRepo", "amyrepo", "my-repo", "my.repo", "my_repo", "myrepo", "myrepo2", "other", "x"}
	folds := wp07FoldsCase(t, filepath.Dir(rig.store.Root))
	want := []string{"my.repo"}
	if folds {
		want = []string{"MYREPO", "My.Repo", "MyRepo", "my.repo"}
	}
	check := func(label string) {
		t.Helper()
		got, err := rig.h.repoAliases("myrepo", known)
		slices.Sort(got)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: aliases of myrepo = %q (err %v), want %q", label, got, err, want)
		}
	}
	check("folds=" + fmt.Sprint(folds))
	if !folds {
		// A live repo that differs only in case is its own directory here.
		if err := rig.store.Init("MyRepo"); err != nil {
			t.Fatalf("init MyRepo: %v", err)
		}
		check("with a live MyRepo")
	}
}

// TestPurgeStaleAccessUncertainCaseVariant: a case variant whose directory
// cannot be stat'ed (EACCES here) is never guessed to be an alias: its rows
// stay and an error is returned. The new id's own rows and its exact
// aliases, which need no stat, are still purged: the directory was just
// created, so they are certainly stale.
func TestPurgeStaleAccessUncertainCaseVariant(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions enforced for the test user")
	}
	rig := setupRig(t)
	us := wp07SignIn(t, rig, "alice", "bob", "carol")
	seed := func(repo, uid string) string {
		t.Helper()
		if err := rig.users.AddMember(repo, uid, userstore.RoleOwner); err != nil {
			t.Fatalf("seed %s: %v", repo, err)
		}
		_, raw, err := rig.users.MintInviteKey(repo, uid, "k")
		if err != nil {
			t.Fatalf("seed %s key: %v", repo, err)
		}
		if err := rig.users.RecordActivity(repo, uid, "push", ""); err != nil {
			t.Fatalf("seed %s activity: %v", repo, err)
		}
		return raw
	}
	kOwn := seed("myrepo", us["alice"].id)
	kDot := seed("my.repo", us["bob"].id)
	kCase := seed("MyRepo", us["carol"].id)
	if err := rig.store.Init("myrepo"); err != nil {
		t.Fatalf("init myrepo: %v", err)
	}
	known, err := rig.users.KnownRepoIDs()
	if err != nil {
		t.Fatalf("known ids: %v", err)
	}

	root := rig.store.Root
	if err := os.Chmod(root, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	if _, err := os.Stat(filepath.Join(root, "MyRepo")); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Skipf("stat under a chmod 000 root gives %v on this filesystem", err)
	}
	aliases, aliasErr := rig.h.repoAliases("myrepo", known)
	purgeErr := rig.h.purgeStaleAccess("myrepo", known)
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatalf("chmod back: %v", err)
	}

	if aliasErr == nil || !slices.Equal(aliases, []string{"my.repo"}) {
		t.Errorf("repoAliases = %q (err %v), want [my.repo] and an error", aliases, aliasErr)
	}
	if purgeErr == nil {
		t.Errorf("purgeStaleAccess: nil error, want the stat error")
	}
	for repo, raw := range map[string]string{"myrepo": kOwn, "my.repo": kDot} {
		wp07WantMembers(t, "purged "+repo, rig, repo, map[string]string{})
		wp07WantKeys(t, "purged "+repo, rig, repo, 0)
		wp07WantResolves(t, "purged "+repo, rig, raw, "")
		wp07WantActivity(t, "purged "+repo, rig, repo, 0)
	}
	wp07WantMembers(t, "kept MyRepo", rig, "MyRepo", map[string]string{us["carol"].id: userstore.RoleOwner})
	wp07WantKeys(t, "kept MyRepo", rig, "MyRepo", 1)
	wp07WantResolves(t, "kept MyRepo", rig, kCase, "MyRepo")
	wp07WantActivity(t, "kept MyRepo", rig, "MyRepo", 1)
}
