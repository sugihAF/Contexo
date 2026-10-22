package syncops

// Owner: WP-1.5. EnsureBase tests (E5/E11/E12). The rp15 fixture they use
// lives in repair_test.go.
//
// It is in package syncops (not syncops_test) on purpose: wave-1 tests drive
// an engine on syncopstest.FakeRemote while overriding unexported seams
// (ensureBaseStatusBudget, pushEnsureBase, pullEnsureBase, ...). syncopstest
// therefore must never import syncops; this file stops compiling (import
// cycle) if it does.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncops/syncopstest"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
)

// rp15AssertEnsureShape fails the test when res or one of its slices is nil.
func rp15AssertEnsureShape(t *testing.T, res *EnsureBaseResult) {
	t.Helper()
	if res == nil || res.Repaired == nil || res.Unverified == nil || res.Warnings == nil {
		t.Fatalf("EnsureBase result has a nil part: %+v", res)
	}
}

// ensure runs EnsureBase and fails the test on an error or a nil slice.
func (fx *rp15Fixture) ensure(ctx context.Context, o EnsureBaseOptions) *EnsureBaseResult {
	fx.t.Helper()
	res, err := EnsureBase(ctx, fx.env, o)
	if err != nil {
		fx.t.Fatalf("EnsureBase: %v", err)
	}
	rp15AssertEnsureShape(fx.t, res)
	return res
}

// fullSyncTree is the whole .sync tree, lock files included.
func (fx *rp15Fixture) fullSyncTree() map[string]string {
	fx.t.Helper()
	return rp15Tree(fx.t, syncstate.SyncDir(fx.hub), nil)
}

// rp15AssertEmpty asserts res reports nothing.
func rp15AssertEmpty(t *testing.T, what string, res *EnsureBaseResult) {
	t.Helper()
	rp15AssertEnsureShape(t, res)
	if len(res.Repaired)+len(res.Unverified)+len(res.Warnings) != 0 {
		t.Fatalf("%s: result is not empty: %+v", what, res)
	}
}

func TestEnsureBase_NoNetworkWhenComplete(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	for _, slug := range []string{"a", "b"} {
		rel, sha := fx.synced(slug)
		fx.putBase(rel, rp15Page(slug, "v1 of "+slug), sha)
	}
	fx.save()
	listing := fx.listing()
	before, tree := fx.requests(), fx.fullSyncTree()

	for _, locked := range []bool{false, true} {
		res := fx.ensure(context.Background(), EnsureBaseOptions{Listing: listing, Locked: locked})
		rp15AssertEmpty(t, fmt.Sprintf("Locked=%v", locked), res)
	}
	fx.assertDelta(before, map[string]int{})
	rp15AssertTree(t, ".sync", fx.fullSyncTree(), tree)
	fx.assertNoLock()
}

func TestEnsureBase_NilListingNoop(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, _ := fx.synced("a")
	fx.moved("b")
	fx.save()
	listing := fx.listing()
	before, tree := fx.requests(), fx.fullSyncTree()

	offline := fx.env
	noRemote := fx.env
	noRemote.Remote = nil
	cases := []struct {
		name string
		env  Env
		o    EnsureBaseOptions
	}{
		{"nil listing", offline, EnsureBaseOptions{}},
		{"nil listing, locked, paths", offline, EnsureBaseOptions{Paths: []string{a}, Locked: true}},
		{"nil remote", noRemote, EnsureBaseOptions{Listing: listing}},
		{"nil remote, locked", noRemote, EnsureBaseOptions{Listing: listing, Locked: true}},
	}
	for _, c := range cases {
		res, err := EnsureBase(context.Background(), c.env, c.o)
		if err != nil {
			t.Fatalf("%s: EnsureBase: %v", c.name, err)
		}
		rp15AssertEmpty(t, c.name, res)
		fx.assertDelta(before, map[string]int{})
		rp15AssertTree(t, c.name+": .sync", fx.fullSyncTree(), tree)
		fx.assertNoLock()
	}
}

func TestEnsureBase_SkipsWhenLocked(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, _ := fx.synced("a")
	b, _ := fx.moved("b")
	fx.save()
	listing := fx.listing()
	unlock, err := syncstate.Lock(fx.hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	before, tree := fx.requests(), fx.fullSyncTree()

	res := fx.ensure(context.Background(), EnsureBaseOptions{Listing: listing})
	stale := []string{a, b}
	if len(res.Repaired) != 0 || !reflect.DeepEqual(res.Unverified, stale) {
		t.Fatalf("result = %+v, want unverified %v", res, stale)
	}
	rp15Warning(t, res.Warnings, stale)
	fx.assertDelta(before, map[string]int{})
	rp15AssertTree(t, ".sync (the test's lock included)", fx.fullSyncTree(), tree)
}

func TestEnsureBase_LockedTrueDoesNotLock(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, ca := fx.synced("a")
	b, cb := fx.moved("b")
	fx.save()
	listing := fx.listing()
	unlock, err := syncstate.Lock(fx.hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	held, err := os.ReadFile(syncstate.LockPath(fx.hub))
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	res := fx.ensure(context.Background(), EnsureBaseOptions{Listing: listing, Locked: true})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("EnsureBase(Locked: true) took %v: it waited on the caller's lock", elapsed)
	}
	if !reflect.DeepEqual(res.Repaired, []string{a, b}) || len(res.Unverified) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("result = %+v, want a and b repaired", res)
	}
	fx.assertBase(a, ca, rp15Page("a", "v1 of a"))
	fx.assertBase(b, cb, rp15Page("b", "v1 of b"))
	if now, err := os.ReadFile(syncstate.LockPath(fx.hub)); err != nil || !bytes.Equal(now, held) {
		t.Fatalf("the caller's lock changed: %q (%v), want %q", now, err, held)
	}
}

func TestEnsureBase_ReleasesLock(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, ca := fx.synced("a")
	fx.save()

	res := fx.ensure(context.Background(), EnsureBaseOptions{Listing: fx.listing()})
	if !reflect.DeepEqual(res.Repaired, []string{a}) {
		t.Fatalf("result = %+v, want a repaired", res)
	}
	fx.assertBase(a, ca, rp15Page("a", "v1 of a"))
	fx.assertNoLock()
	unlock, err := syncstate.Lock(fx.hub, "x", 0)
	if err != nil {
		t.Fatalf("Lock after EnsureBase: %v", err)
	}
	unlock()
}

func TestEnsureBase_StatusBudget(t *testing.T) {
	const budget = 300 * time.Millisecond
	old := ensureBaseStatusBudget
	ensureBaseStatusBudget = budget
	t.Cleanup(func() { ensureBaseStatusBudget = old })
	fx := rp15New(t, syncopstest.FakeOptions{})
	paths, _ := fx.movedN(20)
	fx.save()
	listing := fx.listing()
	w := rp15Wrap(fx)
	w.delay = func(int) time.Duration { return 250 * time.Millisecond }

	start := time.Now()
	res := fx.ensure(context.Background(), EnsureBaseOptions{Listing: listing})
	elapsed := time.Since(start)
	callsAtReturn, _, _ := w.stats()
	tree := fx.fullSyncTree()

	// 20 calls of 250 ms at most 4 at a time need 1.25 s: only the budget
	// can end it this early, with paths left over.
	if elapsed > budget+time.Second {
		t.Fatalf("EnsureBase took %v with a %v status budget", elapsed, budget)
	}
	if len(res.Unverified) == 0 {
		t.Fatalf("nothing left unverified after %v: %+v", elapsed, res)
	}
	rp15Warning(t, res.Warnings, res.Unverified)
	got := append(append([]string{}, res.Repaired...), res.Unverified...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, paths) {
		t.Fatalf("repaired %v + unverified %v is not every stale path", res.Repaired, res.Unverified)
	}
	for _, p := range res.Repaired {
		if !fx.baseOK(p) {
			t.Fatalf("%s reported repaired without a valid base", p)
		}
	}
	for _, p := range res.Unverified {
		if fx.baseOK(p) {
			t.Fatalf("%s reported unverified but has a base", p)
		}
	}
	fx.assertNoLock()

	// The calls still in flight finish later; they must neither write nor
	// start new requests.
	time.Sleep(600 * time.Millisecond)
	rp15AssertTree(t, ".sync after EnsureBase returned", fx.fullSyncTree(), tree)
	if calls, _, _ := w.stats(); calls > callsAtReturn+repairMaxConcurrency {
		t.Fatalf("ReadPageAt calls went from %d to %d after EnsureBase returned", callsAtReturn, calls)
	}
}

func TestEnsureBase_ConcurrencyAtMostFour(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	paths, _ := fx.movedN(20)
	fx.save()
	listing := fx.listing()
	w := rp15Wrap(fx)
	w.delay = func(int) time.Duration { return 50 * time.Millisecond }

	res := fx.ensure(context.Background(), EnsureBaseOptions{Listing: listing})
	if !reflect.DeepEqual(res.Repaired, paths) || len(res.Unverified) != 0 {
		t.Fatalf("result = %+v", res)
	}
	calls, inflight, maxIn := w.stats()
	if calls != 20 || inflight != 0 {
		t.Fatalf("calls = %d, in flight = %d; want 20, 0", calls, inflight)
	}
	if maxIn > 4 || maxIn < 2 {
		t.Fatalf("max in flight = %d, want 2..4 (a fan-out capped at 4)", maxIn)
	}
}

// rp15HeadEqual runs EnsureBase over n HEAD-equal stale pages and asserts
// the requests (the test's own ListPages included) and that every page got
// its base.
func rp15HeadEqual(t *testing.T, n int, want map[string]int) {
	t.Helper()
	fx := rp15New(t, syncopstest.FakeOptions{})
	paths := []string{}
	for i := 0; i < n; i++ {
		rel, _ := fx.synced(fmt.Sprintf("h%02d", i))
		paths = append(paths, rel)
	}
	fx.save()
	before := fx.requests()
	res := fx.ensure(context.Background(), EnsureBaseOptions{Listing: fx.listing()})
	fx.assertDelta(before, want)
	if !reflect.DeepEqual(res.Repaired, paths) || len(res.Unverified) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("result = %+v, want every page repaired", res)
	}
	for _, p := range paths {
		if !fx.baseOK(p) {
			t.Fatalf("%s has no valid base", p)
		}
	}
}

func TestEnsureBase_EightOrFewerUseReadPage(t *testing.T) {
	rp15HeadEqual(t, 8, map[string]int{"ListPages": 1, "ReadPage": 8})
}

func TestEnsureBase_NineUseOnePullPages(t *testing.T) {
	rp15HeadEqual(t, 9, map[string]int{"ListPages": 1, "PullPages": 1})
}

func TestEnsureBase_MovedSinceListingGoesToPhaseTwo(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, c1 := fx.synced("a")
	fx.save()
	listing := fx.listing()       // a at c1: HEAD-equal
	c2 := fx.seed("a", "v2 of a") // the server moves on after the listing
	before := fx.requests()

	res := fx.ensure(context.Background(), EnsureBaseOptions{Listing: listing})
	fx.assertDelta(before, map[string]int{"ReadPage": 1, "ReadPageAt": 1})
	if !reflect.DeepEqual(res.Repaired, []string{a}) || len(res.Unverified) != 0 {
		t.Fatalf("result = %+v, want a repaired", res)
	}
	fx.assertBase(a, c1, rp15Page("a", "v1 of a"))
	if got := fx.classify(fx.listing(), a); got.State != contract.StateIncoming {
		t.Fatalf("a classifies %+v against the new listing (%s), want incoming", got, c2)
	}
}

func TestEnsureBase_PathsFilter(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, ca := fx.synced("a")
	b, _ := fx.synced("b")
	fx.save()
	listing := fx.listing()
	before := fx.requests()

	res := fx.ensure(context.Background(), EnsureBaseOptions{
		Listing: listing, Paths: []string{a, rp15Rel("not-a-page")},
	})
	if !reflect.DeepEqual(res.Repaired, []string{a}) || len(res.Unverified) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("result = %+v, want only a repaired and b not reported", res)
	}
	fx.assertDelta(before, map[string]int{"ReadPage": 1})
	fx.assertBase(a, ca, rp15Page("a", "v1 of a"))
	if fx.baseOK(b) {
		t.Fatal("b got a base although Paths left it out")
	}
}

func TestEnsureBase_HashMismatchedBaseIsRepaired(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, ca := fx.synced("a")
	fx.putBase(a, rp15Page("a", "v1 of a"), ca)
	fx.save()
	// The record still names ca, but the base file was edited.
	if err := os.WriteFile(syncstate.BaseFile(fx.hub, a), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fx.baseOK(a) {
		t.Fatal("precondition: the tampered base still verifies")
	}
	listing := fx.listing()
	before := fx.requests()

	res := fx.ensure(context.Background(), EnsureBaseOptions{Listing: listing})
	if !reflect.DeepEqual(res.Repaired, []string{a}) || len(res.Unverified) != 0 {
		t.Fatalf("result = %+v, want a repaired", res)
	}
	fx.assertDelta(before, map[string]int{"ReadPage": 1})
	fx.assertBase(a, ca, rp15Page("a", "v1 of a"))
}

func TestEnsureBase_RemoteFailureIsNotAnError(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, _ := fx.moved("a")
	fx.save()
	listing := fx.listing()
	fx.fake.FailNext("ReadPageAt", &sync.NetworkError{Op: "read page at", Err: errors.New("connection refused")})
	tree := fx.syncTree()

	res := fx.ensure(context.Background(), EnsureBaseOptions{Listing: listing})
	if len(res.Repaired) != 0 || !reflect.DeepEqual(res.Unverified, []string{a}) {
		t.Fatalf("result = %+v, want a unverified", res)
	}
	rp15Warning(t, res.Warnings, []string{a})
	if !strings.Contains(res.Warnings[0].Message, "network") {
		t.Fatalf("warning message %q does not name the network failure", res.Warnings[0].Message)
	}
	rp15AssertTree(t, ".sync", fx.syncTree(), tree)
	fx.assertNoLock()
}

func TestEnsureBase_LocalWriteFailureIsAnError(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, ca := fx.synced("a")
	fx.save()
	listing := fx.listing()
	// A regular file where the base store directory goes: Bases.Put fails.
	if err := os.WriteFile(filepath.Join(syncstate.SyncDir(fx.hub), "base"), []byte("not a dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := EnsureBase(context.Background(), fx.env, EnsureBaseOptions{Listing: listing})
	if err == nil {
		t.Fatalf("EnsureBase = %+v, nil; want the local write error", res)
	}
	rp15AssertEnsureShape(t, res)
	if len(res.Repaired) != 0 || !reflect.DeepEqual(res.Unverified, []string{a}) {
		t.Fatalf("result = %+v, want a unverified", res)
	}
	if got := fx.state()[a]; got != ca {
		t.Fatalf("page_shas[a] = %s, want %s", got, ca)
	}
	if _, err := os.Lstat(syncstate.BasesPath(fx.hub)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("bases.json written after a failed base write (%v)", err)
	}
	fx.assertNoLock()
	unlock, err := syncstate.Lock(fx.hub, "x", 0)
	if err != nil {
		t.Fatalf("Lock after the failure: %v", err)
	}
	unlock()
}

func TestEnsureBase_CancelledContextWritesNothing(t *testing.T) {
	t.Run("before the call", func(t *testing.T) {
		fx := rp15New(t, syncopstest.FakeOptions{})
		a, _ := fx.moved("a")
		fx.save()
		listing := fx.listing()
		before, tree := fx.requests(), fx.fullSyncTree()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		res, err := EnsureBase(ctx, fx.env, EnsureBaseOptions{Listing: listing})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("EnsureBase = %+v, %v; want context.Canceled", res, err)
		}
		rp15AssertEnsureShape(t, res)
		if len(res.Repaired) != 0 || !reflect.DeepEqual(res.Unverified, []string{a}) {
			t.Fatalf("result = %+v, want a unverified", res)
		}
		fx.assertDelta(before, map[string]int{})
		rp15AssertTree(t, ".sync", fx.fullSyncTree(), tree)
		fx.assertNoLock()
	})
	for _, locked := range []bool{false, true} {
		t.Run(fmt.Sprintf("during the network phase, Locked=%v", locked), func(t *testing.T) {
			fx := rp15New(t, syncopstest.FakeOptions{})
			paths, _ := fx.movedN(3)
			fx.save()
			listing := fx.listing()
			tree := fx.fullSyncTree()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := rp15Wrap(fx)
			w.hook = func(n int, _ string) {
				if n == 1 {
					cancel()
				}
			}

			res, err := EnsureBase(ctx, fx.env, EnsureBaseOptions{Listing: listing, Locked: locked})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("EnsureBase = %+v, %v; want context.Canceled", res, err)
			}
			rp15AssertEnsureShape(t, res)
			if len(res.Repaired) != 0 || !reflect.DeepEqual(res.Unverified, paths) {
				t.Fatalf("result = %+v, want every path unverified", res)
			}
			rp15AssertTree(t, ".sync", fx.fullSyncTree(), tree)
			fx.assertNoLock()
		})
	}
}

func TestEnsureBase_ServerInfoReadOnce(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, _ := fx.synced("a")
	b, _ := fx.moved("b")
	fx.save()
	listing := fx.listing()
	w := rp15Wrap(fx)
	before := fx.requests()

	// A second ServerInfo would report an unknown server and skip b.
	res := fx.ensure(context.Background(), EnsureBaseOptions{Listing: listing})
	if !reflect.DeepEqual(res.Repaired, []string{a, b}) || len(res.Unverified) != 0 {
		t.Fatalf("result = %+v, want a and b repaired", res)
	}
	calls := w.serverInfoCalls()
	if len(calls) != 1 || len(rp15Delta(before, calls[0])) != 0 {
		t.Fatalf("ServerInfo calls (requests before each) = %v, want one before any read", calls)
	}
}

// TestEnsureBase_ProbeXPageSHA pins the server fact base repair relies on
// (WP-0.2): ?at=<sha> on a later commit that did not touch the path answers
// with X-Page-SHA = the path's own last commit at or before sha, so repair can
// turn a repo-HEAD state sha (the old gitstore bug) into the path's sha.
func TestEnsureBase_ProbeXPageSHA(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	s := testkit.StartServer(t)
	c := sync.NewClient(s.URL, s.Key)
	const repo = "rp15-probe"
	if err := c.CreateRepo(repo); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	push := func(path, content string) string {
		t.Helper()
		resp, err := c.PushPages(repo, &sync.PushRequest{
			AuthorName: "Ana", AuthorEmail: "ana@acme.dev", Message: "rp15 " + path,
			Files: []sync.PushFile{{Path: path, Content: content}},
		})
		if err != nil || len(resp.Pushed) != 1 || len(resp.Conflicts) != 0 {
			t.Fatalf("push %s: %+v, %v", path, resp, err)
		}
		return resp.Pushed[0].SHA
	}
	const a, b = "wiki/concepts/a.md", "wiki/concepts/b.md"
	shaA := push(a, "---\nslug: a\n---\nA v1\n")
	shaB := push(b, "---\nslug: b\n---\nB v1\n")
	if shaA == shaB {
		t.Fatalf("two pushes made one commit %s", shaA)
	}

	body, pageSHA, err := c.ReadPageAt(repo, a, shaB)
	if err != nil {
		t.Fatalf("ReadPageAt(a, HEAD=b's commit): %v", err)
	}
	if pageSHA != shaA {
		t.Fatalf("X-Page-SHA = %q, want a's own last commit %q (not HEAD %q)", pageSHA, shaA, shaB)
	}
	if string(body) != "---\nslug: a\n---\nA v1\n" {
		t.Fatalf("?at body = %q", body)
	}
	_, caps, known := c.ServerInfo()
	hasAt := false
	for _, c := range caps {
		hasAt = hasAt || c == contract.SrvPagesAt
	}
	if !known || !hasAt {
		t.Fatalf("ServerInfo after ?at = %v, %v; want known with pages.at", caps, known)
	}
}
