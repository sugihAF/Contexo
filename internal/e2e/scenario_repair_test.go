package e2e

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// rp is the helper prefix of this file (scenarios 06 and 06b).

// rpServerCaps is server_capabilities of a repair run against a v1 server.
var rpServerCaps = []contract.ServerCapability{
	contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA,
}

// rpDropBase deletes rel's base file and bases.json record, leaving
// state.json alone.
func rpDropBase(t testing.TB, u *e2eUser, rel string) {
	t.Helper()
	b, err := syncstate.OpenBases(u.Hub)
	if err != nil {
		t.Fatalf("open bases: %v", err)
	}
	if err := b.Delete(rel); err != nil {
		t.Fatalf("delete base of %s: %v", rel, err)
	}
	if err := b.Save(); err != nil {
		t.Fatalf("save bases: %v", err)
	}
}

// rpRepair runs ctx repair --json (plus --dry-run) and strictly decodes
// the ctx.repair.v1 document.
func rpRepair(t testing.TB, u *e2eUser, dryRun bool) syncops.RepairResult {
	t.Helper()
	args := []string{"repair"}
	if dryRun {
		args = append(args, "--dry-run")
	}
	var res syncops.RepairResult
	assertOK(t, runCtx(t, u, append(args, "--json")...), &res)
	return res
}

// rpPushedSHA returns the sha doc pushed rel at.
func rpPushedSHA(t testing.TB, doc contract.PushDoc, rel string) string {
	t.Helper()
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != rel || doc.Pushed[0].SHA == "" {
		t.Fatalf("pushed = %+v, want exactly %s with a sha", doc.Pushed, rel)
	}
	return doc.Pushed[0].SHA
}

// rpPOSTs counts every POST the proxy recorded.
func rpPOSTs(px *recordingProxy) int { return px.Count(http.MethodPost, "") }

// TestScenario06_RepairBuggyState: state.json holding the repo HEAD for a
// page (the old gitstore no-op-write sha bug) with its base lost is fixed by
// ctx repair (the dry run writes nothing); status then shows no false
// incoming. A lost bases.json is unverified offline (row 12) and repaired by
// online status.
func TestScenario06_RepairBuggyState(t *testing.T) {
	srv := startServer(t)
	aspec := conceptPage("repair-a", "Page A.")
	bspec := conceptPage("repair-b", "Page B.")
	relA, relB := pageRel(aspec), pageRel(bspec)
	alice := newUser(t, srv, "alice", withPages(aspec, bspec))

	shaA := rpPushedSHA(t, mustPush(t, alice, "--yes", "--", relA), relA)
	shaB := rpPushedSHA(t, mustPush(t, alice, "--yes", "--", relB), relB)
	head := serverHead(t, srv, defaultRepo)
	if head != shaB || head == shaA {
		t.Fatalf("server HEAD %s, want B's commit %s (one that did not touch A %s)", head, shaB, shaA)
	}

	// The buggy state: page_shas[A] = repo HEAD and A's base gone.
	setPageSHA(t, alice, relA, head)
	rpDropBase(t, alice, relA)
	if _, ok := readBases(t, alice)[relA]; ok {
		t.Fatalf("bases.json still has %s", relA)
	}
	off := mustStatus(t, alice, true)
	if e := mustPage(t, off, relA); e.State != contract.StateUnverified || e.Base == nil || e.Base.SHA != head || !e.Base.Stale {
		t.Errorf("offline %s = %s base %+v, want unverified with stale base %s", relA, e.State, e.Base, head)
	}

	// repair --dry-run reports the fix and writes nothing.
	stateBefore := readHubFile(t, alice, ".sync/state.json")
	basesBefore := readHubFile(t, alice, ".sync/bases.json")
	want := syncops.RepairResult{
		Schema:             contract.SchemaRepair,
		DryRun:             true,
		ServerCapabilities: rpServerCaps,
		Backfilled:         []syncops.RepairBackfilled{},
		Repaired:           []syncops.RepairedPage{{Path: relA, OldSHA: head, NewSHA: shaA, Reason: "path_sha_fixed"}},
		Skipped:            []syncops.RepairSkipped{},
		Unverified:         []string{},
		Warnings:           []contract.Warning{},
	}
	if got := rpRepair(t, alice, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("repair --dry-run =\n%+v\nwant\n%+v", got, want)
	}
	if got := readHubFile(t, alice, ".sync/state.json"); !bytes.Equal(got, stateBefore) {
		t.Errorf("repair --dry-run changed state.json:\n%s\nwas\n%s", got, stateBefore)
	}
	if got := readHubFile(t, alice, ".sync/bases.json"); !bytes.Equal(got, basesBefore) {
		t.Errorf("repair --dry-run changed bases.json:\n%s\nwas\n%s", got, basesBefore)
	}
	if _, err := os.Stat(syncstate.BaseFile(alice.Hub, relA)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("repair --dry-run wrote base/%s (stat err %v)", relA, err)
	}

	// repair moves A to its own last-touch sha and stores its base.
	want.DryRun = false
	if got := rpRepair(t, alice, false); !reflect.DeepEqual(got, want) {
		t.Fatalf("repair =\n%+v\nwant\n%+v", got, want)
	}
	if sha := loadState(t, alice).PageSHAs[relA]; sha != shaA {
		t.Errorf("page_shas[%s] = %s after repair, want %s", relA, sha, shaA)
	}
	if b := readBases(t, alice)[relA]; b.SHA != shaA {
		t.Errorf("bases.json[%s].sha = %s after repair, want %s", relA, b.SHA, shaA)
	}

	// No false incoming.
	st := mustStatus(t, alice, false)
	for rel, sha := range map[string]string{relA: shaA, relB: shaB} {
		e := mustPage(t, st, rel)
		if e.State != contract.StateUnchanged || e.Base == nil || e.Base.SHA != sha || e.Base.Stale ||
			e.Remote == nil || e.Remote.SHA != sha {
			t.Errorf("%s = %s base %+v remote %+v, want unchanged at %s", rel, e.State, e.Base, e.Remote, sha)
		}
	}
	if want := (contract.StateCounts{Unchanged: 2}); st.Counts != want || len(st.Warnings) != 0 {
		t.Errorf("counts %+v warnings %+v, want %+v and no warning", st.Counts, st.Warnings, want)
	}

	// Skew: a lost bases.json is unverified offline (row 12)...
	if err := os.Remove(syncstate.BasesPath(alice.Hub)); err != nil {
		t.Fatal(err)
	}
	off = mustStatus(t, alice, true)
	for rel, sha := range map[string]string{relA: shaA, relB: shaB} {
		e := mustPage(t, off, rel)
		if e.State != contract.StateUnverified || e.Base == nil || e.Base.SHA != sha || !e.Base.Stale || e.Remote != nil {
			t.Errorf("offline %s = %s base %+v remote %+v, want unverified with stale base %s and no remote",
				rel, e.State, e.Base, e.Remote, sha)
		}
	}
	if off.Counts.Unverified != 2 || off.Counts.Unchanged != 0 {
		t.Errorf("offline counts = %+v, want unverified 2", off.Counts)
	}
	if _, err := os.Stat(syncstate.BasesPath(alice.Hub)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("status --offline wrote bases.json (stat err %v)", err)
	}

	// ...and online status repairs it.
	st = mustStatus(t, alice, false)
	for rel, sha := range map[string]string{relA: shaA, relB: shaB} {
		if e := mustPage(t, st, rel); e.State != contract.StateUnchanged || e.Base == nil || e.Base.SHA != sha || e.Base.Stale {
			t.Errorf("online %s = %s base %+v, want unchanged at %s", rel, e.State, e.Base, sha)
		}
	}
	if want := (contract.StateCounts{Unchanged: 2}); st.Counts != want || len(st.Warnings) != 0 {
		t.Errorf("online counts %+v warnings %+v, want %+v and no warning", st.Counts, st.Warnings, want)
	}
	bases := readBases(t, alice)
	if bases[relA].SHA != shaA || bases[relB].SHA != shaB {
		t.Errorf("bases.json after online status = %+v, want %s@%s and %s@%s", bases, relA, shaA, relB, shaB)
	}
}

// rpBump is the E11 case (3) setup: alice pushed P at A, bob pushed his
// edit at S2, alice has an unmerged local edit and an old MCP bumped her
// page_shas[P] to S2 after a 409 without touching bases.json.
type rpBump struct {
	srv        *e2eServer
	alice      *e2eUser
	rel        string
	shaA, sha2 string
	aliceBytes []byte
	server     []byte // the server bytes at S2
}

func rpSetupBump(t *testing.T) *rpBump {
	t.Helper()
	srv := startServer(t)
	spec := conceptPage("bumped", "P v1.")
	rel := pageRel(spec)
	alice := newUser(t, srv, "alice", withPages(spec))
	shaA := rpPushedSHA(t, mustPush(t, alice), rel)

	bob := newUser(t, srv, "bob")
	mustPull(t, bob)
	editPage(t, bob, rel, "Bob's teammate edit.")
	sha2 := rpPushedSHA(t, mustPush(t, bob, "--yes"), rel)

	editPage(t, alice, rel, "Alice's unmerged agent edit.")
	basesBefore := readHubFile(t, alice, ".sync/bases.json")
	setPageSHA(t, alice, rel, sha2)
	if got := readHubFile(t, alice, ".sync/bases.json"); !bytes.Equal(got, basesBefore) {
		t.Fatalf("the bump touched bases.json")
	}
	if b := readBases(t, alice)[rel]; b.SHA != shaA {
		t.Fatalf("bases.json[%s].sha = %s, want the older valid base %s", rel, b.SHA, shaA)
	}
	return &rpBump{srv: srv, alice: alice, rel: rel, shaA: shaA, sha2: sha2,
		aliceBytes: alice.P.Read(rel), server: serverContent(t, srv, defaultRepo, rel)}
}

// rpCheckKeptBase checks the E11 (3) result: page_shas[P] back at A, the
// file untouched and a repair sidecar recording S2 over A.
func rpCheckKeptBase(t testing.TB, b *rpBump) {
	t.Helper()
	if sha := loadState(t, b.alice).PageSHAs[b.rel]; sha != b.shaA {
		t.Errorf("page_shas[%s] = %s, want the kept base %s (E11 case 3)", b.rel, sha, b.shaA)
	}
	if got := b.alice.P.Read(b.rel); !bytes.Equal(got, b.aliceBytes) {
		t.Errorf("alice's %s changed:\n%s", b.rel, got)
	}
	rec, ok := readConflicts(t, b.alice)[b.rel]
	if !ok {
		t.Fatalf("conflicts.json has no record for %s (E11 case 3 writes a repair sidecar)", b.rel)
	}
	if rec.Source != "repair" || rec.ServerSHA != b.sha2 || rec.BaseSHA != b.shaA ||
		rec.Kind != string(contract.ConflictBothModified) || !rec.HasContent {
		t.Errorf("conflicts.json[%s] = %+v, want source repair, server %s, base %s, both_modified, content",
			b.rel, rec, b.sha2, b.shaA)
	}
	side, err := os.ReadFile(syncstate.ConflictFile(b.alice.Hub, b.rel))
	if err != nil || !bytes.Equal(side, b.server) {
		t.Errorf(".sync/conflicts/%s = %q (err %v), want the server bytes at %s", b.rel, side, err, b.sha2)
	}
}

// rpCheckPushSkips runs the extension's push and checks that it sends
// nothing for P and lists it skipped with reason conflict.
func rpCheckPushSkips(t testing.TB, b *rpBump) {
	t.Helper()
	posts, files := rpPOSTs(b.srv.Proxy), len(b.srv.Proxy.PushedFiles())
	doc := mustPush(t, b.alice, "--yes")
	want := contract.SkippedPage{Path: b.rel, State: contract.StateConflict, Reason: contract.SkipConflict, ServerSHA: b.sha2}
	if len(doc.Pushed) != 0 || len(doc.Conflicts) != 0 || len(doc.Skipped) != 1 || doc.Skipped[0] != want || doc.NewHead != "" {
		t.Errorf("push = pushed %+v conflicts %+v skipped %+v new_head %q, want only skipped [%+v]",
			doc.Pushed, doc.Conflicts, doc.Skipped, doc.NewHead, want)
	}
	if got := rpPOSTs(b.srv.Proxy); got != posts {
		t.Errorf("push made %d POST(s), want 0", got-posts)
	}
	if got := b.srv.Proxy.PushedFiles()[files:]; len(got) != 0 {
		t.Errorf("proxy saw push files %+v, want none", got)
	}
	if sha := serverSHA(t, b.srv, defaultRepo, b.rel); sha != b.sha2 {
		t.Errorf("server sha of %s = %s, want bob's %s untouched", b.rel, sha, b.sha2)
	}
	if got := serverContent(t, b.srv, defaultRepo, b.rel); !bytes.Equal(got, b.server) {
		t.Errorf("server content of %s changed: %q", b.rel, got)
	}
}

// TestScenario06b_OldMCPBumpIsConflict (E11 case 3, Review Focus): an old
// MCP bumped page_shas[P] to the teammate's sha over an unmerged local edit.
// The page is a conflict, never modified, and the extension's push never
// overwrites the teammate. status_then_push is the plan flow;
// push_without_status checks that push's own EnsureBase decides the same.
func TestScenario06b_OldMCPBumpIsConflict(t *testing.T) {
	t.Run("status_then_push", func(t *testing.T) {
		b := rpSetupBump(t)
		st := mustStatus(t, b.alice, false)
		e := mustPage(t, st, b.rel)
		if e.State != contract.StateConflict || e.ConflictKind != contract.ConflictBothModified {
			t.Fatalf("%s = %s/%s, want conflict/both_modified, never modified (E11 case 3)", b.rel, e.State, e.ConflictKind)
		}
		if e.Base == nil || e.Base.SHA != b.shaA || e.Base.Stale || e.Remote == nil || e.Remote.SHA != b.sha2 {
			t.Errorf("%s base %+v remote %+v, want base %s (kept) and remote %s", b.rel, e.Base, e.Remote, b.shaA, b.sha2)
		}
		if st.Counts.Conflict != 1 || st.Counts.Modified != 0 || st.Counts.Ahead != 0 || st.Counts.Unverified != 0 {
			t.Errorf("counts = %+v, want conflict 1, modified 0, ahead 0", st.Counts)
		}
		rpCheckKeptBase(t, b)
		rpCheckPushSkips(t, b)
	})
	t.Run("push_without_status", func(t *testing.T) {
		b := rpSetupBump(t)
		rpCheckPushSkips(t, b)
		rpCheckKeptBase(t, b)
		if e := mustPage(t, mustStatus(t, b.alice, true), b.rel); e.State != contract.StateConflict {
			t.Errorf("offline %s = %s after the push, want conflict", b.rel, e.State)
		}
	})
}
