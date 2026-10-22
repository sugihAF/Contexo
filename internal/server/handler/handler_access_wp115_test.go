package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/userstore"
	"github.com/sugihAF/contexo/quota"
)

// wp115Quota is a quota.Policy for the WP-1.15 race tests. AllowRepoCreate
// refuses (402 quota_repos) the users in denyRepos, so their pushes to an
// absent id never create it. AllowMemberAdd calls memberHook when it is set:
// JoinRepo consults it for a non-member after it resolved the invite key and
// before it adds the member row, on the request's goroutine.
type wp115Quota struct {
	denyRepos  map[string]bool
	memberHook func()
}

func (q wp115Quota) AllowRepoCreate(uid string, _ int) error {
	if q.denyRepos[uid] {
		return &quota.LimitError{Kind: "repos", Limit: 0, Message: "repo cap hit", UpgradeURL: "https://contexo.live/#pricing"}
	}
	return nil
}

func (q wp115Quota) AllowMemberAdd(string, []string, int) error {
	if q.memberHook != nil {
		q.memberHook()
	}
	return nil
}

// wp115LockWaiters counts the goroutines blocked in a sync.Mutex Lock called
// directly from a function whose name contains fn (see wp07WaitsForLockIn).
// For a handler that takes createRepoMu itself, it tells a test that holds
// createRepoMu how many requests wait for it there.
func wp115LockWaiters(fn string) int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	for n == len(buf) {
		buf = make([]byte, 2*len(buf))
		n = runtime.Stack(buf, true)
	}
	count := 0
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
				count++
				break
			}
		}
	}
	return count
}

// wp115AwaitLockWaiters polls every millisecond, for up to 10 s, until at
// least n goroutines wait for a mutex in fn (true). It returns false as soon
// as one of done holds a response (that request answered without waiting),
// or at the deadline. It never receives from done.
func wp115AwaitLockWaiters(t *testing.T, fn string, n int, done ...<-chan *httptest.ResponseRecorder) bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, ch := range done {
			if len(ch) > 0 {
				return false
			}
		}
		if wp115LockWaiters(fn) >= n {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// wp115Async runs req on its own goroutine and returns a channel that
// receives its response. A cleanup waits (up to 10 s) for the request to
// finish, so none outlives the rig. req must not call t.Fatal.
func wp115Async(t *testing.T, req func() *httptest.ResponseRecorder) <-chan *httptest.ResponseRecorder {
	t.Helper()
	out := make(chan *httptest.ResponseRecorder, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		out <- req()
	}()
	t.Cleanup(func() {
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Errorf("a request started by the test never finished")
		}
	})
	return out
}

// wp115Recv receives the response of an async request, failing the test
// after 10 s. Call it only while the test holds no lock the request needs.
func wp115Recv(t *testing.T, label string, ch <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-ch:
		return w
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: no response within 10 s", label)
		return nil
	}
}

// wp115RecreateAsync starts bearer's POST /v1/repos {"id": repo} on its own
// goroutine.
func wp115RecreateAsync(t *testing.T, rig *testRig, repo, bearer string) <-chan *httptest.ResponseRecorder {
	t.Helper()
	return wp115Async(t, func() *httptest.ResponseRecorder { return wp07Create(t, rig, repo, bearer) })
}

// wp115CreateOrWait waits, polling every millisecond for up to 10 s, until
// the create on done answers (its response is returned) or waits for
// createRepoMu in createRepoAsOwner (nil is returned, and the response is
// still on done). nil at the deadline too.
func wp115CreateOrWait(done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case w := <-done:
			return w
		default:
		}
		if wp115LockWaiters("(*Handler).createRepoAsOwner") >= 1 || time.Now().After(deadline) {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
}

// wp115HoldUserstoreWrite takes the userstore's SQLite write lock on a
// dedicated connection (BEGIN IMMEDIATE) and returns its release (ROLLBACK).
// Every other write waits for it (busy_timeout 5 s); reads go on (WAL).
// release is idempotent and also runs when the test ends.
func wp115HoldUserstoreWrite(t *testing.T, rig *testRig) (release func()) {
	t.Helper()
	ctx := context.Background()
	conn, err := rig.users.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("userstore conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		t.Fatalf("hold the userstore write lock: %v", err)
	}
	var once gosync.Once
	release = func() {
		once.Do(func() {
			if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
				t.Errorf("release the userstore write lock: %v", err)
			}
			conn.Close()
		})
	}
	t.Cleanup(release)
	return release
}

// wp115EmulateCreate does what createRepoAsOwner's critical section does
// for a new repo, for a test that holds createRepoMu itself: gitstore.Create
// (it must create the repo), the purge of repo's rows, and ownerID's owner
// row (it must be inserted).
func wp115EmulateCreate(t *testing.T, rig *testRig, repo, ownerID string) {
	t.Helper()
	created, err := rig.store.Create(repo)
	if err != nil || !created {
		t.Fatalf("emulated create of %s: created=%v err=%v, want a new repo", repo, created, err)
	}
	if err := rig.users.PurgeRepo(repo); err != nil {
		t.Fatalf("emulated create of %s: purge: %v", repo, err)
	}
	inserted, err := rig.users.AddMemberIfAbsent(repo, ownerID, userstore.RoleOwner)
	if err != nil || !inserted {
		t.Fatalf("emulated create of %s: owner row inserted=%v err=%v, want inserted", repo, inserted, err)
	}
}

// wp115Mint mints an invite key labelled label for repo over HTTP as bearer
// and returns the raw key.
func wp115Mint(t *testing.T, rig *testRig, repo, bearer, label string) string {
	t.Helper()
	w := doJSON(t, rig.router, http.MethodPost, "/v1/repos/"+repo+"/invite-keys", bearer, map[string]string{"label": label})
	var body struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusCreated || body.Token == "" {
		t.Fatalf("mint key %q for %q: %d %s", label, repo, w.Code, w.Body.String())
	}
	return body.Token
}

// wp115Stale is a repo an operator deleted whose userstore rows remain: alice
// was its owner, bob joined it with k1, and k2 (labelled "k2", held by dave)
// was never used.
type wp115Stale struct {
	repo   string
	k1, k2 string
}

// wp115SeedDeleted makes the stale rows of an operator-deleted repo: alice
// creates repo (POST /v1/repos), mints k1 (label "k1"), bob joins with it,
// alice mints k2 (label "k2"), alice pushes wiki/old.md (activity), then the
// repo directory is removed. us must hold alice and bob; the rig needs
// wp02WireSync.
func wp115SeedDeleted(t *testing.T, rig *testRig, us map[string]wp07User, repo string) wp115Stale {
	t.Helper()
	alice, bob := us["alice"], us["bob"]
	if w := wp07Create(t, rig, repo, alice.bearer); w.Code != http.StatusCreated {
		t.Fatalf("seed: alice creates %s: %d %s", repo, w.Code, w.Body.String())
	}
	s := wp115Stale{repo: repo}
	s.k1 = wp115Mint(t, rig, repo, alice.bearer, "k1")
	if w := wp07Join(t, rig, s.k1, bob.bearer); w.Code != http.StatusOK {
		t.Fatalf("seed: bob joins %s: %d %s", repo, w.Code, w.Body.String())
	}
	s.k2 = wp115Mint(t, rig, repo, alice.bearer, "k2")
	wp07MustPush(t, rig, repo, alice.bearer, "wiki/old.md", "old content")
	if err := os.RemoveAll(filepath.Join(rig.store.Root, repo)); err != nil {
		t.Fatalf("seed: operator deletes %s: %v", repo, err)
	}
	if rig.store.Exists(repo) {
		t.Fatalf("seed: %s still exists after the operator deleted it", repo)
	}
	if r := wp02Role(t, rig, repo, bob.id); r != userstore.RoleMember {
		t.Fatalf("seed: bob's stale role in %s = %q, want member", repo, r)
	}
	if keys, err := rig.users.ListInviteKeys(repo); err != nil || len(keys) != 2 {
		t.Fatalf("seed: %s has keys %+v (err %v), want 2", repo, keys, err)
	}
	return s
}

// wp115WantBody asserts a status and the exact body.
func wp115WantBody(t *testing.T, label string, w *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if w.Code != status || w.Body.String() != body {
		t.Errorf("%s: %d %s, want %d %s", label, w.Code, w.Body.String(), status, body)
	}
}

// wp115WantStaleLockedOut asserts that the stale access of s reaches nothing
// of the re-created repo: no invite key is left, k2 is unknown, alice and
// bob are refused its pages, and dave's join with k2 is a 404. Call it after
// the row assertions: on failure dave's join may add a row.
func wp115WantStaleLockedOut(t *testing.T, label string, rig *testRig, us map[string]wp07User, s wp115Stale) {
	t.Helper()
	wp07WantKeys(t, label, rig, s.repo, 0)
	wp07WantResolves(t, label+": stale k2", rig, s.k2, "")
	for _, name := range []string{"alice", "bob"} {
		wp07WantStatus(t, label+": "+name+" lists "+s.repo,
			wp07Get(t, rig, "/v1/repos/"+s.repo+"/pages", us[name].bearer), http.StatusForbidden)
	}
	wp115WantBody(t, label+": dave joins with stale k2", wp07Join(t, rig, s.k2, us["dave"].bearer),
		http.StatusNotFound, `{"error":"invite key not recognized"}`)
}

// TestStaleAccess_PushInCreateWindowRefused: git init makes the new repo
// visible before its creator purges the stale rows of the id. A former
// member's push or deprecated create in that window used to pass on its
// stale row (a positive first membership check did not wait for
// createRepoMu) and commit into the new repo. A write transaction the test
// holds on the userstore stops carol's re-create between gitstore.Create and
// its purge; bob's push and legacy create then must wait for createRepoMu,
// which the create holds until its purge and owner row are done, and be
// refused.
func TestStaleAccess_PushInCreateWindowRefused(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	wp02WireLegacyCreate(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	bob, carol := us["bob"], us["carol"]
	s := wp115SeedDeleted(t, rig, us, "pw")

	release := wp115HoldUserstoreWrite(t, rig)
	cw := wp115RecreateAsync(t, rig, s.repo, carol.bearer)
	for deadline := time.Now().Add(10 * time.Second); !rig.store.Exists(s.repo); time.Sleep(time.Millisecond) {
		if len(cw) > 0 || time.Now().After(deadline) {
			release()
			w := wp115Recv(t, "carol re-creates pw", cw)
			t.Fatalf("carol's re-create answered %d %s without pw existing while the userstore was held", w.Code, w.Body.String())
		}
	}
	push := wp115Async(t, func() *httptest.ResponseRecorder {
		w, _ := wp02Push(t, rig.router, s.repo, bob.bearer, "", wp02PushFile{Path: "wiki/bob.md", Content: "bob"})
		return w
	})
	legacy := wp115Async(t, func() *httptest.ResponseRecorder { return wp02LegacyCreate(t, rig, s.repo, bob.bearer) })
	acted := ""
	for deadline := time.Now().Add(10 * time.Second); wp115LockWaiters("(*Handler).isMemberAfterCreate") < 2; time.Sleep(time.Millisecond) {
		if len(push) > 0 || len(legacy) > 0 {
			acted = "a request of bob answered before the create's purge and owner row"
			break
		}
		if _, _, err := rig.store.Read(s.repo, "wiki/bob.md"); err == nil {
			acted = "bob's page was committed into the new repo"
			break
		}
		if time.Now().After(deadline) {
			acted = "bob's requests did not wait for createRepoMu within 10 s"
			break
		}
	}
	release()
	if acted != "" {
		t.Errorf("stale member acted inside the create window: %s", acted)
	}

	wp07WantCreated(t, "carol re-creates pw", wp115Recv(t, "carol re-creates pw", cw), "pw")
	wp115WantBody(t, "bob (stale member) pushes in the create window", wp115Recv(t, "bob pushes", push),
		http.StatusForbidden, `{"error":"not a member of this repo"}`)
	wp07WantRepoExists(t, "bob (stale member) legacy-creates in the create window", wp115Recv(t, "bob legacy-creates", legacy))
	if _, _, err := rig.store.Read(s.repo, "wiki/bob.md"); err == nil {
		t.Errorf("bob's page wiki/bob.md is in carol's new pw")
	}
	wp07WantMembers(t, "pw", rig, s.repo, map[string]string{carol.id: userstore.RoleOwner})
	wp07WantActivity(t, "pw", rig, s.repo, 0)
}

// TestStaleAccess_PushAutoCreatePurges: a signed-in first push that creates
// an operator-deleted id (createOwnedRepo) must purge the stale rows of the
// id and of the aliases of its directory, as POST /v1/repos does.
func TestStaleAccess_PushAutoCreatePurges(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	alice, carol := us["alice"], us["carol"]

	// (a) The id itself.
	s := wp115SeedDeleted(t, rig, us, "ax")
	wp07MustPush(t, rig, s.repo, carol.bearer, "wiki/c.md", "carol")
	wp07WantMembers(t, "ax", rig, s.repo, map[string]string{carol.id: userstore.RoleOwner})
	wp07WantActivity(t, "ax (carol's push only)", rig, s.repo, 1)
	for _, name := range []string{"alice", "bob"} {
		w, _ := wp02Push(t, rig.router, s.repo, us[name].bearer, "", wp02PushFile{Path: "wiki/" + name + ".md", Content: name})
		wp07WantStatus(t, "ax: "+name+" pushes", w, http.StatusForbidden)
	}
	wp115WantStaleLockedOut(t, "ax", rig, us, s)

	// (b) An alias: "al.x" names directory alx.
	wp07MustPush(t, rig, "al.x", "legacy-key", "wiki/a.md", "dotted")
	if err := rig.users.AddMember("al.x", alice.id, userstore.RoleOwner); err != nil {
		t.Fatalf("alice owns al.x: %v", err)
	}
	_, raw, err := rig.users.MintInviteKey("al.x", alice.id, "k")
	if err != nil {
		t.Fatalf("mint al.x key: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(rig.store.Root, "alx")); err != nil {
		t.Fatalf("operator deletes alx: %v", err)
	}
	wp07MustPush(t, rig, "alx", carol.bearer, "wiki/c.md", "carol")
	wp07WantMembers(t, "al.x", rig, "al.x", map[string]string{})
	wp07WantKeys(t, "al.x", rig, "al.x", 0)
	wp07WantResolves(t, "stale al.x key", rig, raw, "")
	wp07WantStatus(t, "alice lists via al.x", wp07Get(t, rig, "/v1/repos/al.x/pages", alice.bearer), http.StatusForbidden)
	wp07WantMembers(t, "alx", rig, "alx", map[string]string{carol.id: userstore.RoleOwner})
}

// TestStaleAccess_LegacyCreatePurges: the deprecated POST /v1/repos/:id by a
// signed-in user that creates an operator-deleted id must purge its stale
// rows too.
func TestStaleAccess_LegacyCreatePurges(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	wp02WireLegacyCreate(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	carol := us["carol"]
	s := wp115SeedDeleted(t, rig, us, "lx")

	wp115WantBody(t, "carol legacy-creates lx", wp02LegacyCreate(t, rig, s.repo, carol.bearer), http.StatusCreated, `{"repo_id":"lx"}`)
	wp07WantMembers(t, "lx", rig, s.repo, map[string]string{carol.id: userstore.RoleOwner})
	wp07WantActivity(t, "lx", rig, s.repo, 0)
	wp115WantStaleLockedOut(t, "lx", rig, us, s)
}

// TestStaleAccess_LegacyKeyCreatePurges: a legacy-key create (first push or
// the deprecated create) of an operator-deleted id leaves an ownerless repo
// and must purge the stale rows of the id and of the aliases of its
// directory. A legacy-key create of an existing repo stays an idempotent
// no-op that purges nothing.
func TestStaleAccess_LegacyKeyCreatePurges(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	wp02WireLegacyCreate(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	alice, bob, carol := us["alice"], us["bob"], us["carol"]

	// (a) First push.
	sa := wp115SeedDeleted(t, rig, us, "kx")
	wp07MustPush(t, rig, sa.repo, "legacy-key", "wiki/k.md", "legacy")
	wp07WantMembers(t, "kx", rig, sa.repo, map[string]string{})
	wp07WantActivity(t, "kx", rig, sa.repo, 0)
	wp115WantStaleLockedOut(t, "kx", rig, us, sa)

	// (b) Deprecated create.
	sb := wp115SeedDeleted(t, rig, us, "ky")
	wp115WantBody(t, "legacy key creates ky", wp02LegacyCreate(t, rig, sb.repo, "legacy-key"), http.StatusCreated, `{"repo_id":"ky"}`)
	wp07WantMembers(t, "ky", rig, sb.repo, map[string]string{})
	wp07WantActivity(t, "ky", rig, sb.repo, 0)
	wp115WantStaleLockedOut(t, "ky", rig, us, sb)

	// (c) An existing repo is left alone.
	wp07WantCreated(t, "alice creates keep", wp07Create(t, rig, "keep", alice.bearer), "keep")
	kk := wp07Mint(t, rig, "keep", alice.bearer)
	wp07WantStatus(t, "bob joins keep", wp07Join(t, rig, kk, bob.bearer), http.StatusOK)
	wp115WantBody(t, "legacy key creates existing keep", wp02LegacyCreate(t, rig, "keep", "legacy-key"), http.StatusCreated, `{"repo_id":"keep"}`)
	wp07MustPush(t, rig, "keep", "legacy-key", "wiki/k.md", "legacy")
	wp07WantMembers(t, "keep", rig, "keep", map[string]string{alice.id: userstore.RoleOwner, bob.id: userstore.RoleMember})
	wp07WantResolves(t, "keep key", rig, kk, "keep")

	// (d) An alias: "k.z" names the directory of carol's deleted kz.
	wp07WantCreated(t, "carol creates kz", wp07Create(t, rig, "kz", carol.bearer), "kz")
	kz := wp07Mint(t, rig, "kz", carol.bearer)
	if err := os.RemoveAll(filepath.Join(rig.store.Root, "kz")); err != nil {
		t.Fatalf("operator deletes kz: %v", err)
	}
	wp07MustPush(t, rig, "k.z", "legacy-key", "wiki/k.md", "legacy")
	wp07WantMembers(t, "kz", rig, "kz", map[string]string{})
	wp07WantKeys(t, "kz", rig, "kz", 0)
	wp07WantResolves(t, "stale kz key", rig, kz, "")
	wp07WantStatus(t, "carol lists kz", wp07Get(t, rig, "/v1/repos/kz/pages", carol.bearer), http.StatusForbidden)
}

// TestStaleAccess_LegacyKeyCreateHoldsCreateRepoMu: a legacy-key create
// (first push, deprecated create) runs gitstore.Create under createRepoMu
// like every other creation, so it is serialized with the membership
// decisions a creation can invalidate. The test holds createRepoMu: both
// creates must wait for it in createLegacyRepo and make no directory until
// it is released.
func TestStaleAccess_LegacyKeyCreateHoldsCreateRepoMu(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	wp02WireLegacyCreate(rig)

	var push, create <-chan *httptest.ResponseRecorder
	waited := false
	var made []string
	func() {
		createRepoMu.Lock()
		defer createRepoMu.Unlock()
		push = wp115Async(t, func() *httptest.ResponseRecorder {
			w, _ := wp02Push(t, rig.router, "lk-push", "legacy-key", "", wp02PushFile{Path: "wiki/p.md", Content: "p"})
			return w
		})
		create = wp115Async(t, func() *httptest.ResponseRecorder { return wp02LegacyCreate(t, rig, "lk-create", "legacy-key") })
		waited = wp115AwaitLockWaiters(t, "(*Handler).createLegacyRepo", 2, push, create)
		for _, id := range []string{"lk-push", "lk-create"} {
			if _, err := os.Stat(filepath.Join(rig.store.Root, id)); !errors.Is(err, fs.ErrNotExist) {
				made = append(made, fmt.Sprintf("%s (stat err %v)", id, err))
			}
		}
	}()
	wPush := wp115Recv(t, "legacy-key push to lk-push", push)
	wCreate := wp115Recv(t, "legacy-key create of lk-create", create)
	if !waited {
		t.Errorf("legacy-key create ran outside createRepoMu: push answered %d %s, create answered %d %s",
			wPush.Code, wPush.Body.String(), wCreate.Code, wCreate.Body.String())
	}
	if len(made) > 0 {
		t.Errorf("a legacy-key create made a repo directory while createRepoMu was held: %v", made)
	}
	wp07WantStatus(t, "legacy-key push to lk-push", wPush, http.StatusOK)
	wp115WantBody(t, "legacy-key create of lk-create", wCreate, http.StatusCreated, `{"repo_id":"lk-create"}`)
	for _, id := range []string{"lk-push", "lk-create"} {
		if !rig.store.Exists(id) {
			t.Errorf("%s does not exist after its legacy-key create", id)
		}
	}
}

// TestStaleAccess_FloodDuringRecreateLeavesOnlyCreator is the realistic
// attack: while carol re-creates an operator-deleted id, its former members
// and owner flood every route that could regain access (dave and bob join
// with the stale k2, alice mints keys, bob pushes; bob's and dave's pushes
// to the absent id are refused by the quota, so they never create it).
// Whatever interleaving, carol must end up the only member, with no key, no
// page of bob and no activity. Probabilistic: it does not hit every window
// on every run.
func TestStaleAccess_FloodDuringRecreateLeavesOnlyCreator(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	alice, bob, carol, dave := us["alice"], us["bob"], us["carol"], us["dave"]
	t.Cleanup(func() { rig.h.SetQuota(nil) })

	for r := 0; r < 5; r++ {
		repo := fmt.Sprintf("flood-%02d", r)
		rig.h.SetQuota(nil)
		s := wp115SeedDeleted(t, rig, us, repo)
		rig.h.SetQuota(wp115Quota{denyRepos: map[string]bool{bob.id: true, dave.id: true}})

		stop := make(chan struct{})
		var wg gosync.WaitGroup
		flood := func(req func()) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					req()
				}
			}()
		}
		for g := 0; g < 3; g++ {
			flood(func() { wp07Join(t, rig, s.k2, dave.bearer) })
			flood(func() { wp07Join(t, rig, s.k2, bob.bearer) })
			flood(func() {
				doJSON(t, rig.router, http.MethodPost, "/v1/repos/"+repo+"/invite-keys", alice.bearer, map[string]string{"label": "flood"})
			})
			flood(func() {
				wp02Push(t, rig.router, repo, bob.bearer, "", wp02PushFile{Path: "wiki/bob.md", Content: "bob"})
			})
		}
		time.Sleep(20 * time.Millisecond)
		w := wp07Create(t, rig, repo, carol.bearer)
		time.Sleep(20 * time.Millisecond)
		close(stop)
		wg.Wait()

		wp07WantCreated(t, repo+": carol re-creates", w, repo)
		wp07WantMembers(t, repo, rig, repo, map[string]string{carol.id: userstore.RoleOwner})
		wp07WantKeys(t, repo, rig, repo, 0)
		if _, _, err := rig.store.Read(repo, "wiki/bob.md"); err == nil {
			t.Errorf("%s: bob's page wiki/bob.md is in carol's new repo", repo)
		}
		wp07WantActivity(t, repo, rig, repo, 0)
	}
}
