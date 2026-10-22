package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/userstore"
)

// wp115WireReadRoutes mounts the member-gated read routes that neither
// setupRig nor wp02WireSync registers (timeline, history, diff, evolution)
// and the member removal on a new /v1 group of the rig.
func wp115WireReadRoutes(rig *testRig) {
	v1 := rig.router.Group("/v1")
	v1.Use(auth.GinMiddleware(rig.resolver.Validator()))
	v1.GET("/repos/:id/timeline", rig.h.Timeline)
	v1.GET("/repos/:id/history/*path", rig.h.History)
	v1.GET("/repos/:id/diff/*path", rig.h.Diff)
	v1.GET("/repos/:id/evolution/*path", rig.h.Evolution)
}

// wp115InstallGitGate puts a git wrapper first in PATH for the test. Once the
// directory gate exists, the first git process that starts parks until
// gate/release exists, and gate/entered tells the test it has parked; every
// other git process runs the real git at once. Arm it with os.Mkdir(gate).
// release (idempotent) lets a parked git go; defer it, so a failing test
// never leaves one parked. gitstore's reads take no repo lock, so a parked
// read blocks no write.
func wp115InstallGitGate(t *testing.T) (gate string, release func()) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the git gate is a POSIX shell wrapper")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("look up git: %v", err)
	}
	bin := t.TempDir()
	gate = filepath.Join(t.TempDir(), "gate")
	wrapper := "#!/bin/sh\n" +
		"if [ -d \"$WP115_GIT_GATE\" ] && mkdir \"$WP115_GIT_GATE/entered\" 2>/dev/null; then\n" +
		"  while [ ! -e \"$WP115_GIT_GATE/release\" ]; do sleep 0.01; done\n" +
		"fi\n" +
		"exec \"$WP115_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write the git gate: %v", err)
	}
	t.Setenv("WP115_GIT_GATE", gate)
	t.Setenv("WP115_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var once gosync.Once
	release = func() {
		once.Do(func() {
			if err := os.MkdirAll(gate, 0o755); err != nil {
				t.Errorf("release the git gate: %v", err)
				return
			}
			if err := os.WriteFile(filepath.Join(gate, "release"), nil, 0o644); err != nil {
				t.Errorf("release the git gate: %v", err)
			}
		})
	}
	return gate, release
}

// TestStaleAccess_ReadRoutesRecheckAfterRead: the member-gated read routes
// checked membership once and then read the repo. A former member of an
// operator-deleted id whose check saw its stale row just before a re-create
// purged it, and whose read ran after the new owner's first push, got the
// new repo's pages, history and diffs (and a pull recorded the former
// member in the new feed). Deterministic: a write transaction the test holds
// stops carol's re-create between gitstore.Create and its purge, so bob's
// check sees his stale row; bob's read then parks at its first git process
// (the git gate) while the test lets the purge and the owner row commit and
// carol pushes two versions of wiki/c.md; then bob's read goes on. Every
// route must answer bob as a non-member, and write nothing for him.
func TestStaleAccess_ReadRoutesRecheckAfterRead(t *testing.T) {
	routes := []struct{ name, path string }{
		{"pages", "/pages"},
		{"page", "/pages/wiki/c.md"},
		{"pull", "/sync/pull"},
		{"timeline", "/timeline"},
		{"history", "/history/wiki/c.md"},
		{"diff", "/diff/wiki/c.md"},
		{"evolution", "/evolution/wiki/c.md"},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			rig := setupRig(t)
			wp02WireSync(rig)
			wp115WireReadRoutes(rig)
			us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
			bob, carol := us["bob"], us["carol"]
			gate, releaseGate := wp115InstallGitGate(t)
			defer releaseGate()
			s := wp115SeedDeleted(t, rig, us, "rr")

			release := wp115HoldUserstoreWrite(t, rig)
			defer release()
			cw := wp115RecreateAsync(t, rig, s.repo, carol.bearer)
			// gitstore.Create makes .git with git init and then runs two git
			// config; the identity it writes last is the sign that Create ran
			// its last git process and the create waits in its purge. Arming
			// the gate before that would park carol's git config instead of
			// bob's read.
			created := func() bool {
				b, err := os.ReadFile(filepath.Join(rig.store.Root, s.repo, ".git", "config"))
				return err == nil && strings.Contains(string(b), "contexo-server")
			}
			for deadline := time.Now().Add(10 * time.Second); !created(); time.Sleep(time.Millisecond) {
				if len(cw) > 0 || time.Now().After(deadline) {
					release()
					w := wp115Recv(t, "carol re-creates rr", cw)
					t.Fatalf("carol's re-create answered %d %s without rr created while the userstore was held", w.Code, w.Body.String())
				}
			}
			if err := os.Mkdir(gate, 0o755); err != nil {
				t.Fatalf("arm the git gate: %v", err)
			}
			read := wp115Async(t, func() *httptest.ResponseRecorder {
				return wp07Get(t, rig, "/v1/repos/"+s.repo+rt.path, bob.bearer)
			})
			for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
				if _, err := os.Stat(filepath.Join(gate, "entered")); err == nil {
					break
				}
				if len(read) > 0 || time.Now().After(deadline) {
					release()
					releaseGate()
					w := wp115Recv(t, "bob reads", read)
					t.Fatalf("bob's read did not reach git while the create was held: %d %s", w.Code, w.Body.String())
				}
			}

			release()
			wp07WantCreated(t, "carol re-creates rr", wp115Recv(t, "carol re-creates rr", cw), "rr")
			wp07MustPush(t, rig, s.repo, carol.bearer, "wiki/c.md", "## Carol\ncarol v1\n")
			wp07MustPush(t, rig, s.repo, carol.bearer, "wiki/c.md", "## Carol\ncarol v2\n")
			releaseGate()

			wp115WantBody(t, "bob (stale member) "+rt.name+" straddling the re-create", wp115Recv(t, "bob reads", read),
				http.StatusForbidden, `{"error":"not a member of this repo"}`)
			wp07WantMembers(t, "rr", rig, s.repo, map[string]string{carol.id: userstore.RoleOwner})
			events, err := rig.users.ListActivity(s.repo, 100, 0)
			if err != nil {
				t.Fatalf("activity of rr: %v", err)
			}
			for _, e := range events {
				if e.UserID != carol.id {
					t.Errorf("rr's feed has a %q row of %s, want only carol's pushes", e.Action, e.Email)
				}
			}
		})
	}
}

// TestStaleAccess_MembersFloodDuringRecreate: ListMembers checked
// membership and then listed the members in a second statement. A former
// member or owner who kept asking while another user re-created the id got,
// about once per re-create, a 200 listing the new owner's user id and email:
// in WAL mode the check saw the stale row while the purge was open, and the
// list ran after the new owner row. Probabilistic (each round floods the
// route from 16 goroutines across one re-create); run with -count=50.
func TestStaleAccess_MembersFloodDuringRecreate(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	alice, bob, carol := us["alice"], us["bob"], us["carol"]
	const rounds = 8
	for r := 0; r < rounds; r++ {
		repo := fmt.Sprintf("ml-%02d", r)
		wp115SeedDeleted(t, rig, us, repo)

		stop := make(chan struct{})
		var (
			wg     gosync.WaitGroup
			mu     gosync.Mutex
			leaked []string
		)
		for g := 0; g < 8; g++ {
			for _, u := range []wp07User{alice, bob} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						default:
						}
						w := doJSON(t, rig.router, http.MethodGet, "/v1/repos/"+repo+"/members", u.bearer, nil)
						if w.Code == http.StatusOK && strings.Contains(w.Body.String(), carol.id) {
							mu.Lock()
							leaked = append(leaked, w.Body.String())
							mu.Unlock()
						}
					}
				}()
			}
		}
		time.Sleep(20 * time.Millisecond)
		created := wp07Create(t, rig, repo, carol.bearer)
		time.Sleep(20 * time.Millisecond)
		close(stop)
		wg.Wait()

		wp07WantCreated(t, repo+": carol re-creates", created, repo)
		if len(leaked) > 0 {
			t.Errorf("%s: %d member lists of the re-created repo went to its former members, e.g. %s", repo, len(leaked), leaked[0])
		}
		for name, u := range map[string]wp07User{"alice": alice, "bob": bob} {
			wp115WantBody(t, repo+": "+name+" lists the members after the re-create",
				doJSON(t, rig.router, http.MethodGet, "/v1/repos/"+repo+"/members", u.bearer, nil),
				http.StatusForbidden, `{"error":"not a member of this repo"}`)
		}
	}
}

// TestStaleAccess_RemoveMemberWaitsForCreateRepoMu: the owner check of
// DELETE /v1/repos/:id/members/:userId and the removal decide under
// createRepoMu, like the invite-key routes, so a stale owner's removal runs
// wholly before a re-create of the id or after its purge (403). The test
// holds createRepoMu, starts alice's (stale owner) removal of dave, waits
// until it waits for the lock, re-creates the id by hand with carol as owner
// and dave as a member, and releases.
func TestStaleAccess_RemoveMemberWaitsForCreateRepoMu(t *testing.T) {
	rig := setupRig(t)
	wp02WireSync(rig)
	us := wp07SignIn(t, rig, "alice", "bob", "carol", "dave")
	alice, carol, dave := us["alice"], us["carol"], us["dave"]
	s := wp115SeedDeleted(t, rig, us, "rmx")

	var removal <-chan *httptest.ResponseRecorder
	waited := func() bool {
		createRepoMu.Lock()
		defer createRepoMu.Unlock()
		removal = wp115Async(t, func() *httptest.ResponseRecorder {
			return doJSON(t, rig.router, http.MethodDelete, "/v1/repos/"+s.repo+"/members/"+dave.id, alice.bearer, nil)
		})
		ok := wp115AwaitLockWaiters(t, "(*Handler).RemoveMember", 1, removal)
		wp115EmulateCreate(t, rig, s.repo, carol.id)
		if err := rig.users.AddMember(s.repo, dave.id, userstore.RoleMember); err != nil {
			t.Errorf("dave joins the new rmx: %v", err)
		}
		return ok
	}()
	w := wp115Recv(t, "alice removes dave", removal)
	if !waited {
		t.Errorf("member removal did not wait for createRepoMu: it answered %d %s", w.Code, w.Body.String())
	}
	wp115WantBody(t, "alice (stale owner) removes dave from the re-created rmx", w,
		http.StatusForbidden, `{"error":"not a member of this repo"}`)
	wp07WantMembers(t, "rmx", rig, s.repo, map[string]string{carol.id: userstore.RoleOwner, dave.id: userstore.RoleMember})
}
