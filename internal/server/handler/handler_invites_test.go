package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/userstore"
)

// wp115WireInviteKeyRoutes mounts GET and DELETE of the invite-key routes on
// a new /v1 group of the rig (setupRig registers only POST).
func wp115WireInviteKeyRoutes(rig *testRig) {
	v1 := rig.router.Group("/v1")
	v1.Use(auth.GinMiddleware(rig.resolver.Validator()))
	v1.GET("/repos/:id/invite-keys", rig.h.ListInviteKeys)
	v1.DELETE("/repos/:id/invite-keys/:keyId", rig.h.DeleteInviteKey)
}

// wp115HookBody is a request body that runs hook on its first Read, then
// serves payload: hook runs at the moment the handler reads its body.
type wp115HookBody struct {
	payload []byte
	hook    func()
	fired   bool
}

func (b *wp115HookBody) Read(p []byte) (int, error) {
	if !b.fired {
		b.fired = true
		b.hook()
	}
	if len(b.payload) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.payload)
	b.payload = b.payload[n:]
	return n, nil
}

// wp115MintBody is the 201 body of POST /v1/repos/:id/invite-keys.
type wp115MintBody struct {
	Key struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		CreatedAt int64  `json:"created_at"`
		ExpiresAt int64  `json:"expires_at"`
	} `json:"key"`
	Token string `json:"token"`
}

// wp115KeyJSON is the exact JSON of one invite key in the mint and list
// bodies.
func wp115KeyJSON(m wp115MintBody) string {
	return fmt.Sprintf(`{"id":%q,"label":%q,"created_at":%d,"expires_at":%d}`, m.Key.ID, m.Key.Label, m.Key.CreatedAt, m.Key.ExpiresAt)
}

// wp115WantMinted asserts a mint's 201 body byte for byte: the key with the
// label echoed, a 7-day expiry and a ctxi_ token. It returns the body.
func wp115WantMinted(t *testing.T, label string, w *httptest.ResponseRecorder, wantLabel string) wp115MintBody {
	t.Helper()
	var m wp115MintBody
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("%s: %d %s (decode err %v), want 201", label, w.Code, w.Body.String(), err)
	}
	want := `{"key":` + wp115KeyJSON(m) + `,"token":` + fmt.Sprintf("%q", m.Token) + `}`
	if w.Body.String() != want {
		t.Errorf("%s: body %s, want exactly %s", label, w.Body.String(), want)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("%s: Content-Type %q, want application/json; charset=utf-8", label, ct)
	}
	if m.Key.ID == "" || m.Key.Label != wantLabel || !strings.HasPrefix(m.Token, userstore.InviteKeyPrefix) ||
		m.Key.ExpiresAt-m.Key.CreatedAt != 604800 {
		t.Errorf("%s: key %+v token %q, want an id, label %q, a %s token and a 604800 s lifetime",
			label, m.Key, m.Token, wantLabel, userstore.InviteKeyPrefix)
	}
	return m
}

// TestStaleAccess_JoinStraddlingRecreateNeverJoins: JoinRepo resolved the
// invite key and inserted the member row later, without createRepoMu. A
// re-create of the operator-deleted id that purged between the two let a
// stale key add its holder to the NEW repo. The seam is the member quota,
// which runs between the resolve and the insert for a non-member: it starts
// carol's re-create and waits until the create answered or waits for
// createRepoMu.
func TestStaleAccess_JoinStraddlingRecreateNeverJoins(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	carol, dave := us["carol"], us["dave"]
	s := wp115SeedDeleted(t, rig, us, "jx")

	fired := 0
	var create *httptest.ResponseRecorder
	var createDone <-chan *httptest.ResponseRecorder
	rig.h.SetQuota(wp115Quota{memberHook: func() {
		fired++
		if fired > 1 {
			return
		}
		createDone = wp115RecreateAsync(t, rig, s.repo, carol.bearer)
		create = wp115CreateOrWait(createDone)
	}})
	join := wp07Join(t, rig, s.k2, dave.bearer)
	if fired == 0 {
		t.Fatalf("the member quota never ran for dave's join (%d %s): the seam is broken", join.Code, join.Body.String())
	}
	if create == nil {
		create = wp115Recv(t, "carol re-creates jx", createDone)
	}

	wp07WantCreated(t, "carol re-creates jx during dave's join", create, "jx")
	wp07WantMembers(t, "jx", rig, s.repo, map[string]string{carol.id: userstore.RoleOwner})
	wp07WantResolves(t, "stale k2", rig, s.k2, "")
	wp115WantBody(t, "dave joins again with k2", wp07Join(t, rig, s.k2, dave.bearer),
		http.StatusNotFound, `{"error":"invite key not recognized"}`)
	wp07WantStatus(t, "dave lists jx", wp07Get(t, rig, "/v1/repos/jx/pages", dave.bearer), http.StatusForbidden)
	if fired != 1 {
		t.Errorf("the member quota ran %d times, want 1 (dave's first join only)", fired)
	}
}

// TestStaleAccess_JoinWaitsForCreateRepoMu: a join decides under
// createRepoMu, so it cannot interleave with a creation of the key's repo.
// The test holds createRepoMu: a stale member's and a non-member's joins with
// the stale k2 must wait for it, then find the key purged by the creation
// the test makes while holding the lock.
func TestStaleAccess_JoinWaitsForCreateRepoMu(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	bob, carol, dave := us["bob"], us["carol"], us["dave"]
	s := wp115SeedDeleted(t, rig, us, "jw")

	var bobJoin, daveJoin <-chan *httptest.ResponseRecorder
	waited := false
	func() {
		createRepoMu.Lock()
		defer createRepoMu.Unlock()
		bobJoin = wp115Async(t, func() *httptest.ResponseRecorder { return wp07Join(t, rig, s.k2, bob.bearer) })
		daveJoin = wp115Async(t, func() *httptest.ResponseRecorder { return wp07Join(t, rig, s.k2, dave.bearer) })
		waited = wp115AwaitLockWaiters(t, "(*Handler).JoinRepo", 2, bobJoin, daveJoin)
		wp115EmulateCreate(t, rig, s.repo, carol.id)
	}()
	wBob := wp115Recv(t, "bob joins jw", bobJoin)
	wDave := wp115Recv(t, "dave joins jw", daveJoin)
	if !waited {
		t.Errorf("join did not wait for createRepoMu: bob answered %d %s, dave answered %d %s",
			wBob.Code, wBob.Body.String(), wDave.Code, wDave.Body.String())
	}
	const unknown = `{"error":"invite key not recognized"}`
	wp115WantBody(t, "bob (stale member) joins with k2", wBob, http.StatusNotFound, unknown)
	wp115WantBody(t, "dave joins with k2", wDave, http.StatusNotFound, unknown)
	wp07WantMembers(t, "jw", rig, s.repo, map[string]string{carol.id: userstore.RoleOwner})
}

// TestStaleAccess_InviteWritesUnderCreateRepoMu pins deterministically that
// every invite-key write (mint, join, delete) runs under createRepoMu:
// triggers record, for each insert or delete, whether createRepoMu is held
// when SQLite runs it (the WP-0.7 lock probe).
func TestStaleAccess_InviteWritesUnderCreateRepoMu(t *testing.T) {
	rig := setupRig(t)
	wp115WireInviteKeyRoutes(rig)
	us := wp07SignIn(t, rig, "alice", "bob")
	alice, bob := us["alice"], us["bob"]
	wp07WantCreated(t, "alice creates probe", wp07Create(t, rig, "probe", alice.bearer), "probe")

	db := rig.users.DB()
	for _, stmt := range []string{
		`CREATE TABLE wp115_lock_probe (step TEXT NOT NULL, held INTEGER NOT NULL)`,
		`CREATE TRIGGER wp115_probe_member AFTER INSERT ON repo_members
BEGIN INSERT INTO wp115_lock_probe VALUES ('insert member ' || NEW.role, ` + wp07LockProbeFunc + `()); END`,
		`CREATE TRIGGER wp115_probe_key_insert AFTER INSERT ON repo_invite_keys
BEGIN INSERT INTO wp115_lock_probe VALUES ('insert key', ` + wp07LockProbeFunc + `()); END`,
		`CREATE TRIGGER wp115_probe_key_delete BEFORE DELETE ON repo_invite_keys
BEGIN INSERT INTO wp115_lock_probe VALUES ('delete key', ` + wp07LockProbeFunc + `()); END`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("install the lock probe: %v", err)
		}
	}

	m := wp115WantMinted(t, "alice mints",
		doJSON(t, rig.router, http.MethodPost, "/v1/repos/probe/invite-keys", alice.bearer, map[string]string{"label": "probe"}), "probe")
	wp07WantStatus(t, "bob joins", wp07Join(t, rig, m.Token, bob.bearer), http.StatusOK)
	wp07WantStatus(t, "alice deletes the key",
		doJSON(t, rig.router, http.MethodDelete, "/v1/repos/probe/invite-keys/"+m.Key.ID, alice.bearer, nil), http.StatusNoContent)

	rows, err := db.Query(`SELECT step, held FROM wp115_lock_probe ORDER BY rowid`)
	if err != nil {
		t.Fatalf("read the lock probe: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var step string
		var held int
		if err := rows.Scan(&step, &held); err != nil {
			t.Fatalf("scan the lock probe: %v", err)
		}
		got = append(got, fmt.Sprintf("%s held=%d", step, held))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the lock probe: %v", err)
	}
	want := []string{"insert key held=1", "insert member member held=1", "delete key held=1"}
	if !slices.Equal(got, want) {
		t.Errorf("invite-key writes and whether createRepoMu was held:\n got %q\nwant %q", got, want)
	}
}

// TestStaleAccess_MintStraddlingRecreateNeverMints: MintInviteKey checked
// the owner row, then read its body, then inserted the key, without
// createRepoMu. A stale owner whose body arrived while the id was re-created
// got a live key for the NEW repo. The seam is the body: its first Read
// starts carol's re-create and waits until it answered or waits for
// createRepoMu.
func TestStaleAccess_MintStraddlingRecreateNeverMints(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	alice, bob, carol := us["alice"], us["bob"], us["carol"]
	s := wp115SeedDeleted(t, rig, us, "mx")

	var create *httptest.ResponseRecorder
	var createDone <-chan *httptest.ResponseRecorder
	body := &wp115HookBody{payload: []byte(`{"label":"straddle"}`), hook: func() {
		createDone = wp115RecreateAsync(t, rig, s.repo, carol.bearer)
		create = wp115CreateOrWait(createDone)
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/repos/mx/invite-keys", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+alice.bearer)
	w := httptest.NewRecorder()
	rig.router.ServeHTTP(w, req)
	if !body.fired {
		t.Fatalf("alice's mint never read its body (%d %s): the seam is broken", w.Code, w.Body.String())
	}
	if create == nil {
		create = wp115Recv(t, "carol re-creates mx", createDone)
	}

	wp07WantCreated(t, "carol re-creates mx during alice's mint", create, "mx")
	wp07WantKeys(t, "mx", rig, s.repo, 0)
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &minted)
	if minted.Token != "" {
		wp07WantResolves(t, "alice's straddling key", rig, minted.Token, "")
		wp115WantBody(t, "bob joins with alice's straddling key", wp07Join(t, rig, minted.Token, bob.bearer),
			http.StatusNotFound, `{"error":"invite key not recognized"}`)
	}
	wp115WantBody(t, "alice mints again for mx",
		doJSON(t, rig.router, http.MethodPost, "/v1/repos/mx/invite-keys", alice.bearer, map[string]string{"label": "again"}),
		http.StatusForbidden, `{"error":"not a member of this repo"}`)
	wp07WantMembers(t, "mx", rig, s.repo, map[string]string{carol.id: userstore.RoleOwner})
}

// TestStaleAccess_BodiesReadBeforeCreateRepoMu: JoinRepo and MintInviteKey
// read their request body before they take createRepoMu. A body read under
// it would let one client that trickles its body (up to the server's read
// timeout) stall every creation, push membership check and invite-key
// request in the process. The body's first Read checks that nothing holds
// the lock; the request runs on the test goroutine and nothing else takes it.
func TestStaleAccess_BodiesReadBeforeCreateRepoMu(t *testing.T) {
	rig := setupRig(t)
	us := wp07SignIn(t, rig, "alice", "bob")
	alice, bob := us["alice"], us["bob"]
	wp07WantCreated(t, "alice creates bodies", wp07Create(t, rig, "bodies", alice.bearer), "bodies")
	key := wp115Mint(t, rig, "bodies", alice.bearer, "k")

	for _, tc := range []struct {
		name, path, bearer, payload string
		status                      int
	}{
		{"join", "/v1/repos/join", bob.bearer, `{"key":"` + key + `"}`, http.StatusOK},
		{"mint", "/v1/repos/bodies/invite-keys", alice.bearer, `{"label":"body"}`, http.StatusCreated},
	} {
		body := &wp115HookBody{payload: []byte(tc.payload), hook: func() {
			if !createRepoMu.TryLock() {
				t.Errorf("%s read its request body while createRepoMu was held", tc.name)
				return
			}
			createRepoMu.Unlock()
		}}
		req := httptest.NewRequest(http.MethodPost, tc.path, body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tc.bearer)
		w := httptest.NewRecorder()
		rig.router.ServeHTTP(w, req)
		if !body.fired {
			t.Errorf("%s never read its body (%d %s): the seam is broken", tc.name, w.Code, w.Body.String())
		}
		if w.Code != tc.status {
			t.Errorf("%s: %d %s, want %d", tc.name, w.Code, w.Body.String(), tc.status)
		}
	}
}

// TestStaleAccess_MintWaitsForCreateRepoMu: a mint checks the owner row and
// inserts the key under createRepoMu. The test holds createRepoMu: a stale
// owner's mint must wait for it, then be refused by the creation the test
// makes while holding the lock.
func TestStaleAccess_MintWaitsForCreateRepoMu(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	alice, carol := us["alice"], us["carol"]
	s := wp115SeedDeleted(t, rig, us, "mw")

	var mint <-chan *httptest.ResponseRecorder
	waited := false
	func() {
		createRepoMu.Lock()
		defer createRepoMu.Unlock()
		mint = wp115Async(t, func() *httptest.ResponseRecorder {
			return doJSON(t, rig.router, http.MethodPost, "/v1/repos/mw/invite-keys", alice.bearer, map[string]string{"label": "late"})
		})
		waited = wp115AwaitLockWaiters(t, "(*Handler).MintInviteKey", 1, mint)
		wp115EmulateCreate(t, rig, s.repo, carol.id)
	}()
	w := wp115Recv(t, "alice mints for mw", mint)
	if !waited {
		t.Errorf("mint did not wait for createRepoMu: it answered %d %s", w.Code, w.Body.String())
	}
	wp115WantBody(t, "alice (stale owner) mints for mw", w, http.StatusForbidden, `{"error":"not a member of this repo"}`)
	wp07WantKeys(t, "mw", rig, s.repo, 0)
	wp07WantMembers(t, "mw", rig, s.repo, map[string]string{carol.id: userstore.RoleOwner})
}

// TestStaleAccess_OwnerKeyRoutesWaitForCreateRepoMu: listing and deleting
// invite keys check the owner row and act under createRepoMu, so a stale
// owner can neither read the new owner's key metadata nor delete a key of
// the new repo. The test holds createRepoMu while both requests wait, makes
// carol's creation and her live key, then releases it.
func TestStaleAccess_OwnerKeyRoutesWaitForCreateRepoMu(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	wp115WireInviteKeyRoutes(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	alice, carol := us["alice"], us["carol"]
	s := wp115SeedDeleted(t, rig, us, "ow")
	keys, err := rig.users.ListInviteKeys(s.repo)
	if err != nil {
		t.Fatalf("list ow keys: %v", err)
	}
	k2ID := ""
	for _, k := range keys {
		if k.Label == "k2" {
			k2ID = k.ID
		}
	}
	if k2ID == "" {
		t.Fatalf("no k2 among the ow keys %+v", keys)
	}

	var list, del <-chan *httptest.ResponseRecorder
	waitedList, waitedDel := false, false
	carolKey := ""
	func() {
		createRepoMu.Lock()
		defer createRepoMu.Unlock()
		list = wp115Async(t, func() *httptest.ResponseRecorder { return wp07Get(t, rig, "/v1/repos/ow/invite-keys", alice.bearer) })
		del = wp115Async(t, func() *httptest.ResponseRecorder {
			return doJSON(t, rig.router, http.MethodDelete, "/v1/repos/ow/invite-keys/"+k2ID, alice.bearer, nil)
		})
		waitedList = wp115AwaitLockWaiters(t, "(*Handler).ListInviteKeys", 1, list, del)
		waitedDel = wp115AwaitLockWaiters(t, "(*Handler).DeleteInviteKey", 1, list, del)
		wp115EmulateCreate(t, rig, s.repo, carol.id)
		_, raw, err := rig.users.MintInviteKey(s.repo, carol.id, "carol")
		if err != nil {
			t.Errorf("mint carol's key: %v", err)
		}
		carolKey = raw
	}()
	wList := wp115Recv(t, "alice lists ow keys", list)
	wDel := wp115Recv(t, "alice deletes k2", del)
	if !waitedList || !waitedDel {
		t.Errorf("owner key routes did not wait for createRepoMu (list waited %v, delete waited %v): list answered %d %s, delete answered %d %s",
			waitedList, waitedDel, wList.Code, wList.Body.String(), wDel.Code, wDel.Body.String())
	}
	const notMember = `{"error":"not a member of this repo"}`
	wp115WantBody(t, "alice (stale owner) lists ow keys", wList, http.StatusForbidden, notMember)
	wp115WantBody(t, "alice (stale owner) deletes k2", wDel, http.StatusForbidden, notMember)
	if carolKey != "" {
		wp07WantResolves(t, "carol's key", rig, carolKey, "ow")
	}
}

// TestInviteRoutes_ResponsesUnchanged pins every non-attack response of the
// invite-key routes byte for byte (the CLI and the dashboard read them).
// Expired keys (410) are pinned by TestE2E_InviteKeyExpiry and the member
// cap (402) by TestJoinRepo_QuotaBlocksOverCapWith402.
func TestInviteRoutes_ResponsesUnchanged(t *testing.T) {
	rig := setupRig(t)
	wp115WireInviteKeyRoutes(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "dave")
	alice, bob, dave := us["alice"], us["bob"], us["dave"]
	wp07WantCreated(t, "alice creates pin", wp07Create(t, rig, "pin", alice.bearer), "pin")
	mintAs := func(bearer string, body any) *httptest.ResponseRecorder {
		return doJSON(t, rig.router, http.MethodPost, "/v1/repos/pin/invite-keys", bearer, body)
	}

	m := wp115WantMinted(t, "alice mints", mintAs(alice.bearer, map[string]string{"label": "team"}), "team")
	wp115WantBody(t, "alice lists", wp07Get(t, rig, "/v1/repos/pin/invite-keys", alice.bearer),
		http.StatusOK, `{"keys":[`+wp115KeyJSON(m)+`]}`)

	const joined = `{"repo_id":"pin","role":"member"}`
	wp115WantBody(t, "bob joins", wp07Join(t, rig, m.Token, bob.bearer), http.StatusOK, joined)
	wp115WantBody(t, "bob joins again", wp07Join(t, rig, m.Token, bob.bearer), http.StatusOK, joined)
	wp115WantBody(t, "alice joins her own key", wp07Join(t, rig, m.Token, alice.bearer),
		http.StatusOK, `{"repo_id":"pin","role":"owner"}`)
	wp115WantBody(t, "unknown key", wp07Join(t, rig, "ctxi_unknown", dave.bearer),
		http.StatusNotFound, `{"error":"invite key not recognized"}`)
	wp115WantBody(t, "empty key", wp07Join(t, rig, "", dave.bearer), http.StatusBadRequest, `{"error":"key required"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/repos/join", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+dave.bearer)
	w := httptest.NewRecorder()
	rig.router.ServeHTTP(w, req)
	wp115WantBody(t, "malformed join body", w, http.StatusBadRequest, `{"error":"key required"}`)
	wp115WantBody(t, "legacy key joins", wp07Join(t, rig, m.Token, "legacy-key"),
		http.StatusForbidden, `{"error":"user session required"}`)

	wp115WantBody(t, "bob (member) mints", mintAs(bob.bearer, map[string]string{"label": "x"}),
		http.StatusForbidden, `{"error":"owner role required"}`)
	wp115WantBody(t, "dave (non-member) mints", mintAs(dave.bearer, map[string]string{"label": "x"}),
		http.StatusForbidden, `{"error":"not a member of this repo"}`)
	wp115WantBody(t, "legacy key mints", mintAs("legacy-key", map[string]string{"label": "x"}),
		http.StatusForbidden, `{"error":"user session required to mint invite keys"}`)
	wp115WantBody(t, "bob (member) lists", wp07Get(t, rig, "/v1/repos/pin/invite-keys", bob.bearer),
		http.StatusForbidden, `{"error":"owner role required"}`)
	wp115WantBody(t, "dave (non-member) lists", wp07Get(t, rig, "/v1/repos/pin/invite-keys", dave.bearer),
		http.StatusForbidden, `{"error":"not a member of this repo"}`)
	wp115WantBody(t, "bob (member) deletes",
		doJSON(t, rig.router, http.MethodDelete, "/v1/repos/pin/invite-keys/"+m.Key.ID, bob.bearer, nil),
		http.StatusForbidden, `{"error":"owner role required"}`)

	// A mint without a body is not an error: the label is empty.
	empty := wp115WantMinted(t, "alice mints without a body", mintAs(alice.bearer, nil), "")
	wp115WantBody(t, "alice deletes the unlabelled key",
		doJSON(t, rig.router, http.MethodDelete, "/v1/repos/pin/invite-keys/"+empty.Key.ID, alice.bearer, nil),
		http.StatusNoContent, "")

	wp115WantBody(t, "alice deletes",
		doJSON(t, rig.router, http.MethodDelete, "/v1/repos/pin/invite-keys/"+m.Key.ID, alice.bearer, nil),
		http.StatusNoContent, "")
	wp115WantBody(t, "alice deletes again",
		doJSON(t, rig.router, http.MethodDelete, "/v1/repos/pin/invite-keys/"+m.Key.ID, alice.bearer, nil),
		http.StatusNotFound, `{"error":"invite key not found"}`)
	wp07WantKeys(t, "pin", rig, "pin", 0)
	wp07WantMembers(t, "pin", rig, "pin", map[string]string{alice.id: userstore.RoleOwner, bob.id: userstore.RoleMember})
}
