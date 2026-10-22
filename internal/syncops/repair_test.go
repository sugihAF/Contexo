package syncops

// Owner: WP-1.5. Repair tests and the rp15 fixture shared with
// ensurebase_test.go. TestRepairResultJSONKeyOrder pins the E18 wire shape
// of `ctx repair --json`. This file may import syncopstest.FakeRemote:
// syncopstest never imports syncops, so there is no cycle (see
// ensurebase_test.go).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	stdsync "sync"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncops/syncopstest"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
)

func TestRepairResultJSONKeyOrder(t *testing.T) {
	const (
		shaA = "1111111111111111111111111111111111111111"
		shaB = "2222222222222222222222222222222222222222"
	)
	arrays := []string{"server_capabilities", "backfilled", "repaired", "skipped", "unverified", "warnings"}

	t.Run("full", func(t *testing.T) {
		r := NewRepairResult(true)
		r.ServerCapabilities = append(r.ServerCapabilities, contract.SrvPagesAt)
		r.Backfilled = append(r.Backfilled, RepairBackfilled{Path: "wiki/concepts/a.md", SHA: shaA})
		r.Repaired = append(r.Repaired, RepairedPage{Path: "wiki/concepts/b.md", OldSHA: shaA, NewSHA: shaB, Reason: "adopted"})
		r.Skipped = append(r.Skipped, RepairSkipped{Path: "wiki/concepts/c.md", Reason: "invalid"})
		r.Unverified = append(r.Unverified, "wiki/concepts/d.md")
		r.Warnings = append(r.Warnings, contract.Warning{
			Code: contract.WarnBaseRepairSkipped, Message: "1 page left unverified", Paths: []string{"wiki/concepts/d.md"},
		})

		raw := wp01WriteDoc(t, r)
		wp01AssertKeys(t, "top level", raw, "schema", "dry_run", "server_capabilities",
			"backfilled", "repaired", "skipped", "unverified", "warnings")

		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		if string(top["schema"]) != `"ctx.repair.v1"` || string(top["dry_run"]) != "true" {
			t.Fatalf("schema = %s, dry_run = %s", top["schema"], top["dry_run"])
		}
		wp01AssertKeys(t, "backfilled[0]", wp01First(t, top["backfilled"]), "path", "sha")
		wp01AssertKeys(t, "repaired[0]", wp01First(t, top["repaired"]), "path", "old_sha", "new_sha", "reason")
		wp01AssertKeys(t, "skipped[0]", wp01First(t, top["skipped"]), "path", "reason")
		wp01AssertKeys(t, "warnings[0]", wp01First(t, top["warnings"]), "code", "message", "paths")

		// It strictly round-trips.
		var back RepairResult
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&back); err != nil {
			t.Fatalf("strict decode: %v", err)
		}
		if !reflect.DeepEqual(&back, r) {
			t.Fatalf("round trip = %+v, want %+v", back, *r)
		}
	})

	t.Run("empty arrays are [] not null", func(t *testing.T) {
		r := NewRepairResult(false)
		raw := wp01WriteDoc(t, r)
		wp01AssertKeys(t, "top level", raw, "schema", "dry_run", "server_capabilities",
			"backfilled", "repaired", "skipped", "unverified", "warnings")
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		if string(top["schema"]) != `"ctx.repair.v1"` || string(top["dry_run"]) != "false" {
			t.Fatalf("schema = %s, dry_run = %s", top["schema"], top["dry_run"])
		}
		for _, k := range arrays {
			if got := string(top[k]); got != "[]" {
				t.Errorf("%s = %s, want []", k, got)
			}
		}
	})
}

func wp01WriteDoc(t *testing.T, doc any) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := contract.WriteDoc(&buf, doc); err != nil {
		t.Fatalf("WriteDoc: %v", err)
	}
	return buf.Bytes()
}

// wp01AssertKeys asserts raw is a JSON object whose keys are exactly want,
// in order.
func wp01AssertKeys(t *testing.T, what string, raw []byte, want ...string) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		t.Fatalf("%s: not a JSON object (%v, %v): %s", what, tok, err, raw)
	}
	var got []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		got = append(got, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s keys = %q, want %q", what, got, want)
	}
}

// wp01First returns the first element of a JSON array.
func wp01First(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil || len(arr) == 0 {
		t.Fatalf("not a non-empty array (%v): %s", err, raw)
	}
	return arr[0]
}

// ---- rp15 fixture (shared with ensurebase_test.go) ----

// rp15Now is the fixture clock (Env.Now).
var rp15Now = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

// rp15Fixture is a temp project (root/.contexo with wiki/concepts) whose Env
// talks to a FakeRemote. shas is the page_shas map save writes.
type rp15Fixture struct {
	t    *testing.T
	root string
	hub  string
	fake *syncopstest.FakeRemote
	env  Env
	shas map[string]string
}

// rp15New builds a fixture on a FakeRemote with o. HOME is isolated.
func rp15New(t *testing.T, o syncopstest.FakeOptions) *rp15Fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	if err := os.MkdirAll(filepath.Join(hub, "wiki", "concepts"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := syncopstest.NewFakeRemote(o)
	env := NewEnv(root, "acme-kb", "https://kb.acme.dev", Author{Name: "Ana", Email: "ana@acme.dev"}, fake)
	env.Now = func() time.Time { return rp15Now }
	return &rp15Fixture{t: t, root: root, hub: hub, fake: fake, env: env, shas: map[string]string{}}
}

// rp15OldServer is the "FakeRemote old mode" of the plan.
var rp15OldServer = syncopstest.FakeOptions{OldServer: true, NoExpectAbsent: true, LegacyNoopHeadSHA: true}

// rp15Rel is the page path of the concept slug.
func rp15Rel(slug string) string { return "wiki/concepts/" + slug + ".md" }

// rp15Page is a valid concept page for slug with body.
func rp15Page(slug, body string) []byte {
	return testkit.PageBytes(testkit.PageSpec{Slug: slug, Body: body})
}

// seed commits the page (slug, body) on the fake and returns its sha.
func (fx *rp15Fixture) seed(slug, body string) string {
	return fx.fake.Seed(rp15Rel(slug), rp15Page(slug, body))
}

// write writes the working file of rel.
func (fx *rp15Fixture) write(rel string, data []byte) {
	fx.t.Helper()
	p := filepath.Join(fx.hub, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		fx.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		fx.t.Fatal(err)
	}
}

// save writes fx.shas as state.json.
func (fx *rp15Fixture) save() {
	fx.t.Helper()
	shas := map[string]string{}
	for k, v := range fx.shas {
		shas[k] = v
	}
	if err := sync.SaveState(fx.hub, &sync.State{PageSHAs: shas}); err != nil {
		fx.t.Fatal(err)
	}
}

// putBase stores data as the base of rel at sha.
func (fx *rp15Fixture) putBase(rel string, data []byte, sha string) {
	fx.t.Helper()
	b, err := syncstate.OpenBases(fx.hub)
	if err != nil {
		fx.t.Fatal(err)
	}
	if err := b.Put(rel, data, sha, rp15Now); err != nil {
		fx.t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		fx.t.Fatal(err)
	}
}

// putSidecar stores a conflict sidecar for rel with content.
func (fx *rp15Fixture) putSidecar(rec syncstate.ConflictRecord, content []byte) {
	fx.t.Helper()
	c, err := syncstate.OpenConflicts(fx.hub)
	if err != nil {
		fx.t.Fatal(err)
	}
	if err := c.Put(rec, content); err != nil {
		fx.t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		fx.t.Fatal(err)
	}
}

// synced adds a page last pulled at HEAD: seeded once, its working file
// equal to the server bytes, page_shas at its sha and no base (a HEAD-equal
// stale path). Call save afterwards.
func (fx *rp15Fixture) synced(slug string) (rel, sha string) {
	rel = rp15Rel(slug)
	sha = fx.seed(slug, "v1 of "+slug)
	fx.write(rel, rp15Page(slug, "v1 of "+slug))
	fx.shas[rel] = sha
	return rel, sha
}

// moved is synced plus a later server edit: the listing sha differs from
// page_shas, so the path goes to phase 2 (?at).
func (fx *rp15Fixture) moved(slug string) (rel, sha string) {
	rel, sha = fx.synced(slug)
	fx.seed(slug, "v2 of "+slug)
	return rel, sha
}

// movedN adds n moved pages m00, m01, ... and returns their paths (sorted)
// and page_shas values.
func (fx *rp15Fixture) movedN(n int) ([]string, map[string]string) {
	paths, shas := []string{}, map[string]string{}
	for i := 0; i < n; i++ {
		rel, sha := fx.moved(fmt.Sprintf("m%02d", i))
		paths = append(paths, rel)
		shas[rel] = sha
	}
	sort.Strings(paths)
	return paths, shas
}

// listing is a (counted) ListPages on the fake.
func (fx *rp15Fixture) listing() []sync.PageMeta {
	fx.t.Helper()
	l, err := fx.fake.ListPages("acme-kb")
	if err != nil {
		fx.t.Fatal(err)
	}
	return l
}

// state returns the page_shas of state.json on disk.
func (fx *rp15Fixture) state() map[string]string {
	fx.t.Helper()
	st, err := sync.LoadState(fx.hub)
	if err != nil {
		fx.t.Fatal(err)
	}
	return st.PageSHAs
}

// baseOK reports whether rel has a valid base for its page_shas value that
// also passes ReadVerified.
func (fx *rp15Fixture) baseOK(rel string) bool {
	fx.t.Helper()
	b, err := syncstate.OpenBases(fx.hub)
	if err != nil {
		fx.t.Fatal(err)
	}
	if !b.Valid(rel, fx.state()[rel]) {
		return false
	}
	_, err = b.ReadVerified(rel)
	return err == nil
}

// assertBase asserts rel's base is valid and verified, at sha, holding want.
func (fx *rp15Fixture) assertBase(rel, sha string, want []byte) {
	fx.t.Helper()
	if got := fx.state()[rel]; got != sha {
		fx.t.Fatalf("page_shas[%s] = %s, want %s", rel, got, sha)
	}
	b, err := syncstate.OpenBases(fx.hub)
	if err != nil {
		fx.t.Fatal(err)
	}
	if !b.Valid(rel, sha) {
		rec, _ := b.Get(rel)
		fx.t.Fatalf("base of %s not valid for %s (record %+v)", rel, sha, rec)
	}
	got, err := b.ReadVerified(rel)
	if err != nil {
		fx.t.Fatalf("ReadVerified(%s): %v", rel, err)
	}
	if !bytes.Equal(got, want) {
		fx.t.Fatalf("base of %s = %q, want %q", rel, got, want)
	}
}

// classify gives rel's page state against listing, with the E9 sidecar
// override, the way status computes it.
func (fx *rp15Fixture) classify(listing []sync.PageMeta, rel string) contract.ClassifyResult {
	fx.t.Helper()
	remote := map[string]string{}
	for _, m := range listing {
		remote[m.Path] = m.SHA
	}
	v, err := syncstate.LoadView(fx.hub, remote)
	if err != nil {
		fx.t.Fatal(err)
	}
	f := v.Facts[rel]
	if f == nil {
		fx.t.Fatalf("%s is not an entry of the view", rel)
	}
	res, err := contract.ClassifySides(contract.Side(f.Local), contract.Side(f.Base), contract.Side(f.Remote))
	if err != nil {
		fx.t.Fatal(err)
	}
	if f.Conflict != nil {
		res = ApplySidecar(res, f.Base.SHA, &SidecarFact{
			ServerSHA: f.Conflict.ServerSHA, Kind: contract.ConflictKind(f.Conflict.Kind),
		})
	}
	return res
}

// requests is the fake's per-method call count.
func (fx *rp15Fixture) requests() map[string]int { return fx.fake.Requests() }

// rp15Delta returns after - before per method, without zeros.
func rp15Delta(before, after map[string]int) map[string]int {
	d := map[string]int{}
	for k, v := range after {
		if n := v - before[k]; n != 0 {
			d[k] = n
		}
	}
	for k, v := range before {
		if _, ok := after[k]; !ok && v != 0 {
			d[k] = -v
		}
	}
	return d
}

// rp15AssertDelta asserts the fake's calls since before are exactly want.
func (fx *rp15Fixture) assertDelta(before map[string]int, want map[string]int) {
	fx.t.Helper()
	if got := rp15Delta(before, fx.requests()); !reflect.DeepEqual(got, want) {
		fx.t.Fatalf("requests = %v, want %v", got, want)
	}
}

// rp15Tree maps every file under dir ('/'-separated, relative) to the
// sha256 of its bytes, leaving out rels skip accepts. A missing dir is an
// empty tree.
func rp15Tree(t *testing.T, dir string, skip func(rel string) bool) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && p == dir {
			return filepath.SkipAll
		}
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skip != nil && skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			tree[rel+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		tree[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// rp15NoLockFiles skips the lock and its guard: the only files a run
// that writes nothing else may leave a trace of.
func rp15NoLockFiles(rel string) bool { return rel == "lock" || rel == "lock.guard" }

// syncTree is the .sync tree without the lock files.
func (fx *rp15Fixture) syncTree() map[string]string {
	fx.t.Helper()
	return rp15Tree(fx.t, syncstate.SyncDir(fx.hub), rp15NoLockFiles)
}

// assertTree asserts tree equals want.
func rp15AssertTree(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s changed:\n got %v\nwant %v", what, got, want)
	}
}

// assertNoLock asserts .sync/lock does not exist.
func (fx *rp15Fixture) assertNoLock() {
	fx.t.Helper()
	if _, err := os.Lstat(syncstate.LockPath(fx.hub)); !errors.Is(err, fs.ErrNotExist) {
		fx.t.Fatalf("lock file left behind (%v)", err)
	}
}

// rp15Remote wraps the fake: ReadPageAt counts calls (n is 1-based), runs
// hook, waits delay(n) (or until the test ends) and tracks the maximum
// number of calls in flight. ServerInfo records the fake's request counts
// at each call and reports an unknown server after the first one, as a
// client would after a proxy error without the capability headers.
type rp15Remote struct {
	*syncopstest.FakeRemote
	delay func(n int) time.Duration
	hook  func(n int, path string)
	done  chan struct{}

	mu          stdsync.Mutex
	calls       int
	inflight    int
	maxInflight int
	infoAt      []map[string]int
}

func (r *rp15Remote) ServerInfo() (string, []contract.ServerCapability, bool) {
	reqs := r.FakeRemote.Requests()
	r.mu.Lock()
	r.infoAt = append(r.infoAt, reqs)
	first := len(r.infoAt) == 1
	r.mu.Unlock()
	if !first {
		return "", []contract.ServerCapability{}, false
	}
	return r.FakeRemote.ServerInfo()
}

// serverInfoCalls returns the fake's request counts at each ServerInfo
// call.
func (r *rp15Remote) serverInfoCalls() []map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]int{}, r.infoAt...)
}

// rp15Wrap wraps fx's fake and points fx.env at the wrapper.
func rp15Wrap(fx *rp15Fixture) *rp15Remote {
	r := &rp15Remote{FakeRemote: fx.fake, done: make(chan struct{})}
	fx.t.Cleanup(func() { close(r.done) })
	fx.env.Remote = r
	return r
}

func (r *rp15Remote) ReadPageAt(repoID, path, sha string) ([]byte, string, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	r.inflight++
	r.maxInflight = max(r.maxInflight, r.inflight)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.inflight--
		r.mu.Unlock()
	}()
	if r.hook != nil {
		r.hook(n, path)
	}
	if r.delay != nil {
		if d := r.delay(n); d > 0 {
			select {
			case <-time.After(d):
			case <-r.done:
			}
		}
	}
	return r.FakeRemote.ReadPageAt(repoID, path, sha)
}

// stats returns the call count, the calls in flight and their maximum.
func (r *rp15Remote) stats() (calls, inflight, maxInflight int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.inflight, r.maxInflight
}

// rp15Err429 is the client's error for a 429 on GET ...?at=.
func rp15Err429() error {
	body := []byte(`{"error":"rate limited"}`)
	return sync.NewHTTPError(429, body, "sync: read page at (429): "+string(body), nil, true)
}

// rp15Warning asserts warnings is exactly one base_repair_skipped naming
// paths.
func rp15Warning(t *testing.T, warnings []contract.Warning, paths []string) {
	t.Helper()
	if len(warnings) != 1 || warnings[0].Code != contract.WarnBaseRepairSkipped ||
		!reflect.DeepEqual(warnings[0].Paths, paths) || warnings[0].Message == "" {
		t.Fatalf("warnings = %+v, want one base_repair_skipped naming %v", warnings, paths)
	}
}

// rp15Repair runs Repair and fails the test on an error.
func (fx *rp15Fixture) repair(o RepairOptions) *RepairResult {
	fx.t.Helper()
	res, err := Repair(context.Background(), fx.env, o)
	if err != nil {
		fx.t.Fatalf("Repair(%+v): %v", o, err)
	}
	if res == nil {
		fx.t.Fatal("Repair returned a nil result")
	}
	return res
}

// rp15AssertResult asserts the arrays of res.
func rp15AssertResult(t *testing.T, res *RepairResult, back []RepairBackfilled, rep []RepairedPage, skip []RepairSkipped) {
	t.Helper()
	if back == nil {
		back = []RepairBackfilled{}
	}
	if rep == nil {
		rep = []RepairedPage{}
	}
	if skip == nil {
		skip = []RepairSkipped{}
	}
	unverified := []string{}
	for _, s := range skip {
		unverified = append(unverified, s.Path)
	}
	if !reflect.DeepEqual(res.Backfilled, back) {
		t.Errorf("backfilled = %+v, want %+v", res.Backfilled, back)
	}
	if !reflect.DeepEqual(res.Repaired, rep) {
		t.Errorf("repaired = %+v, want %+v", res.Repaired, rep)
	}
	if !reflect.DeepEqual(res.Skipped, skip) {
		t.Errorf("skipped = %+v, want %+v", res.Skipped, skip)
	}
	if !reflect.DeepEqual(res.Unverified, unverified) {
		t.Errorf("unverified = %v, want %v", res.Unverified, unverified)
	}
	if len(skip) == 0 {
		if res.Warnings == nil || len(res.Warnings) != 0 {
			t.Errorf("warnings = %+v, want []", res.Warnings)
		}
	} else {
		rp15Warning(t, res.Warnings, unverified)
	}
	if t.Failed() {
		t.FailNow()
	}
}

// ---- Repair ----

func TestRepair_FixesRepoHeadShaFromOldBug(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, b := rp15Rel("a"), rp15Rel("b")
	c1 := fx.seed("a", "A v1")
	c2 := fx.seed("b", "B v1") // repo HEAD
	fx.write(a, rp15Page("a", "A v1"))
	fx.write(b, rp15Page("b", "B v1"))
	// The old gitstore bug stored repo HEAD for a no-op write of a.
	fx.shas[a], fx.shas[b] = c2, c2
	fx.save()

	res := fx.repair(RepairOptions{})
	rp15AssertResult(t, res,
		[]RepairBackfilled{{Path: b, SHA: c2}},
		[]RepairedPage{{Path: a, OldSHA: c2, NewSHA: c1, Reason: "path_sha_fixed"}},
		nil)
	if res.Schema != contract.SchemaRepair || res.DryRun {
		t.Fatalf("schema/dry_run = %s/%v", res.Schema, res.DryRun)
	}
	fx.assertBase(a, c1, rp15Page("a", "A v1"))
	fx.assertBase(b, c2, rp15Page("b", "B v1"))
	listing := fx.listing()
	for _, p := range []string{a, b} {
		if got := fx.classify(listing, p); got.State != contract.StateUnchanged {
			t.Fatalf("%s classifies %+v, want unchanged", p, got)
		}
	}
}

func TestRepair_LeavesTrueIncomingAlone(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, c1 := fx.synced("a")
	fx.save()
	c2 := fx.seed("a", "v2 of a") // a true incoming change

	res := fx.repair(RepairOptions{})
	rp15AssertResult(t, res, []RepairBackfilled{{Path: a, SHA: c1}}, nil, nil)
	fx.assertBase(a, c1, rp15Page("a", "v1 of a"))
	listing := fx.listing()
	if got := fx.classify(listing, a); got.State != contract.StateIncoming {
		t.Fatalf("a classifies %+v, want incoming (server %s, base %s)", got, c2, c1)
	}
}

func TestRepair_OldServerOnlyBackfillsWhereShaMatchesHead(t *testing.T) {
	fx := rp15New(t, rp15OldServer)
	a, ca := fx.synced("a")
	b, _ := fx.synced("b")
	fx.seed("c", "v1 of c")
	fx.shas[b] = fx.fake.Head() // the old bug: repo HEAD, not b's own commit
	fx.save()

	before := fx.requests()
	res := fx.repair(RepairOptions{})
	fx.assertDelta(before, map[string]int{"ListPages": 1, "ReadPage": 1})
	rp15AssertResult(t, res,
		[]RepairBackfilled{{Path: a, SHA: ca}}, nil,
		[]RepairSkipped{{Path: b, Reason: "server_unsupported"}})
	if res.ServerCapabilities == nil || len(res.ServerCapabilities) != 0 {
		t.Fatalf("server_capabilities = %#v, want []", res.ServerCapabilities)
	}
	fx.assertBase(a, ca, rp15Page("a", "v1 of a"))
	if fx.baseOK(b) {
		t.Fatal("b got a base without ?at")
	}
	listing := fx.listing()
	if got := fx.classify(listing, b); got.State != contract.StateUnverified {
		t.Fatalf("b classifies %+v, want unverified", got)
	}
}

// rp15OldBump builds the E11 old-MCP case: base A = v1 at c1, the server
// moved to v2 at c2, page_shas bumped to c2 (an old MCP after a 409), and
// the working file local.
func rp15OldBump(t *testing.T, v2body string, local func(v1, v2 []byte) []byte) (fx *rp15Fixture, a, c1, c2 string, v1, v2 []byte) {
	fx = rp15New(t, syncopstest.FakeOptions{})
	a = rp15Rel("a")
	v1 = rp15Page("a", "A v1")
	v2 = rp15Page("a", v2body)
	c1 = fx.fake.Seed(a, v1)
	fx.putBase(a, v1, c1)
	c2 = fx.fake.Seed(a, v2)
	fx.write(a, local(v1, v2))
	fx.shas[a] = c2
	fx.save()
	return fx, a, c1, c2, v1, v2
}

func TestRepair_OldMCPBumpAfter409IsConflict(t *testing.T) {
	// v2 carries an escape sequence: the sidecar must hold the sanitized
	// server bytes.
	fx, a, c1, c2, v1, v2 := rp15OldBump(t, "A v2 \x1b[31mred\x1b[0m", func(v1, _ []byte) []byte {
		return append(append([]byte{}, v1...), "an unmerged agent edit\n"...)
	})

	res := fx.repair(RepairOptions{})
	rp15AssertResult(t, res, nil,
		[]RepairedPage{{Path: a, OldSHA: c2, NewSHA: c1, Reason: "kept_previous_base"}}, nil)
	fx.assertBase(a, c1, v1)

	c, err := syncstate.OpenConflicts(fx.hub)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := c.Get(a)
	want := syncstate.ConflictRecord{
		Path: a, ServerSHA: c2, BaseSHA: c1, Kind: syncstate.KindBothModified,
		Source: syncstate.SourceRepair, DetectedAt: rp15Now, HasContent: true,
	}
	if !ok || !reflect.DeepEqual(rec, want) {
		t.Fatalf("sidecar = %+v (%v), want %+v", rec, ok, want)
	}
	content, err := c.Content(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != schema.SanitizeContent(string(v2)) || bytes.Equal(content, v2) {
		t.Fatalf("sidecar content = %q, want the sanitized server bytes", content)
	}
	listing := fx.listing()
	got := fx.classify(listing, a)
	if got.State != contract.StateConflict || got.ConflictKind != contract.ConflictBothModified {
		t.Fatalf("a classifies %+v, want conflict both_modified", got)
	}
}

func TestRepair_OldBumpUnchangedFileIsIncoming(t *testing.T) {
	fx, a, c1, c2, v1, _ := rp15OldBump(t, "A v2", func(v1, _ []byte) []byte { return v1 })

	res := fx.repair(RepairOptions{})
	rp15AssertResult(t, res, nil,
		[]RepairedPage{{Path: a, OldSHA: c2, NewSHA: c1, Reason: "kept_previous_base"}}, nil)
	fx.assertBase(a, c1, v1)
	c, err := syncstate.OpenConflicts(fx.hub)
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := c.Get(a); ok {
		t.Fatalf("unexpected sidecar %+v", rec)
	}
	if got := fx.classify(fx.listing(), a); got.State != contract.StateIncoming {
		t.Fatalf("a classifies %+v, want incoming", got)
	}
}

func TestRepair_OldBinaryPullAdopted(t *testing.T) {
	fx, a, c1, c2, _, v2 := rp15OldBump(t, "A v2", func(_, v2 []byte) []byte { return v2 })
	fx.putSidecar(syncstate.ConflictRecord{
		Path: a, ServerSHA: c2, BaseSHA: c1, Kind: syncstate.KindBothModified,
		Source: syncstate.SourcePull, DetectedAt: rp15Now,
	}, v2)

	res := fx.repair(RepairOptions{})
	rp15AssertResult(t, res, []RepairBackfilled{{Path: a, SHA: c2}}, nil, nil)
	fx.assertBase(a, c2, v2)
	c, err := syncstate.OpenConflicts(fx.hub)
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := c.Get(a); ok {
		t.Fatalf("sidecar not cleared: %+v", rec)
	}
	if _, err := os.Stat(syncstate.ConflictFile(fx.hub, a)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("sidecar content left behind (%v)", err)
	}
	if got := fx.classify(fx.listing(), a); got.State != contract.StateUnchanged {
		t.Fatalf("a classifies %+v, want unchanged", got)
	}
}

// TestRepair_HeadShaOverValidBaseKeepsLocalEditModified: an old binary wrote
// repo HEAD (the gitstore bug) over a valid base A of a page nobody touched
// since A, and the working file has a local edit. ?at answers with A's own
// sha, so the server has no other version: E11 (3) keeps A and the page is
// modified, with no sidecar whose server sha would equal its base sha.
func TestRepair_HeadShaOverValidBaseKeepsLocalEditModified(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a := rp15Rel("a")
	v1 := rp15Page("a", "A v1")
	c1 := fx.fake.Seed(a, v1)
	fx.putBase(a, v1, c1)
	head := fx.seed("b", "B v1") // a later commit that did not touch a
	fx.write(a, append(append([]byte{}, v1...), "a local edit\n"...))
	fx.shas[a] = head
	fx.save()

	res := fx.repair(RepairOptions{})
	rp15AssertResult(t, res, nil,
		[]RepairedPage{{Path: a, OldSHA: head, NewSHA: c1, Reason: "kept_previous_base"}}, nil)
	fx.assertBase(a, c1, v1)
	c, err := syncstate.OpenConflicts(fx.hub)
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := c.Get(a); ok {
		t.Fatalf("sidecar %+v written although the server version is the base", rec)
	}
	if got := fx.classify(fx.listing(), a); got.State != contract.StateModified {
		t.Fatalf("a classifies %+v, want modified", got)
	}
}

func TestEnsureBase_500HeadEqualPagesTwoRequests(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	paths := []string{}
	for i := 0; i < 500; i++ {
		rel, _ := fx.synced(fmt.Sprintf("p%03d", i))
		paths = append(paths, rel)
	}
	fx.save()

	before := fx.requests()
	listing := fx.listing()
	res, err := EnsureBase(context.Background(), fx.env, EnsureBaseOptions{Listing: listing})
	if err != nil {
		t.Fatalf("EnsureBase: %v", err)
	}
	fx.assertDelta(before, map[string]int{"ListPages": 1, "PullPages": 1})
	if !reflect.DeepEqual(res.Repaired, paths) || len(res.Unverified) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("result: %d repaired, unverified %v, warnings %+v", len(res.Repaired), res.Unverified, res.Warnings)
	}
	b, err := syncstate.OpenBases(fx.hub)
	if err != nil {
		t.Fatal(err)
	}
	st := fx.state()
	for _, p := range paths {
		if !b.Valid(p, st[p]) {
			t.Fatalf("%s: no valid base", p)
		}
		if _, err := b.ReadVerified(p); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
}

func TestEnsureBase_PhaseTwoCappedAt50(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	paths, _ := fx.movedN(60)
	fx.save()

	before := fx.requests()
	listing := fx.listing()
	res, err := EnsureBase(context.Background(), fx.env, EnsureBaseOptions{Listing: listing})
	if err != nil {
		t.Fatalf("EnsureBase: %v", err)
	}
	fx.assertDelta(before, map[string]int{"ListPages": 1, "ReadPageAt": 50})
	if !reflect.DeepEqual(res.Repaired, paths[:50]) {
		t.Fatalf("repaired = %v, want the first 50", res.Repaired)
	}
	if !reflect.DeepEqual(res.Unverified, paths[50:]) {
		t.Fatalf("unverified = %v, want %v", res.Unverified, paths[50:])
	}
	rp15Warning(t, res.Warnings, paths[50:])
}

func TestRepair_StopsOn429(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	paths, shas := fx.movedN(6)
	fx.save()
	w := rp15Wrap(fx)
	w.hook = func(n int, _ string) {
		if n == 3 {
			fx.fake.FailNext("ReadPageAt", rp15Err429())
		}
	}

	before := fx.requests()
	res := fx.repair(RepairOptions{Concurrency: 1})
	fx.assertDelta(before, map[string]int{"ListPages": 1, "ReadPageAt": 3})
	skipped := []RepairSkipped{}
	for _, p := range paths[2:] {
		skipped = append(skipped, RepairSkipped{Path: p, Reason: "rate_limited"})
	}
	rp15AssertResult(t, res,
		[]RepairBackfilled{{Path: paths[0], SHA: shas[paths[0]]}, {Path: paths[1], SHA: shas[paths[1]]}},
		nil, skipped)
	for i, p := range paths {
		if ok := fx.baseOK(p); ok != (i < 2) {
			t.Fatalf("%s: base ok = %v", p, ok)
		}
	}
}

// rp15Mixed builds the mixed scenario: adopt (an old-binary pull with a
// sidecar), keep (an old MCP bump over an edit), rebase (repo HEAD in
// page_shas over an edit, no base), invalid (an invalid working file),
// deleted (no working file) and fine (a valid base, never touched). want
// is the result a run gives.
func rp15Mixed(t *testing.T) (fx *rp15Fixture, want *RepairResult) {
	fx = rp15New(t, syncopstest.FakeOptions{})
	fine, cf := fx.synced("fine")
	fx.putBase(fine, rp15Page("fine", "v1 of fine"), cf)

	rebase := rp15Rel("rebase")
	cr := fx.seed("rebase", "v1 of rebase")
	fx.write(rebase, rp15Page("rebase", "a local edit of rebase"))

	adopt := rp15Rel("adopt")
	ca1 := fx.seed("adopt", "v1 of adopt")
	fx.putBase(adopt, rp15Page("adopt", "v1 of adopt"), ca1)
	ca2 := fx.seed("adopt", "v2 of adopt")
	fx.write(adopt, rp15Page("adopt", "v2 of adopt"))
	fx.shas[adopt] = ca2
	fx.putSidecar(syncstate.ConflictRecord{
		Path: adopt, ServerSHA: ca2, BaseSHA: ca1, Kind: syncstate.KindBothModified,
		Source: syncstate.SourcePull, DetectedAt: rp15Now,
	}, rp15Page("adopt", "v2 of adopt"))

	keep := rp15Rel("keep")
	ck1 := fx.seed("keep", "v1 of keep")
	fx.putBase(keep, rp15Page("keep", "v1 of keep"), ck1)
	ck2 := fx.seed("keep", "v2 of keep")
	fx.write(keep, rp15Page("keep", "an agent edit of keep"))
	fx.shas[keep] = ck2

	invalid, ci := fx.synced("invalid")
	fx.write(invalid, []byte("not a page\n"))

	deleted, cd := fx.synced("deleted")
	if err := os.Remove(filepath.Join(fx.hub, filepath.FromSlash(deleted))); err != nil {
		t.Fatal(err)
	}

	head := fx.fake.Head()
	fx.shas[rebase] = head
	fx.save()

	want = NewRepairResult(false)
	want.ServerCapabilities = []contract.ServerCapability{
		contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA,
	}
	want.Backfilled = []RepairBackfilled{{Path: adopt, SHA: ca2}, {Path: deleted, SHA: cd}, {Path: invalid, SHA: ci}}
	want.Repaired = []RepairedPage{
		{Path: keep, OldSHA: ck2, NewSHA: ck1, Reason: "kept_previous_base"},
		{Path: rebase, OldSHA: head, NewSHA: cr, Reason: "path_sha_fixed"},
	}
	return fx, want
}

func TestRepair_NeverWritesWorkingFiles(t *testing.T) {
	fx, want := rp15Mixed(t)
	outsideSync := func(rel string) bool { return rel == ".contexo/.sync" }
	before := rp15Tree(t, fx.root, outsideSync)

	res := fx.repair(RepairOptions{})
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("result = %+v\nwant %+v", res, want)
	}
	rp15AssertTree(t, "working tree", rp15Tree(t, fx.root, outsideSync), before)
}

func TestRepair_DryRunWritesNothing(t *testing.T) {
	fx, want := rp15Mixed(t)
	skip := func(rel string) bool { return rp15NoLockFiles(rel) || rel == ".gitignore" }
	syncBefore := rp15Tree(t, syncstate.SyncDir(fx.hub), skip)
	outsideSync := func(rel string) bool { return rel == ".contexo/.sync" }
	workBefore := rp15Tree(t, fx.root, outsideSync)

	dry := fx.repair(RepairOptions{DryRun: true})
	rp15AssertTree(t, ".sync", rp15Tree(t, syncstate.SyncDir(fx.hub), skip), syncBefore)
	rp15AssertTree(t, "working tree", rp15Tree(t, fx.root, outsideSync), workBefore)
	fx.assertNoLock()
	if !dry.DryRun {
		t.Fatal("dry_run = false")
	}

	real := fx.repair(RepairOptions{})
	if !reflect.DeepEqual(real, want) {
		t.Fatalf("real run = %+v\nwant %+v", real, want)
	}
	dry.DryRun = false
	if !reflect.DeepEqual(dry, real) {
		t.Fatalf("dry run = %+v\nreal run = %+v", dry, real)
	}
	if reflect.DeepEqual(rp15Tree(t, syncstate.SyncDir(fx.hub), skip), syncBefore) {
		t.Fatal("the real run wrote nothing")
	}
}

func TestRepair_BusyIsBusyError(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	fx.synced("a")
	fx.save()
	t.Setenv("CONTEXO_LOCK_TIMEOUT", "0")
	unlock, err := syncstate.Lock(fx.hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	res, err := Repair(context.Background(), fx.env, RepairOptions{})
	var busy *syncstate.BusyError
	if !errors.As(err, &busy) || busy.Holder.Command != "push" {
		t.Fatalf("Repair = %+v, %v; want *syncstate.BusyError held by push", res, err)
	}
	if res != nil {
		t.Fatalf("result with the busy error: %+v", res)
	}
	if got := fx.requests(); len(got) != 0 {
		t.Fatalf("requests = %v, want none", got)
	}
}

func TestRepair_NoRemoteIsNotAuthenticated(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	fx.synced("a")
	fx.save()
	before := fx.syncTree()
	fx.env.Remote = nil

	res, err := Repair(context.Background(), fx.env, RepairOptions{})
	var ce *contract.Error
	if !errors.As(err, &ce) || ce.Code != contract.CodeNotAuthenticated || res != nil {
		t.Fatalf("Repair = %+v, %v; want not_authenticated", res, err)
	}
	rp15AssertTree(t, ".sync", fx.syncTree(), before)
	fx.assertNoLock()
}

func TestRepair_ListPagesFailureReturned(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	fx.synced("a")
	fx.save()
	body := []byte(`{"error":"rate limited"}`)
	fx.fake.FailNext("ListPages", sync.NewHTTPError(429, body, "sync: list pages (429): "+string(body), nil, false))
	before := fx.syncTree()
	reqs := fx.requests()

	res, err := Repair(context.Background(), fx.env, RepairOptions{})
	var coded interface{ ErrorCode() string }
	if !errors.As(err, &coded) || coded.ErrorCode() != "rate_limited" || res != nil {
		t.Fatalf("Repair = %+v, %v; want a rate_limited error", res, err)
	}
	fx.assertDelta(reqs, map[string]int{"ListPages": 1})
	rp15AssertTree(t, ".sync", fx.syncTree(), before)
	fx.assertNoLock()
}

func TestRepair_BudgetAndConcurrencyOptions(t *testing.T) {
	t.Run("budget 3", func(t *testing.T) {
		fx := rp15New(t, syncopstest.FakeOptions{})
		paths, shas := fx.movedN(5)
		fx.save()
		before := fx.requests()
		res := fx.repair(RepairOptions{Budget: 3})
		fx.assertDelta(before, map[string]int{"ListPages": 1, "ReadPageAt": 3})
		back := []RepairBackfilled{}
		for _, p := range paths[:3] {
			back = append(back, RepairBackfilled{Path: p, SHA: shas[p]})
		}
		rp15AssertResult(t, res, back, nil, []RepairSkipped{
			{Path: paths[3], Reason: "budget_exhausted"}, {Path: paths[4], Reason: "budget_exhausted"},
		})
	})
	t.Run("concurrency 99", func(t *testing.T) {
		fx := rp15New(t, syncopstest.FakeOptions{})
		paths, _ := fx.movedN(12)
		fx.save()
		w := rp15Wrap(fx)
		w.delay = func(int) time.Duration { return 50 * time.Millisecond }
		res := fx.repair(RepairOptions{Concurrency: 99})
		if len(res.Backfilled) != len(paths) || len(res.Skipped) != 0 {
			t.Fatalf("result = %+v", res)
		}
		calls, _, maxIn := w.stats()
		if calls != 12 || maxIn > 4 || maxIn < 1 {
			t.Fatalf("calls = %d, max in flight = %d; want 12 calls, at most 4 in flight", calls, maxIn)
		}
	})
}

func TestRepair_SecondRunIsNoop(t *testing.T) {
	fx, want := rp15Mixed(t)
	if res := fx.repair(RepairOptions{}); !reflect.DeepEqual(res, want) {
		t.Fatalf("first run = %+v\nwant %+v", res, want)
	}
	before := fx.requests()
	tree := fx.syncTree()

	res := fx.repair(RepairOptions{})
	fx.assertDelta(before, map[string]int{"ListPages": 1})
	rp15AssertResult(t, res, nil, nil, nil)
	rp15AssertTree(t, ".sync", fx.syncTree(), tree)
}

func TestRepair_ResultArraysSortedNonNil(t *testing.T) {
	t.Run("empty project", func(t *testing.T) {
		fx := rp15New(t, syncopstest.FakeOptions{})
		res := fx.repair(RepairOptions{})
		raw := wp01WriteDoc(t, res)
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"backfilled", "repaired", "skipped", "unverified", "warnings"} {
			if string(top[k]) != "[]" {
				t.Errorf("%s = %s, want []", k, top[k])
			}
		}
		if res.ServerCapabilities == nil {
			t.Error("server_capabilities is nil")
		}
	})
	t.Run("sorted", func(t *testing.T) {
		fx := rp15New(t, syncopstest.FakeOptions{})
		// Created in reverse order so nothing comes out sorted by accident.
		fx.synced("z")
		fx.synced("y")
		for _, slug := range []string{"k2", "k1"} {
			rel := rp15Rel(slug)
			c1 := fx.seed(slug, "v1 of "+slug)
			fx.putBase(rel, rp15Page(slug, "v1 of "+slug), c1)
			fx.shas[rel] = fx.seed(slug, "v2 of "+slug)
			fx.write(rel, rp15Page(slug, "v1 of "+slug))
		}
		for _, slug := range []string{"r", "q", "p", "o"} {
			fx.moved(slug)
		}
		fx.save()

		res := fx.repair(RepairOptions{Budget: 2})
		if len(res.Backfilled) != 4 || len(res.Repaired) != 2 || len(res.Skipped) != 2 {
			t.Fatalf("result = %+v", res)
		}
		if !sort.SliceIsSorted(res.Backfilled, func(i, j int) bool { return res.Backfilled[i].Path < res.Backfilled[j].Path }) ||
			!sort.SliceIsSorted(res.Repaired, func(i, j int) bool { return res.Repaired[i].Path < res.Repaired[j].Path }) ||
			!sort.SliceIsSorted(res.Skipped, func(i, j int) bool { return res.Skipped[i].Path < res.Skipped[j].Path }) ||
			!sort.StringsAreSorted(res.Unverified) || !sort.StringsAreSorted(res.Warnings[0].Paths) {
			t.Fatalf("unsorted result: %+v", res)
		}
		if want := []string{rp15Rel("q"), rp15Rel("r")}; !reflect.DeepEqual(res.Unverified, want) {
			t.Fatalf("unverified = %v, want %v", res.Unverified, want)
		}
	})
}

func TestRepair_InvalidLocalFileStillBackfilled(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, ca := fx.synced("a")
	fx.write(a, []byte("---\nnot: [valid\n---\nbroken\n"))
	fx.save()

	res := fx.repair(RepairOptions{})
	rp15AssertResult(t, res, []RepairBackfilled{{Path: a, SHA: ca}}, nil, nil)
	fx.assertBase(a, ca, rp15Page("a", "v1 of a"))
	c, err := syncstate.OpenConflicts(fx.hub)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.List()) != 0 {
		t.Fatalf("sidecars = %+v", c.List())
	}
}

func TestRepair_StateChangedDuringRepairIsSkipped(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, ca1 := fx.synced("a")
	b, cb := fx.synced("b")
	ca2 := fx.seed("a", "v2 of a") // HEAD
	fx.shas[b] = ca2               // the old bug on b
	fx.save()

	w := rp15Wrap(fx)
	w.hook = func(_ int, path string) {
		if path != a {
			return
		}
		// An old ctx (no lock) pulls a while repair is on the network.
		st, err := sync.LoadState(fx.hub)
		if err == nil {
			st.PageSHAs[a] = ca2
			err = sync.SaveState(fx.hub, st)
		}
		if err != nil {
			t.Errorf("concurrent state write: %v", err)
		}
	}

	res := fx.repair(RepairOptions{})
	rp15AssertResult(t, res, nil,
		[]RepairedPage{{Path: b, OldSHA: ca2, NewSHA: cb, Reason: "path_sha_fixed"}},
		[]RepairSkipped{{Path: a, Reason: "state_changed"}})
	st := fx.state()
	if st[a] != ca2 || st[b] != cb {
		t.Fatalf("page_shas = %v; want a = %s (the concurrent value, not %s), b = %s", st, ca2, ca1, cb)
	}
	if fx.baseOK(a) {
		t.Fatal("a got a base despite the concurrent change")
	}
	fx.assertBase(b, cb, rp15Page("b", "v1 of b"))
}

func TestRepair_ServerInfoReadOnce(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, ca := fx.synced("a")
	b, cb := fx.moved("b")
	fx.save()
	w := rp15Wrap(fx)
	before := fx.requests()

	// A second ServerInfo would report an unknown server and skip b.
	res := fx.repair(RepairOptions{})
	rp15AssertResult(t, res, []RepairBackfilled{{Path: a, SHA: ca}, {Path: b, SHA: cb}}, nil, nil)
	calls := w.serverInfoCalls()
	if len(calls) != 1 || !reflect.DeepEqual(rp15Delta(before, calls[0]), map[string]int{"ListPages": 1}) {
		t.Fatalf("ServerInfo calls (requests before each) = %v, want one right after ListPages", calls)
	}
}

// rp15FileID returns the identity of the file at path, taken from an open
// handle (on Windows, os.Stat loads the id lazily from the path).
func rp15FileID(t *testing.T, path string) os.FileInfo {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func TestRepair_WritesOnlyChangedIndexes(t *testing.T) {
	fx := rp15New(t, syncopstest.FakeOptions{})
	a, ca := fx.synced("a") // a pure backfill: page_shas keeps its value
	fx.save()
	state := rp15FileID(t, sync.StatePath(fx.hub))

	res := fx.repair(RepairOptions{})
	rp15AssertResult(t, res, []RepairBackfilled{{Path: a, SHA: ca}}, nil, nil)
	fx.assertBase(a, ca, rp15Page("a", "v1 of a"))
	if !os.SameFile(state, rp15FileID(t, sync.StatePath(fx.hub))) {
		t.Fatal("state.json rewritten although no page_shas value changed")
	}
	if _, err := os.Lstat(syncstate.ConflictsPath(fx.hub)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("conflicts.json written without a sidecar change (%v)", err)
	}
}
