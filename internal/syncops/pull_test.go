package syncops

// Owner: WP-1.3. Engine tests of the safe pull. They drive Pull on
// syncopstest.FakeRemote over a testkit project and override the unexported
// seams pullEnsureBase and pullBeforeWriteHook, so they never call
// t.Parallel. Helpers are pl13-prefixed.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncops/syncopstest"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
	"github.com/sugihAF/contexo/internal/ui"
)

// pl13Now is the fixed Env.Now of every fixture.
var pl13Now = time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)

// pl13Old is the mtime a file gets before a pull that must not touch it, so
// a rewrite shows up as a newer mtime.
var pl13Old = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

const (
	pl13RepoID    = "pl13"
	pl13ServerURL = "https://pl13.test"
)

// pl13Fixture is one project (Root, Hub) on one in-memory server (Fake)
// with an Env over both.
type pl13Fixture struct {
	t    *testing.T
	Root string
	Hub  string
	Fake *syncopstest.FakeRemote
	Env  Env
}

func pl13NewFixture(t *testing.T) *pl13Fixture {
	t.Helper()
	p := testkit.NewProject(t, testkit.ProjectOptions{RepoID: pl13RepoID, NoCreds: true})
	fake := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := NewEnv(p.Root, pl13RepoID, pl13ServerURL, Author{Name: "Pl", Email: "pl@acme.dev"}, fake)
	env.Now = func() time.Time { return pl13Now }
	if env.HubDir != p.HubDir {
		t.Fatalf("Env.HubDir = %q, testkit HubDir = %q", env.HubDir, p.HubDir)
	}
	return &pl13Fixture{t: t, Root: p.Root, Hub: p.HubDir, Fake: fake, Env: env}
}

// pl13Rel is the path of the concept page slug.
func pl13Rel(slug string) string { return "wiki/concepts/" + slug + ".md" }

// pl13Page is a valid concept page for slug with body.
func pl13Page(slug, body string) []byte {
	return testkit.PageBytes(testkit.PageSpec{Slug: slug, Body: body})
}

// pl13BOM prefixes data with a UTF-8 byte-order mark: Equal to data, not
// byte-equal.
func pl13BOM(data []byte) []byte {
	return append([]byte{0xEF, 0xBB, 0xBF}, data...)
}

// pl13Sanitized is the one sanitize rule (E6) applied to server bytes.
func pl13Sanitized(server []byte) []byte {
	return []byte(schema.SanitizeContent(string(server)))
}

// path is the native path of rel under the hub.
func (fx *pl13Fixture) path(rel string) string {
	return filepath.Join(fx.Hub, filepath.FromSlash(rel))
}

// synced seeds the server with the concept page slug and records it as
// pulled: the same bytes locally, a valid base and page_shas[rel] = sha.
func (fx *pl13Fixture) synced(slug, body string) (rel, sha string) {
	fx.t.Helper()
	rel = pl13Rel(slug)
	data := pl13Page(slug, body)
	sha = fx.Fake.Seed(rel, data)
	fx.write(rel, data)
	bases := fx.bases()
	if err := bases.Put(rel, data, sha, pl13Now); err != nil {
		fx.t.Fatalf("Bases.Put(%s): %v", rel, err)
	}
	if err := bases.Save(); err != nil {
		fx.t.Fatalf("Bases.Save: %v", err)
	}
	st := fx.state()
	st.PageSHAs[rel] = sha
	fx.saveState(st)
	return rel, sha
}

// serverEdit commits a new version of the concept page slug on the server.
func (fx *pl13Fixture) serverEdit(slug, body string) (data []byte, sha string) {
	fx.t.Helper()
	data = pl13Page(slug, body)
	return data, fx.Fake.Seed(pl13Rel(slug), data)
}

func (fx *pl13Fixture) setLastPull(sha string) {
	fx.t.Helper()
	st := fx.state()
	st.LastPullSHA = sha
	fx.saveState(st)
}

func (fx *pl13Fixture) state() *sync.State {
	fx.t.Helper()
	st, err := sync.LoadState(fx.Hub)
	if err != nil {
		fx.t.Fatalf("LoadState: %v", err)
	}
	return st
}

func (fx *pl13Fixture) saveState(st *sync.State) {
	fx.t.Helper()
	if err := sync.SaveState(fx.Hub, st); err != nil {
		fx.t.Fatalf("SaveState: %v", err)
	}
}

func (fx *pl13Fixture) bases() *syncstate.Bases {
	fx.t.Helper()
	b, err := syncstate.OpenBases(fx.Hub)
	if err != nil {
		fx.t.Fatalf("OpenBases: %v", err)
	}
	return b
}

func (fx *pl13Fixture) conflicts() *syncstate.Conflicts {
	fx.t.Helper()
	c, err := syncstate.OpenConflicts(fx.Hub)
	if err != nil {
		fx.t.Fatalf("OpenConflicts: %v", err)
	}
	return c
}

// putSidecar records a conflict sidecar for rec.Path with content.
func (fx *pl13Fixture) putSidecar(rec syncstate.ConflictRecord, content []byte) {
	fx.t.Helper()
	c := fx.conflicts()
	if err := c.Put(rec, content); err != nil {
		fx.t.Fatalf("Conflicts.Put(%s): %v", rec.Path, err)
	}
	if err := c.Save(); err != nil {
		fx.t.Fatalf("Conflicts.Save: %v", err)
	}
}

func (fx *pl13Fixture) write(rel string, data []byte) {
	fx.t.Helper()
	p := fx.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		fx.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		fx.t.Fatal(err)
	}
}

func (fx *pl13Fixture) read(rel string) []byte {
	fx.t.Helper()
	data, err := os.ReadFile(fx.path(rel))
	if err != nil {
		fx.t.Fatalf("read %s: %v", rel, err)
	}
	return data
}

func (fx *pl13Fixture) remove(rel string) {
	fx.t.Helper()
	if err := os.Remove(fx.path(rel)); err != nil {
		fx.t.Fatal(err)
	}
}

// pull runs Pull and fails the test on an error; the document passes
// pl13CheckDoc.
func (fx *pl13Fixture) pull() *contract.PullDoc {
	fx.t.Helper()
	doc, err := fx.pullErr()
	if err != nil {
		fx.t.Fatalf("Pull: %v", err)
	}
	if doc == nil {
		fx.t.Fatal("Pull returned a nil document and a nil error")
	}
	pl13CheckDoc(fx.t, doc)
	return doc
}

func (fx *pl13Fixture) pullErr() (*contract.PullDoc, error) {
	return Pull(context.Background(), fx.Env, PullOptions{})
}

// requests is the fake's request counts without zero entries.
func (fx *pl13Fixture) requests() map[string]int {
	out := map[string]int{}
	for k, v := range fx.Fake.Requests() {
		if v != 0 {
			out[k] = v
		}
	}
	return out
}

// pl13CheckDoc asserts the document invariants every pull keeps: schema,
// server and repo, non-nil slices, entries sorted by path.
func pl13CheckDoc(t *testing.T, doc *contract.PullDoc) {
	t.Helper()
	if doc.Schema != contract.SchemaPull {
		t.Errorf("schema = %q, want %q", doc.Schema, contract.SchemaPull)
	}
	if doc.ServerURL != pl13ServerURL || doc.RepoID != pl13RepoID {
		t.Errorf("server_url, repo_id = %q, %q; want %q, %q", doc.ServerURL, doc.RepoID, pl13ServerURL, pl13RepoID)
	}
	if doc.Written == nil || doc.Adopted == nil || doc.Skipped == nil || doc.Warnings == nil {
		t.Fatalf("document has a nil slice: %+v", doc)
	}
	for name, paths := range map[string][]string{
		"written": pl13PulledPaths(doc.Written),
		"adopted": pl13PulledPaths(doc.Adopted),
		"skipped": pl13SkippedPaths(doc.Skipped),
	} {
		if !sort.StringsAreSorted(paths) {
			t.Errorf("%s is not sorted by path: %v", name, paths)
		}
	}
}

func pl13PulledPaths(ps []contract.PulledPage) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.Path)
	}
	return out
}

func pl13SkippedPaths(ss []contract.SkippedPage) []string {
	out := []string{}
	for _, s := range ss {
		out = append(out, s.Path)
	}
	return out
}

// pl13Stat returns the sha256 of the file at path and its mtime.
func pl13Stat(t *testing.T, path string) (sum string, mtime time.Time) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), fi.ModTime()
}

// pl13Backdate sets the mtime of path to 2020-01-01 so a rewrite is visible.
func pl13Backdate(t *testing.T, path string) {
	t.Helper()
	if err := os.Chtimes(path, pl13Old, pl13Old); err != nil {
		t.Fatal(err)
	}
}

// pl13Untouched backdates path and returns a check that its bytes and mtime
// are unchanged.
func pl13Untouched(t *testing.T, path string) func() {
	t.Helper()
	pl13Backdate(t, path)
	sum0, mt0 := pl13Stat(t, path)
	return func() {
		t.Helper()
		sum, mt := pl13Stat(t, path)
		if sum != sum0 {
			t.Errorf("%s: content changed (sha256 %s -> %s)", path, sum0, sum)
		}
		if !mt.Equal(mt0) {
			t.Errorf("%s: mtime changed (%v -> %v): the file was rewritten", path, mt0, mt)
		}
	}
}

// pl13TreeHash is a sha256 over the sorted (rel path, bytes) of every
// regular file under hub, except the lock files and .sync/.gitignore.
func pl13TreeHash(t *testing.T, hub string) string {
	t.Helper()
	skip := map[string]bool{".sync/lock": true, ".sync/lock.guard": true, ".sync/.gitignore": true}
	type entry struct {
		rel  string
		data []byte
	}
	var entries []entry
	err := filepath.WalkDir(hub, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(hub, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skip[rel] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, entry{rel, data})
		return nil
	})
	if err != nil {
		t.Fatalf("tree hash: %v", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	h := sha256.New()
	for _, e := range entries {
		sum := sha256.Sum256(e.data)
		h.Write([]byte(e.rel))
		h.Write([]byte{0})
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// pl13NoEnsureBase makes pullEnsureBase a no-op returning an empty result,
// so a stale base stays unverified whatever EnsureBase becomes.
func pl13NoEnsureBase(t *testing.T) {
	t.Helper()
	prev := pullEnsureBase
	pullEnsureBase = func(ctx context.Context, env Env, o EnsureBaseOptions) (*EnsureBaseResult, error) {
		return &EnsureBaseResult{Repaired: []string{}, Unverified: []string{}, Warnings: []contract.Warning{}}, nil
	}
	t.Cleanup(func() { pullEnsureBase = prev })
}

// pl13SetHook sets pullBeforeWriteHook for the test.
func pl13SetHook(t *testing.T, fn func(rel string)) {
	t.Helper()
	prev := pullBeforeWriteHook
	pullBeforeWriteHook = fn
	t.Cleanup(func() { pullBeforeWriteHook = prev })
}

// pl13Skip returns the skipped entry for rel, or nil.
func pl13Skip(doc *contract.PullDoc, rel string) *contract.SkippedPage {
	for i := range doc.Skipped {
		if doc.Skipped[i].Path == rel {
			return &doc.Skipped[i]
		}
	}
	return nil
}

// pl13WantSkip asserts doc skipped rel with state, reason and server sha.
func pl13WantSkip(t *testing.T, doc *contract.PullDoc, rel string, state contract.PageState, reason contract.SkipReason, serverSHA string) {
	t.Helper()
	got := pl13Skip(doc, rel)
	if got == nil {
		t.Fatalf("no skipped entry for %s; skipped = %+v", rel, doc.Skipped)
	}
	want := contract.SkippedPage{Path: rel, State: state, Reason: reason, ServerSHA: serverSHA}
	if *got != want {
		t.Fatalf("skipped %s = %+v, want %+v", rel, *got, want)
	}
}

// pl13WantPulled asserts got equals want (both lists of PulledPage).
func pl13WantPulled(t *testing.T, what string, got, want []contract.PulledPage) {
	t.Helper()
	if want == nil {
		want = []contract.PulledPage{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %+v, want %+v", what, got, want)
	}
}

// pl13Warning returns the warning with code, or nil.
func pl13Warning(doc *contract.PullDoc, code contract.WarningCode) *contract.Warning {
	for i := range doc.Warnings {
		if doc.Warnings[i].Code == code {
			return &doc.Warnings[i]
		}
	}
	return nil
}

// wantNoSidecar asserts rel has neither a conflicts.json record nor a
// .sync/conflicts file.
func (fx *pl13Fixture) wantNoSidecar(rel string) {
	fx.t.Helper()
	if rec, ok := fx.conflicts().Get(rel); ok {
		fx.t.Fatalf("conflicts.json has a record for %s: %+v", rel, rec)
	}
	if _, err := os.Stat(syncstate.ConflictFile(fx.Hub, rel)); !errors.Is(err, fs.ErrNotExist) {
		fx.t.Fatalf("sidecar file for %s: stat err = %v, want not-exist", rel, err)
	}
}

// wantBase asserts the base record of rel has sha and Hash(data), and the
// base bytes are data.
func (fx *pl13Fixture) wantBase(rel, sha string, data []byte) {
	fx.t.Helper()
	b := fx.bases()
	rec, ok := b.Get(rel)
	if !ok {
		fx.t.Fatalf("no base record for %s", rel)
	}
	if rec.SHA != sha {
		fx.t.Fatalf("base record %s sha = %s, want %s", rel, rec.SHA, sha)
	}
	if rec.Hash != syncstate.Hash(data) {
		fx.t.Fatalf("base record %s hash = %s, want Hash(file) %s", rel, rec.Hash, syncstate.Hash(data))
	}
	got, err := b.Read(rel)
	if err != nil {
		fx.t.Fatalf("read base %s: %v", rel, err)
	}
	if !bytes.Equal(got, data) {
		fx.t.Fatalf("base bytes of %s = %q, want %q", rel, got, data)
	}
}

// wantPageSHA asserts page_shas[rel] == sha ("" = absent).
func (fx *pl13Fixture) wantPageSHA(rel, sha string) {
	fx.t.Helper()
	got, ok := fx.state().PageSHAs[rel]
	if sha == "" && ok {
		fx.t.Fatalf("page_shas[%s] = %s, want absent", rel, got)
	}
	if got != sha {
		fx.t.Fatalf("page_shas[%s] = %q, want %q", rel, got, sha)
	}
}

// pl13Counting wraps a Remote and records how many ReadPage calls run at
// once.
type pl13Counting struct {
	Remote
	inflight, max atomic.Int32
}

func (c *pl13Counting) ReadPage(repoID, path string) ([]byte, string, error) {
	n := c.inflight.Add(1)
	for {
		m := c.max.Load()
		if n <= m || c.max.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(25 * time.Millisecond)
	c.inflight.Add(-1)
	return c.Remote.ReadPage(repoID, path)
}

// ---- Step 1: Review Focus trio ----

func TestPull_NeverOverwritesModifiedPage(t *testing.T) {
	cases := []struct {
		name       string
		serverEdit bool
		state      contract.PageState
		reason     contract.SkipReason
	}{
		{"local edit only", false, contract.StateModified, contract.SkipModified},
		{"local and server edit", true, contract.StateConflict, contract.SkipConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := pl13NewFixture(t)
			rel, _ := fx.synced("alpha", "v1")
			wantSHA := ""
			if c.serverEdit {
				_, wantSHA = fx.serverEdit("alpha", "server v2")
			}
			fx.write(rel, pl13Page("alpha", "local edit"))
			check := pl13Untouched(t, fx.path(rel))

			doc := fx.pull()

			pl13WantSkip(t, doc, rel, c.state, c.reason, wantSHA)
			pl13WantPulled(t, "written", doc.Written, nil)
			check()
		})
	}
}

func TestPull_SkippedRecordedAsConflict_StateUntouched(t *testing.T) {
	fx := pl13NewFixture(t)
	rel, sb := fx.synced("alpha", "v1")
	base := fx.read(rel)
	server, s := fx.serverEdit("alpha", "server v2")
	fx.write(rel, pl13Page("alpha", "local edit"))

	doc := fx.pull()

	pl13WantSkip(t, doc, rel, contract.StateConflict, contract.SkipConflict, s)
	c := fx.conflicts()
	rec, ok := c.Get(rel)
	if !ok {
		t.Fatalf("no conflicts.json record for %s", rel)
	}
	want := syncstate.ConflictRecord{
		Path: rel, ServerSHA: s, BaseSHA: sb, Kind: syncstate.KindBothModified,
		Source: syncstate.SourcePull, DetectedAt: pl13Now, HasContent: true,
	}
	if rec != want {
		t.Fatalf("conflict record = %+v, want %+v", rec, want)
	}
	content, err := c.Content(rel)
	if err != nil {
		t.Fatalf("sidecar content: %v", err)
	}
	if !bytes.Equal(content, pl13Sanitized(server)) {
		t.Fatalf("sidecar content = %q, want the sanitized server bytes %q", content, pl13Sanitized(server))
	}
	fx.wantPageSHA(rel, sb)
	fx.wantBase(rel, sb, base)
}

func TestPull_ConflictSurvivesNextIncrementalPull(t *testing.T) {
	fx := pl13NewFixture(t)
	rel, _ := fx.synced("alpha", "v1")
	_, s := fx.serverEdit("alpha", "server v2")
	fx.write(rel, pl13Page("alpha", "local edit"))

	doc1 := fx.pull()
	pl13WantSkip(t, doc1, rel, contract.StateConflict, contract.SkipConflict, s)
	if got := fx.state().LastPullSHA; got != fx.Fake.Head() {
		t.Fatalf("last_pull_sha after pull #1 = %q, want Head %q", got, fx.Fake.Head())
	}

	doc2 := fx.pull()
	pl13WantSkip(t, doc2, rel, contract.StateConflict, contract.SkipConflict, s)

	view, err := syncstate.LoadView(fx.Hub, nil)
	if err != nil {
		t.Fatalf("LoadView: %v", err)
	}
	f := view.Facts[rel]
	if f == nil {
		t.Fatalf("offline view has no entry for %s", rel)
	}
	if !f.RemoteFromSidecar {
		t.Fatalf("offline remote side of %s is not from the sidecar: %+v", rel, f.Remote)
	}
	res, err := contract.ClassifySides(contract.Side(f.Local), contract.Side(f.Base), contract.Side(f.Remote))
	if err != nil {
		t.Fatalf("ClassifySides: %v", err)
	}
	var sc *SidecarFact
	if f.Conflict != nil {
		sc = &SidecarFact{ServerSHA: f.Conflict.ServerSHA, Kind: contract.ConflictKind(f.Conflict.Kind)}
	}
	res = ApplySidecar(res, f.Base.SHA, sc)
	if res.State != contract.StateConflict {
		t.Fatalf("offline state of %s = %q, want conflict", rel, res.State)
	}
}

// ---- Step 2: writes and adoption ----

func TestPull_OverwritesUnmodifiedIncoming_UpdatesBaseAndState(t *testing.T) {
	fx := pl13NewFixture(t)
	rel, sb := fx.synced("alpha", "v1")
	v2, s2 := fx.serverEdit("alpha", "server v2")
	fx.putSidecar(syncstate.ConflictRecord{
		Path: rel, ServerSHA: s2, BaseSHA: sb, Kind: syncstate.KindBothModified,
		Source: syncstate.SourcePush, DetectedAt: pl13Now.Add(-time.Hour),
	}, v2)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(fx.path(rel), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	doc := fx.pull()

	pl13WantPulled(t, "written", doc.Written, []contract.PulledPage{{Path: rel, SHA: s2, Kind: contract.WriteUpdate}})
	pl13WantPulled(t, "adopted", doc.Adopted, nil)
	if len(doc.Skipped) != 0 {
		t.Fatalf("skipped = %+v, want none", doc.Skipped)
	}
	file := fx.read(rel)
	if !bytes.Equal(file, pl13Sanitized(v2)) {
		t.Fatalf("file = %q, want the sanitized server v2 %q", file, pl13Sanitized(v2))
	}
	fx.wantBase(rel, s2, file)
	if rec, _ := fx.bases().Get(rel); !rec.SyncedAt.Equal(pl13Now) {
		t.Fatalf("base synced_at = %v, want Env.Now %v", rec.SyncedAt, pl13Now)
	}
	fx.wantPageSHA(rel, s2)
	head := fx.Fake.Head()
	if got := fx.state().LastPullSHA; got != head {
		t.Fatalf("last_pull_sha = %q, want Head %q", got, head)
	}
	if doc.Head != head {
		t.Fatalf("doc.Head = %q, want %q", doc.Head, head)
	}
	if !doc.Reindexed {
		t.Fatal("doc.Reindexed = false after a write")
	}
	fx.wantNoSidecar(rel)
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(fx.path(rel))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("updated file mode = %v, want the existing 0600 kept (R13)", fi.Mode().Perm())
		}
	}
}

func TestPull_IncomingNewWritten(t *testing.T) {
	fx := pl13NewFixture(t)
	rel := pl13Rel("fresh")
	data := pl13Page("fresh", "from a teammate")
	s := fx.Fake.Seed(rel, data)
	// The parent directory does not exist yet: pull creates it.
	if err := os.Remove(filepath.Join(fx.Hub, "wiki", "concepts")); err != nil {
		t.Fatal(err)
	}

	doc := fx.pull()

	pl13WantPulled(t, "written", doc.Written, []contract.PulledPage{{Path: rel, SHA: s, Kind: contract.WriteCreate}})
	file := fx.read(rel)
	if !bytes.Equal(file, pl13Sanitized(data)) {
		t.Fatalf("file = %q, want %q", file, pl13Sanitized(data))
	}
	fx.wantBase(rel, s, file)
	fx.wantPageSHA(rel, s)
	if got := fx.state().LastPullSHA; got != fx.Fake.Head() {
		t.Fatalf("last_pull_sha = %q, want Head %q", got, fx.Fake.Head())
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(fx.path(rel))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o644 {
			t.Fatalf("created file mode = %v, want 0644 (R13)", fi.Mode().Perm())
		}
	}
}

func TestPull_IncomingNewCaseVariantSkipped(t *testing.T) {
	fx := pl13NewFixture(t)
	local := pl13Rel("card-vault")
	fx.write(local, pl13Page("card-vault", "mine"))
	server := "wiki/concepts/Card-Vault.md"
	s := fx.Fake.Seed(server, pl13Page("Card-Vault", "theirs"))
	check := pl13Untouched(t, fx.path(local))

	doc := fx.pull()

	pl13WantSkip(t, doc, server, contract.StateIncomingNew, contract.SkipCaseCollision, s)
	pl13WantSkip(t, doc, local, contract.StateNew, contract.SkipCaseCollision, "")
	pl13WantPulled(t, "written", doc.Written, nil)
	check()
	entries, err := os.ReadDir(filepath.Join(fx.Hub, "wiki", "concepts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "Card-Vault.md" {
			t.Fatal("pull wrote wiki/concepts/Card-Vault.md")
		}
	}
	w := pl13Warning(doc, contract.WarnCaseCollision)
	if w == nil {
		t.Fatalf("no case_collision warning: %+v", doc.Warnings)
	}
	wantPaths := []string{server, local} // sorted: 'C' < 'c'
	if !reflect.DeepEqual(w.Paths, wantPaths) {
		t.Fatalf("case_collision paths = %v, want %v", w.Paths, wantPaths)
	}
	wantMsg := "2 paths differ only by letter case; pull skipped both: " + server + ", " + local
	if w.Message != wantMsg {
		t.Fatalf("case_collision message = %q, want %q", w.Message, wantMsg)
	}
}

func TestPull_IncomingNewCaseVariantDirSkipped(t *testing.T) {
	fx := pl13NewFixture(t)
	fx.synced("alpha", "v1") // a local page in wiki/concepts/
	server := "Wiki/concepts/other.md"
	s := fx.Fake.Seed(server, pl13Page("other", "theirs"))

	doc := fx.pull()

	pl13WantSkip(t, doc, server, contract.StateIncomingNew, contract.SkipCaseCollision, s)
	pl13WantPulled(t, "written", doc.Written, nil)
	entries, err := os.ReadDir(fx.Hub)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "Wiki" {
			t.Fatal("pull created .contexo/Wiki next to .contexo/wiki")
		}
	}
	// A disk-level case variant adds no warning (R2).
	if w := pl13Warning(doc, contract.WarnCaseCollision); w != nil {
		t.Fatalf("unexpected case_collision warning: %+v", *w)
	}
}

func TestPull_AdoptsContentEqualConflict(t *testing.T) {
	fx := pl13NewFixture(t)
	rel, sb := fx.synced("alpha", "v1")
	x, s2 := fx.serverEdit("alpha", "same edit on both sides")
	fx.write(rel, pl13BOM(x))
	fx.putSidecar(syncstate.ConflictRecord{
		Path: rel, ServerSHA: s2, BaseSHA: sb, Kind: syncstate.KindBothModified,
		Source: syncstate.SourcePush, DetectedAt: pl13Now.Add(-time.Hour),
	}, x)
	check := pl13Untouched(t, fx.path(rel))

	doc := fx.pull()

	pl13WantPulled(t, "adopted", doc.Adopted, []contract.PulledPage{{Path: rel, SHA: s2}})
	pl13WantPulled(t, "written", doc.Written, nil)
	if s := pl13Skip(doc, rel); s != nil {
		t.Fatalf("adopted page also skipped: %+v", *s)
	}
	check()
	fx.wantPageSHA(rel, s2)
	fx.wantBase(rel, s2, pl13Sanitized(x))
	fx.wantNoSidecar(rel)
}

// pl13Unverified builds a page whose page_shas names the older server
// version S0 with no base record (stale), the server being at S1.
func pl13Unverified(t *testing.T, fx *pl13Fixture) (rel, s0, s1 string, v1 []byte) {
	t.Helper()
	pl13NoEnsureBase(t)
	rel = pl13Rel("alpha")
	s0 = fx.Fake.Seed(rel, pl13Page("alpha", "v0"))
	v1 = pl13Page("alpha", "v1")
	s1 = fx.Fake.Seed(rel, v1)
	st := fx.state()
	st.PageSHAs[rel] = s0
	fx.saveState(st)
	return rel, s0, s1, v1
}

func TestPull_UnverifiedAdoptedWhenEqual(t *testing.T) {
	fx := pl13NewFixture(t)
	rel, _, s1, v1 := pl13Unverified(t, fx)
	fx.write(rel, v1)
	check := pl13Untouched(t, fx.path(rel))

	doc := fx.pull()

	pl13WantPulled(t, "adopted", doc.Adopted, []contract.PulledPage{{Path: rel, SHA: s1}})
	pl13WantPulled(t, "written", doc.Written, nil)
	check()
	fx.wantBase(rel, s1, pl13Sanitized(v1))
	fx.wantPageSHA(rel, s1)
}

func TestPull_UnverifiedSkippedWhenDifferent(t *testing.T) {
	fx := pl13NewFixture(t)
	rel, s0, s1, _ := pl13Unverified(t, fx)
	fx.write(rel, pl13Page("alpha", "local edit"))
	check := pl13Untouched(t, fx.path(rel))

	doc := fx.pull()

	pl13WantSkip(t, doc, rel, contract.StateUnverified, contract.SkipUnverified, s1)
	pl13WantPulled(t, "adopted", doc.Adopted, nil)
	pl13WantPulled(t, "written", doc.Written, nil)
	check()
	fx.wantNoSidecar(rel)
	fx.wantPageSHA(rel, s0)
	if rec, ok := fx.bases().Get(rel); ok {
		t.Fatalf("unverified skip stored a base: %+v", rec)
	}
}

func TestPull_ModifiedSkipWritesNoSidecar(t *testing.T) {
	cases := []struct {
		name   string
		state  contract.PageState
		reason contract.SkipReason
		// setup changes the synced page rel and reports the server sha the
		// skip carries (R1).
		setup func(fx *pl13Fixture, rel string) string
	}{
		{"modified", contract.StateModified, contract.SkipModified, func(fx *pl13Fixture, rel string) string {
			fx.write(rel, pl13Page("alpha", "local edit"))
			return ""
		}},
		{"invalid", contract.StateInvalid, contract.SkipInvalid, func(fx *pl13Fixture, rel string) string {
			fx.write(rel, []byte("---\nslug: [unclosed\n---\nbody\n"))
			_, s2 := fx.serverEdit("alpha", "server v2")
			return s2
		}},
		{"deleted_local", contract.StateDeletedLocal, contract.SkipDeletedLocal, func(fx *pl13Fixture, rel string) string {
			fx.remove(rel)
			_, s2 := fx.serverEdit("alpha", "server v2")
			return s2
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := pl13NewFixture(t)
			rel, _ := fx.synced("alpha", "v1")
			wantSHA := c.setup(fx, rel)

			doc := fx.pull()

			pl13WantSkip(t, doc, rel, c.state, c.reason, wantSHA)
			fx.wantNoSidecar(rel)
		})
	}
}

func TestPull_DeletedLocalNotResurrected(t *testing.T) {
	fx := pl13NewFixture(t)
	rel, sb := fx.synced("alpha", "v1")
	fx.remove(rel)
	_, s2 := fx.serverEdit("alpha", "server v2")

	doc := fx.pull()

	pl13WantSkip(t, doc, rel, contract.StateDeletedLocal, contract.SkipDeletedLocal, s2)
	pl13WantPulled(t, "written", doc.Written, nil)
	if _, err := os.Stat(fx.path(rel)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted page came back: stat err = %v", err)
	}
	fx.wantPageSHA(rel, sb)
}

func TestPull_OverlayConflict(t *testing.T) {
	fx := pl13NewFixture(t)
	rel, sb := fx.synced("alpha", "v1")
	base := fx.read(rel)
	fx.write(rel, pl13Page("alpha", "local edit"))
	other := strings.Repeat("a", 40)
	if other == sb {
		t.Fatal("test bug: the stand-in server sha equals the base sha")
	}
	fx.putSidecar(syncstate.ConflictRecord{
		Path: rel, ServerSHA: other, BaseSHA: sb, Kind: syncstate.KindBothModified,
		Source: syncstate.SourcePush, DetectedAt: pl13Now.Add(-time.Hour),
	}, pl13Page("alpha", "an older server version"))
	check := pl13Untouched(t, fx.path(rel))

	doc := fx.pull()

	pl13WantSkip(t, doc, rel, contract.StateConflict, contract.SkipConflict, sb)
	check()
	c := fx.conflicts()
	rec, ok := c.Get(rel)
	if !ok {
		t.Fatalf("no conflicts.json record for %s", rel)
	}
	want := syncstate.ConflictRecord{
		Path: rel, ServerSHA: sb, BaseSHA: sb, Kind: syncstate.KindBothModified,
		Source: syncstate.SourcePull, DetectedAt: pl13Now, HasContent: true,
	}
	if rec != want {
		t.Fatalf("sidecar = %+v, want it replaced with %+v (R8)", rec, want)
	}
	content, err := c.Content(rel)
	if err != nil {
		t.Fatalf("sidecar content: %v", err)
	}
	if !bytes.Equal(content, pl13Sanitized(base)) {
		t.Fatalf("sidecar content = %q, want the server (base) bytes %q", content, base)
	}
}

// ---- Step 3: races, sanitizing, failures ----

func TestPull_ChangedDuringPull(t *testing.T) {
	t.Run("incoming", func(t *testing.T) {
		fx := pl13NewFixture(t)
		rel, sb := fx.synced("alpha", "v1")
		base := fx.read(rel)
		_, s2 := fx.serverEdit("alpha", "server v2")
		user := pl13Page("alpha", "user bytes")
		fired := false
		pl13SetHook(t, func(r string) {
			if r == rel && !fired {
				fired = true
				fx.write(rel, user)
			}
		})

		doc := fx.pull()

		pl13WantSkip(t, doc, rel, contract.StateIncoming, contract.SkipChangedDuringPull, s2)
		pl13WantPulled(t, "written", doc.Written, nil)
		if got := fx.read(rel); !bytes.Equal(got, user) {
			t.Fatalf("file = %q, want the user's bytes %q", got, user)
		}
		fx.wantPageSHA(rel, sb)
		fx.wantBase(rel, sb, base)
		fx.wantNoSidecar(rel)
	})
	t.Run("incoming_new", func(t *testing.T) {
		fx := pl13NewFixture(t)
		rel := pl13Rel("fresh")
		s := fx.Fake.Seed(rel, pl13Page("fresh", "theirs"))
		user := pl13Page("fresh", "user bytes")
		fired := false
		pl13SetHook(t, func(r string) {
			if r == rel && !fired {
				fired = true
				fx.write(rel, user)
			}
		})

		doc := fx.pull()

		pl13WantSkip(t, doc, rel, contract.StateIncomingNew, contract.SkipChangedDuringPull, s)
		pl13WantPulled(t, "written", doc.Written, nil)
		if got := fx.read(rel); !bytes.Equal(got, user) {
			t.Fatalf("file = %q, want the user's bytes %q", got, user)
		}
		fx.wantPageSHA(rel, "")
		fx.wantNoSidecar(rel)
	})
}

func TestPull_ChangedDuringPullBeforeAdopt(t *testing.T) {
	fx := pl13NewFixture(t)
	rel, sb := fx.synced("alpha", "v1")
	x, s2 := fx.serverEdit("alpha", "same edit on both sides")
	fx.write(rel, x)
	hookBytes := pl13Page("alpha", "edited while pulling")
	fired := false
	pl13SetHook(t, func(r string) {
		if r == rel && !fired {
			fired = true
			fx.write(rel, hookBytes)
		}
	})

	doc := fx.pull()

	pl13WantSkip(t, doc, rel, contract.StateConflict, contract.SkipChangedDuringPull, s2)
	pl13WantPulled(t, "adopted", doc.Adopted, nil)
	pl13WantPulled(t, "written", doc.Written, nil)
	if got := fx.read(rel); !bytes.Equal(got, hookBytes) {
		t.Fatalf("file = %q, want the hook's bytes %q", got, hookBytes)
	}
	fx.wantNoSidecar(rel)
	fx.wantPageSHA(rel, sb)
}

func TestPull_SanitizedBytesEqualBase(t *testing.T) {
	fx := pl13NewFixture(t)
	rel := pl13Rel("hostile")
	server := pl13Page("hostile", "red \x1b[31malert\x1b[0m and ‮reversed‬ text")
	s := fx.Fake.Seed(rel, server)
	want := pl13Sanitized(server)
	if bytes.Contains(want, []byte("\x1b")) || bytes.Contains(want, []byte("‮")) {
		t.Fatalf("test bug: sanitized bytes still hold ESC or U+202E: %q", want)
	}
	if bytes.Equal(want, server) {
		t.Fatal("test bug: sanitizing changed nothing")
	}

	doc := fx.pull()

	pl13WantPulled(t, "written", doc.Written, []contract.PulledPage{{Path: rel, SHA: s, Kind: contract.WriteCreate}})
	file := fx.read(rel)
	if !bytes.Equal(file, want) {
		t.Fatalf("file = %q, want the sanitized server bytes %q", file, want)
	}
	if bytes.Contains(file, []byte("\x1b")) || bytes.Contains(file, []byte("‮")) {
		t.Fatalf("written file still holds ESC or U+202E: %q", file)
	}
	fx.wantBase(rel, s, file)

	check := pl13Untouched(t, fx.path(rel))
	doc2 := fx.pull()
	pl13WantPulled(t, "second pull written", doc2.Written, nil)
	pl13WantPulled(t, "second pull adopted", doc2.Adopted, nil)
	if len(doc2.Skipped) != 0 {
		t.Fatalf("second pull skipped = %+v, want none", doc2.Skipped)
	}
	check()
}

// pl13FetchFixture has one incoming, one incoming_new and one conflict page
// and last_pull_sha "".
func pl13FetchFixture(t *testing.T) *pl13Fixture {
	t.Helper()
	fx := pl13NewFixture(t)
	fx.synced("inc", "v1")
	fx.serverEdit("inc", "server v2")
	fx.Fake.Seed(pl13Rel("fresh"), pl13Page("fresh", "theirs"))
	rel, _ := fx.synced("both", "v1")
	fx.serverEdit("both", "server v2")
	fx.write(rel, pl13Page("both", "local edit"))
	return fx
}

func TestPull_FetchFailureWritesNothing(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name   string
		method string
		err    error
		// batchMisses sets last_pull_sha to Head(), so the needed pages are
		// missing from the batch and fetched with ReadPage.
		batchMisses bool
		want        contract.ErrorCode
	}{
		{"list pages network", "ListPages", &sync.NetworkError{Op: "list pages", Err: boom}, false, contract.CodeNetwork},
		{"pull pages 429", "PullPages",
			sync.NewHTTPError(429, []byte(`{"error":"slow down"}`), "sync: pull failed (429)", nil, false),
			false, contract.CodeRateLimited},
		{"pull pages 401", "PullPages",
			sync.NewHTTPError(401, []byte(`{"error":"invalid token"}`), "sync: pull failed (401)", nil, false),
			false, contract.CodeAuthRejected},
		{"pull pages 503", "PullPages",
			sync.NewHTTPError(503, []byte(`{"error":"unavailable"}`), "sync: pull failed (503)", nil, false),
			false, contract.CodeServerError},
		{"read page network", "ReadPage", &sync.NetworkError{Op: "read page", Err: boom}, true, contract.CodeNetwork},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := pl13FetchFixture(t)
			if c.batchMisses {
				fx.setLastPull(fx.Fake.Head())
			}
			fx.Fake.FailNext(c.method, c.err)
			before := pl13TreeHash(t, fx.Hub)

			_, err := fx.pullErr()

			if err == nil {
				t.Fatalf("Pull succeeded, want a %s error", c.want)
			}
			if got := ui.ToContractError(err).Code; got != c.want {
				t.Fatalf("Pull error code = %q (%v), want %q", got, err, c.want)
			}
			if after := pl13TreeHash(t, fx.Hub); after != before {
				t.Fatal("a failed pull changed files under .contexo")
			}
		})
	}
}

func TestPull_ServerPathEscapeNeverWritten(t *testing.T) {
	fx := pl13NewFixture(t)
	bad := []string{"../evil.md", "wiki/../../evil2.md", ".sync/state.json", "/abs.md"}
	for _, p := range bad {
		fx.Fake.Seed(p, []byte("pwned\n"))
	}
	ok := pl13Rel("ok")
	s := fx.Fake.Seed(ok, pl13Page("ok", "fine"))

	doc := fx.pull()

	pl13WantPulled(t, "written", doc.Written, []contract.PulledPage{{Path: ok, SHA: s, Kind: contract.WriteCreate}})
	for _, dir := range []string{fx.Hub, fx.Root, filepath.Dir(fx.Root)} {
		for _, name := range []string{"evil.md", "evil2.md", "abs.md"} {
			if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s exists (stat err %v): pull wrote outside its page paths", filepath.Join(dir, name), err)
			}
		}
	}
	raw, err := os.ReadFile(sync.StatePath(fx.Hub))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	if bytes.Equal(raw, []byte("pwned\n")) {
		t.Fatal("pull wrote the server's .sync/state.json")
	}
	if _, err := sync.LoadState(fx.Hub); err != nil {
		t.Fatalf("state.json no longer parses: %v", err)
	}
	isBad := map[string]bool{}
	for _, p := range bad {
		isBad[p] = true
	}
	for _, p := range append(append(pl13PulledPaths(doc.Written), pl13PulledPaths(doc.Adopted)...), pl13SkippedPaths(doc.Skipped)...) {
		if isBad[p] {
			t.Errorf("document has an entry for the bad path %q: %+v", p, doc)
		}
	}
}

func TestPull_LockAndEnsureBaseLocked(t *testing.T) {
	fx := pl13NewFixture(t)
	fx.synced("alpha", "v1")
	fx.Fake.Seed(pl13Rel("beta"), pl13Page("beta", "theirs"))
	listing, err := fx.Fake.ListPages(pl13RepoID)
	if err != nil {
		t.Fatal(err)
	}
	var wantPaths []string
	for _, m := range listing {
		wantPaths = append(wantPaths, m.Path)
	}

	calls := 0
	var got EnsureBaseOptions
	probeOK, probeErr := true, error(nil)
	prev := pullEnsureBase
	pullEnsureBase = func(ctx context.Context, env Env, o EnsureBaseOptions) (*EnsureBaseResult, error) {
		calls++
		got = o
		unlock, ok, err := syncstate.TryLock(env.HubDir, "probe")
		probeOK, probeErr = ok, err
		if ok {
			unlock()
		}
		return &EnsureBaseResult{Repaired: []string{}, Unverified: []string{}, Warnings: []contract.Warning{}}, nil
	}
	t.Cleanup(func() { pullEnsureBase = prev })

	fx.pull()

	if calls != 1 {
		t.Fatalf("pullEnsureBase called %d times, want 1", calls)
	}
	if !got.Locked {
		t.Fatal("EnsureBaseOptions.Locked = false; pull must pass Locked: true")
	}
	var gotPaths []string
	for _, m := range got.Listing {
		gotPaths = append(gotPaths, m.Path)
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("EnsureBaseOptions.Listing paths = %v, want %v", gotPaths, wantPaths)
	}
	if probeErr != nil {
		t.Fatalf("TryLock inside EnsureBase: %v", probeErr)
	}
	if probeOK {
		t.Fatal("TryLock inside EnsureBase succeeded: pull does not hold .sync/lock")
	}
	unlock, ok, err := syncstate.TryLock(fx.Hub, "probe")
	if err != nil || !ok {
		t.Fatalf("TryLock after Pull = %v, %v; want the lock released", ok, err)
	}
	unlock()
}

func TestPull_VanishedPageSkipped(t *testing.T) {
	fx := pl13NewFixture(t)
	rel, sb := fx.synced("alpha", "v1")
	fx.serverEdit("alpha", "server v2")
	fx.setLastPull(fx.Fake.Head())
	fx.Fake.FailNext("ReadPage", sync.NewHTTPError(404,
		[]byte(`{"code":"page_not_found","error":"page not found"}`),
		"sync: page not found", sync.ErrPageNotFound, false))
	check := pl13Untouched(t, fx.path(rel))

	doc := fx.pull()

	pl13WantSkip(t, doc, rel, contract.StateIncoming, contract.SkipChangedDuringPull, "")
	pl13WantPulled(t, "written", doc.Written, nil)
	check()
	fx.wantPageSHA(rel, sb)
}

func TestPull_CorruptStateAdoptsEqualNeverOverwrites(t *testing.T) {
	fx := pl13NewFixture(t)
	eq, edited := pl13Rel("equal"), pl13Rel("edited")
	eqBytes := pl13Page("equal", "same")
	editedServer := pl13Page("edited", "server")
	sEq := fx.Fake.Seed(eq, eqBytes)
	sEdited := fx.Fake.Seed(edited, editedServer)
	fx.write(eq, eqBytes)
	fx.write(edited, pl13Page("edited", "local edit"))
	if err := syncstate.EnsureSyncDir(fx.Hub); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sync.StatePath(fx.Hub), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	check := pl13Untouched(t, fx.path(edited))

	doc := fx.pull()

	w := pl13Warning(doc, contract.WarnStateUnreadable)
	if w == nil {
		t.Fatalf("no state_unreadable warning: %+v", doc.Warnings)
	}
	if !slices.Contains(w.Paths, ".sync/state.json") {
		t.Fatalf("state_unreadable paths = %v, want .sync/state.json among them", w.Paths)
	}
	pl13WantPulled(t, "adopted", doc.Adopted, []contract.PulledPage{{Path: eq, SHA: sEq}})
	pl13WantPulled(t, "written", doc.Written, nil)
	pl13WantSkip(t, doc, edited, contract.StateConflict, contract.SkipConflict, sEdited)
	check()
	rec, ok := fx.conflicts().Get(edited)
	if !ok {
		t.Fatalf("no sidecar for %s", edited)
	}
	if rec.Kind != syncstate.KindBothAdded || rec.BaseSHA != "" || rec.ServerSHA != sEdited || rec.Source != syncstate.SourcePull {
		t.Fatalf("sidecar = %+v, want both_added, no base_sha, server_sha %s, source pull", rec, sEdited)
	}
	fx.wantPageSHA(eq, sEq)
	fx.wantPageSHA(edited, "")
}

func TestPull_SinceRejectedRetriesFull(t *testing.T) {
	fx := pl13NewFixture(t)
	rel, _ := fx.synced("alpha", "v1")
	_, s2 := fx.serverEdit("alpha", "server v2")
	fx.setLastPull(strings.Repeat("e", 40))

	doc := fx.pull()

	if got := fx.Fake.Requests()["PullPages"]; got != 2 {
		t.Fatalf("PullPages calls = %d, want 2 (the rejected since, then since \"\")", got)
	}
	pl13WantPulled(t, "written", doc.Written, []contract.PulledPage{{Path: rel, SHA: s2, Kind: contract.WriteUpdate}})
	head := fx.Fake.Head()
	if doc.Head != head || fx.state().LastPullSHA != head {
		t.Fatalf("doc.Head = %q, last_pull_sha = %q; want Head %q", doc.Head, fx.state().LastPullSHA, head)
	}
}

func TestPull_RepoNotFound(t *testing.T) {
	fx := pl13FetchFixture(t)
	fx.Fake.FailNext("ListPages", sync.NewHTTPError(404,
		[]byte(`{"code":"repo_not_found","error":"repo not found"}`),
		"sync: list pages (404)", nil, false))
	before := pl13TreeHash(t, fx.Hub)

	_, err := fx.pullErr()

	if err == nil {
		t.Fatal("Pull succeeded, want repo_not_found")
	}
	if got := ui.ToContractError(err).Code; got != contract.CodeRepoNotFound {
		t.Fatalf("Pull error code = %q (%v), want repo_not_found", got, err)
	}
	if after := pl13TreeHash(t, fx.Hub); after != before {
		t.Fatal("a failed pull changed files under .contexo")
	}
}

func TestPull_EnvGuards(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(t *testing.T, fx *pl13Fixture)
		want   contract.ErrorCode
		advice []string
	}{
		{"no remote", func(t *testing.T, fx *pl13Fixture) { fx.Env.Remote = nil },
			contract.CodeNotAuthenticated, []string{"ctx auth login"}},
		{"no repo id", func(t *testing.T, fx *pl13Fixture) { fx.Env.RepoID = "" },
			contract.CodeRepoNotConfigured, []string{"ctx remote set-repo <id>"}},
		{"no hub", func(t *testing.T, fx *pl13Fixture) {
			env := NewEnv(t.TempDir(), pl13RepoID, pl13ServerURL, Author{}, fx.Fake)
			env.Now = fx.Env.Now
			fx.Env = env
		}, contract.CodeNotInitialized, []string{"ctx init"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := pl13NewFixture(t)
			fx.synced("alpha", "v1")
			fx.serverEdit("alpha", "server v2")
			c.edit(t, fx)

			doc, err := fx.pullErr()

			if err == nil {
				t.Fatalf("Pull succeeded (%+v), want %s", doc, c.want)
			}
			ce := ui.ToContractError(err)
			if ce.Code != c.want {
				t.Fatalf("Pull error code = %q (%v), want %q", ce.Code, err, c.want)
			}
			if !reflect.DeepEqual(ce.Advice, c.advice) {
				t.Fatalf("advice = %#v, want %#v", ce.Advice, c.advice)
			}
			if got := fx.requests(); len(got) != 0 {
				t.Fatalf("a refused pull made remote calls: %v", got)
			}
		})
	}
}

func TestPull_ReindexFailureIsWarning(t *testing.T) {
	fx := pl13NewFixture(t)
	index := filepath.Join(fx.Hub, "index.md")
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(index, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(index, "keep.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, _ := fx.synced("alpha", "v1")
	_, s2 := fx.serverEdit("alpha", "server v2")

	doc := fx.pull()

	pl13WantPulled(t, "written", doc.Written, []contract.PulledPage{{Path: rel, SHA: s2, Kind: contract.WriteUpdate}})
	if doc.Reindexed {
		t.Fatal("doc.Reindexed = true, but index.md is a directory")
	}
	w := pl13Warning(doc, contract.WarnReindexFailed)
	if w == nil {
		t.Fatalf("no reindex_failed warning: %+v", doc.Warnings)
	}
	if !strings.HasPrefix(w.Message, "reindex: ") {
		t.Fatalf("reindex_failed message = %q, want a \"reindex: \" prefix", w.Message)
	}
	if !reflect.DeepEqual(w.Paths, []string{"index.md", "tags.md"}) {
		t.Fatalf("reindex_failed paths = %v, want [index.md tags.md]", w.Paths)
	}
}

// ---- Step 4: request budget ----

// pl13ManyIncoming syncs n pages, sets last_pull_sha to Head() when
// headFirst, then edits every page on the server. It returns the pages'
// paths and new server shas.
func pl13ManyIncoming(t *testing.T, fx *pl13Fixture, n int, headFirst bool) map[string]string {
	t.Helper()
	slugs := make([]string, n)
	for i := range slugs {
		slugs[i] = fmt.Sprintf("page-%03d", i)
		fx.synced(slugs[i], "v1")
	}
	if headFirst {
		fx.setLastPull(fx.Fake.Head())
	}
	want := map[string]string{}
	for _, s := range slugs {
		_, sha := fx.serverEdit(s, "server v2")
		want[pl13Rel(s)] = sha
	}
	if !headFirst {
		fx.setLastPull(fx.Fake.Head())
	}
	return want
}

// pl13WantAllWritten asserts doc wrote exactly the pages of want as updates.
func pl13WantAllWritten(t *testing.T, doc *contract.PullDoc, want map[string]string) {
	t.Helper()
	exp := []contract.PulledPage{}
	for _, rel := range pl13SortedPaths(want) {
		exp = append(exp, contract.PulledPage{Path: rel, SHA: want[rel], Kind: contract.WriteUpdate})
	}
	pl13WantPulled(t, "written", doc.Written, exp)
}

func pl13SortedPaths(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestPull_RequestBudget(t *testing.T) {
	fx := pl13NewFixture(t)
	want := pl13ManyIncoming(t, fx, 50, true)

	doc := fx.pull()

	if got := fx.requests(); !reflect.DeepEqual(got, map[string]int{"ListPages": 1, "PullPages": 1}) {
		t.Fatalf("requests = %v, want ListPages 1, PullPages 1 and nothing else", got)
	}
	pl13WantAllWritten(t, doc, want)
}

func TestPull_UpToDateBudget(t *testing.T) {
	fx := pl13NewFixture(t)
	_, first := fx.synced("alpha", "v1")
	fx.synced("beta", "v1")
	fx.synced("gamma", "v1")
	fx.setLastPull(first)
	if first == fx.Fake.Head() {
		t.Fatal("test bug: the first seed is Head")
	}

	doc := fx.pull()

	if got := fx.requests(); !reflect.DeepEqual(got, map[string]int{"ListPages": 1, "PullPages": 1}) {
		t.Fatalf("requests = %v, want ListPages 1, PullPages 1 and nothing else", got)
	}
	pl13WantPulled(t, "written", doc.Written, nil)
	pl13WantPulled(t, "adopted", doc.Adopted, nil)
	if len(doc.Skipped) != 0 {
		t.Fatalf("skipped = %+v, want none", doc.Skipped)
	}
	if doc.Reindexed {
		t.Fatal("doc.Reindexed = true without a write")
	}
	if got := fx.state().LastPullSHA; got != fx.Fake.Head() {
		t.Fatalf("last_pull_sha = %q, want Head %q", got, fx.Fake.Head())
	}
}

func TestPull_MissingFromBatchFallback(t *testing.T) {
	t.Run("3 pages read one by one", func(t *testing.T) {
		fx := pl13NewFixture(t)
		want := pl13ManyIncoming(t, fx, 3, false)

		doc := fx.pull()

		req := fx.requests()
		if req["ReadPage"] != 3 || req["PullPages"] != 1 {
			t.Fatalf("requests = %v, want ReadPage 3, PullPages 1", req)
		}
		pl13WantAllWritten(t, doc, want)
	})
	t.Run("12 pages in one full batch", func(t *testing.T) {
		fx := pl13NewFixture(t)
		want := pl13ManyIncoming(t, fx, 12, false)

		doc := fx.pull()

		req := fx.requests()
		if req["PullPages"] != 2 || req["ReadPage"] != 0 {
			t.Fatalf("requests = %v, want PullPages 2, ReadPage 0", req)
		}
		pl13WantAllWritten(t, doc, want)
	})
	t.Run("8 pages at most 4 in flight", func(t *testing.T) {
		fx := pl13NewFixture(t)
		want := pl13ManyIncoming(t, fx, 8, false)
		counting := &pl13Counting{Remote: fx.Fake}
		fx.Env.Remote = counting

		doc := fx.pull()

		peak := counting.max.Load()
		t.Logf("max ReadPage calls in flight: %d", peak)
		if peak > 4 {
			t.Fatalf("max ReadPage calls in flight = %d, want <= 4", peak)
		}
		req := fx.requests()
		if req["ReadPage"] != 8 || req["PullPages"] != 1 {
			t.Fatalf("requests = %v, want ReadPage 8, PullPages 1", req)
		}
		pl13WantAllWritten(t, doc, want)
	})
}

func TestPull_Idempotent(t *testing.T) {
	fx := pl13NewFixture(t)
	inc, _ := fx.synced("inc", "v1")
	_, sInc := fx.serverEdit("inc", "server v2")
	fresh := pl13Rel("fresh")
	sFresh := fx.Fake.Seed(fresh, pl13Page("fresh", "theirs"))
	eq, _ := fx.synced("eq", "v1")
	x, sEq := fx.serverEdit("eq", "same edit on both sides")
	fx.write(eq, x)
	both, _ := fx.synced("both", "v1")
	_, sBoth := fx.serverEdit("both", "server v2")
	fx.write(both, pl13Page("both", "local edit"))

	doc1 := fx.pull()

	pl13WantPulled(t, "pull #1 written", doc1.Written, []contract.PulledPage{
		{Path: fresh, SHA: sFresh, Kind: contract.WriteCreate},
		{Path: inc, SHA: sInc, Kind: contract.WriteUpdate},
	})
	pl13WantPulled(t, "pull #1 adopted", doc1.Adopted, []contract.PulledPage{{Path: eq, SHA: sEq}})
	pl13WantSkip(t, doc1, both, contract.StateConflict, contract.SkipConflict, sBoth)
	c1, err := os.ReadFile(syncstate.ConflictsPath(fx.Hub))
	if err != nil {
		t.Fatalf("read conflicts.json: %v", err)
	}

	// A later clock: a rewritten sidecar would get a new detected_at.
	fx.Env.Now = func() time.Time { return pl13Now.Add(time.Hour) }
	doc2 := fx.pull()

	pl13WantPulled(t, "pull #2 written", doc2.Written, nil)
	pl13WantPulled(t, "pull #2 adopted", doc2.Adopted, nil)
	if !reflect.DeepEqual(doc2.Skipped, doc1.Skipped) {
		t.Fatalf("pull #2 skipped = %+v, want pull #1's %+v", doc2.Skipped, doc1.Skipped)
	}
	c2, err := os.ReadFile(syncstate.ConflictsPath(fx.Hub))
	if err != nil {
		t.Fatalf("read conflicts.json: %v", err)
	}
	if !bytes.Equal(c1, c2) {
		t.Fatalf("conflicts.json changed between pulls (R8 keeps detected_at):\n#1 %s\n#2 %s", c1, c2)
	}
}
