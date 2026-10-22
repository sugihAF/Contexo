package syncops

// Owner: WP-1.1. Engine tests for Status and FillStatusIdentity, on
// syncopstest.FakeRemote (package syncops, so the unexported seams are
// reachable). Tests that override statusEnsureBase or statusCheckGuard never
// run in parallel. Every scenario holds with the wave-0 stubs and with
// WP-1.5's real EnsureBase / WP-1.2's real CheckGuard: unverified pages are
// unrepairable (old server, listing sha != page_shas sha, no older base
// record) or the seam is overridden, and capture batches hold no concept or
// analysis page.

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
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncops/syncopstest"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
	"github.com/sugihAF/contexo/internal/ui"
)

// st11Now is st11Env's clock: +07:00 with nanoseconds, so every contract
// time must come out in UTC whole seconds.
var st11Now = time.Date(2026, 11, 3, 9, 14, 7, 123456789, time.FixedZone("+0700", 7*60*60))

// st11Env is an Env over a fresh temp root holding an empty .contexo, with
// HOME and USERPROFILE isolated and Now fixed to st11Now.
func st11Env(t testing.TB, remote Remote) Env {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Resolve symlinks (macOS /var -> /private/var) so doc.Root compares
	// equal to an absolute path.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".contexo"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := NewEnv(root, "acme-kb", "https://kb.acme.dev", Author{Name: "Ana", Email: "ana@acme.dev"}, remote)
	env.Now = func() time.Time { return st11Now }
	return env
}

// st11Write writes data at rel ('/'-separated, relative to the hub).
func st11Write(t testing.TB, env Env, rel string, data []byte) {
	t.Helper()
	path := filepath.Join(env.HubDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// st11Page is a valid page for spec (testkit.PageBytes).
func st11Page(spec testkit.PageSpec) []byte { return testkit.PageBytes(spec) }

// st11SeedBase records content verbatim as rel's base for sha (bases.json +
// base file) and sets page_shas[rel] = sha, as a push or pull leaves it.
func st11SeedBase(t testing.TB, env Env, rel string, content []byte, sha string) {
	t.Helper()
	bases, err := syncstate.OpenBases(env.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := bases.Put(rel, content, sha, env.Now()); err != nil {
		t.Fatal(err)
	}
	if err := bases.Save(); err != nil {
		t.Fatal(err)
	}
	st11SetPageSHA(t, env, rel, sha)
}

// st11SetPageSHA sets page_shas[rel] = sha in state.json only.
func st11SetPageSHA(t testing.TB, env Env, rel, sha string) {
	t.Helper()
	st, _, err := sync.LoadStateOrEmpty(env.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	st.PageSHAs[rel] = sha
	if err := sync.SaveState(env.HubDir, st); err != nil {
		t.Fatal(err)
	}
}

// st11SHA is a deterministic 40-hex sha that no FakeRemote commit has.
func st11SHA(n int) string {
	sum := sha256.Sum256([]byte{byte(n), 's', 't', '1', '1'})
	return hex.EncodeToString(sum[:20])
}

// st11TreeSum hashes every path under dir with its type and bytes.
func st11TreeSum(t testing.TB, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		h.Write([]byte(filepath.ToSlash(rel) + "\x00" + d.Type().String() + "\x00"))
		if d.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			h.Write(data)
		}
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// st11Entry returns doc's entry for path, failing the test when absent.
func st11Entry(t testing.TB, doc *contract.StatusDoc, path string) *contract.PageEntry {
	t.Helper()
	for i := range doc.Pages {
		if doc.Pages[i].Path == path {
			return &doc.Pages[i]
		}
	}
	t.Fatalf("no entry for %s in %v", path, st11Paths(doc))
	return nil
}

func st11Paths(doc *contract.StatusDoc) []string {
	out := []string{}
	for _, p := range doc.Pages {
		out = append(out, p.Path+"="+string(p.State))
	}
	return out
}

// st11Warning returns doc's warning with code, or nil.
func st11Warning(doc *contract.StatusDoc, code contract.WarningCode) *contract.Warning {
	for i := range doc.Warnings {
		if doc.Warnings[i].Code == code {
			return &doc.Warnings[i]
		}
	}
	return nil
}

// st11Status runs Status and fails the test on an error.
func st11Status(t testing.TB, env Env, o StatusOptions) *contract.StatusDoc {
	t.Helper()
	doc, err := Status(context.Background(), env, o)
	if err != nil {
		t.Fatalf("Status(%+v): %v", o, err)
	}
	st11CheckDoc(t, doc)
	return doc
}

// st11CheckTime asserts a contract time is UTC whole seconds.
func st11CheckTime(t testing.TB, what string, v *contract.Time) {
	t.Helper()
	if v != nil && (v.Location() != time.UTC || v.Nanosecond() != 0) {
		t.Errorf("%s = %v, want UTC whole seconds", what, *v)
	}
}

// st11CheckDoc asserts the document invariants of CONTRACT.md deviation 14
// and the status shape rules: never-null arrays, counts equal to the
// tally, sorted unique paths, conflict_kind iff conflict, invalid iff
// invalid, source none carries no listing facts, remote.error implies
// source none, authenticated implies live, offline implies unknown
// capabilities, server_outdated iff E13, base_repair_skipped only online
// and naming every unverified page (deviation 13), times UTC whole seconds.
func st11CheckDoc(t testing.TB, doc *contract.StatusDoc) {
	t.Helper()
	if doc == nil {
		t.Fatal("nil status document")
	}
	if doc.Schema != contract.SchemaStatus {
		t.Errorf("schema = %q", doc.Schema)
	}
	if doc.Pages == nil || doc.Warnings == nil || doc.Captures.Sessions == nil || doc.Server.Capabilities == nil {
		t.Errorf("null array: pages %v warnings %v sessions %v capabilities %v",
			doc.Pages == nil, doc.Warnings == nil, doc.Captures.Sessions == nil, doc.Server.Capabilities == nil)
	}
	if !filepath.IsAbs(doc.Root) || !filepath.IsAbs(doc.ContexoDir) {
		t.Errorf("root %q / contexo_dir %q not absolute", doc.Root, doc.ContexoDir)
	}
	live := doc.Remote.Source == contract.RemoteLive
	var tally contract.StateCounts
	unverified := []string{}
	for i, p := range doc.Pages {
		if i > 0 && doc.Pages[i-1].Path >= p.Path {
			t.Errorf("pages not sorted and unique at %q", p.Path)
		}
		if p.Tags == nil || p.Notes == nil {
			t.Errorf("%s: null tags or notes", p.Path)
		}
		if (p.ConflictKind != "") != (p.State == contract.StateConflict) {
			t.Errorf("%s: state %s with conflict_kind %q", p.Path, p.State, p.ConflictKind)
		}
		if (p.Invalid != nil) != (p.State == contract.StateInvalid) {
			t.Errorf("%s: state %s with invalid %+v", p.Path, p.State, p.Invalid)
		}
		if p.Local != nil {
			if p.Local.File != p.Path {
				t.Errorf("%s: local.file %q", p.Path, p.Local.File)
			}
			if (p.Local.Hash != "") == (p.State == contract.StateInvalid) {
				t.Errorf("%s: state %s with local.hash %q", p.Path, p.State, p.Local.Hash)
			}
		}
		if p.Base != nil && p.Base.Stale && (p.Base.Hash != "" || p.Base.SyncedAt != nil) {
			t.Errorf("%s: stale base carries hash/synced_at: %+v", p.Path, p.Base)
		}
		if r := p.Remote; r != nil && !live && (r.Author != "" || r.Email != "" || r.Time != nil || r.Message != "") {
			t.Errorf("%s: remote side without a live listing is not sha-only: %+v", p.Path, r)
		}
		if p.State == contract.StateUnverified {
			unverified = append(unverified, p.Path)
		}
		st11Tally(t, &tally, p)
		st11CheckTime(t, p.Path+" updated", p.Updated)
		if p.Base != nil {
			st11CheckTime(t, p.Path+" base.synced_at", p.Base.SyncedAt)
		}
		if p.Remote != nil {
			st11CheckTime(t, p.Path+" remote.time", p.Remote.Time)
		}
	}
	tally.Ahead = tally.Modified + tally.New
	tally.Behind = tally.Incoming + tally.IncomingNew
	if doc.Counts != tally {
		t.Errorf("counts = %+v, tally = %+v", doc.Counts, tally)
	}

	switch doc.Remote.Source {
	case contract.RemoteLive:
		if doc.Remote.FetchedAt == nil || doc.Remote.RepoExists == nil || doc.Remote.Error != nil {
			t.Errorf("live remote = %+v", doc.Remote)
		}
	case contract.RemoteNone:
		if doc.Remote.FetchedAt != nil || doc.Remote.RepoExists != nil || doc.Remote.Head != "" || doc.Remote.PageCount != 0 {
			t.Errorf("source none carries listing facts: %+v", doc.Remote)
		}
	default:
		t.Errorf("remote.source = %q", doc.Remote.Source)
	}
	if doc.Offline && doc.Server.CapabilitiesKnown {
		t.Error("offline document with capabilities_known true")
	}
	if doc.Auth.State == contract.AuthAuthenticated && !live {
		t.Error("auth authenticated without a live listing")
	}

	codes := map[contract.WarningCode]bool{}
	for _, w := range doc.Warnings {
		if w.Paths == nil {
			t.Errorf("warning %s: null paths", w.Code)
		}
		if !slices.IsSorted(w.Paths) {
			t.Errorf("warning %s: paths not sorted: %v", w.Code, w.Paths)
		}
		if codes[w.Code] {
			t.Errorf("warning %s emitted twice", w.Code)
		}
		codes[w.Code] = true
		if w.Code == contract.WarnRemoteStale {
			t.Error("reserved warning remote_stale emitted")
		}
	}
	missing := false
	for _, c := range []contract.ServerCapability{contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA} {
		missing = missing || !slices.Contains(doc.Server.Capabilities, c)
	}
	if want := doc.Server.CapabilitiesKnown && missing; codes[contract.WarnServerOutdated] != want {
		t.Errorf("server_outdated = %v, want %v (known %v, caps %v)",
			codes[contract.WarnServerOutdated], want, doc.Server.CapabilitiesKnown, doc.Server.Capabilities)
	}
	if slices.Contains(doc.Server.Capabilities, contract.SrvPagesETag) {
		t.Error("reserved capability pages.etag emitted")
	}
	brs := st11Warning(doc, contract.WarnBaseRepairSkipped)
	if brs != nil && !live {
		t.Errorf("base_repair_skipped without a live listing: %+v", brs)
	}
	if live && len(unverified) > 0 {
		for _, p := range unverified {
			if brs == nil || !slices.Contains(brs.Paths, p) {
				t.Errorf("unverified %s not named by base_repair_skipped %+v", p, brs)
			}
		}
	}

	st11CheckTime(t, "generated_at", &doc.GeneratedAt)
	st11CheckTime(t, "remote.fetched_at", doc.Remote.FetchedAt)
	for _, s := range doc.Captures.Sessions {
		st11CheckTime(t, "session "+s.SessionID, &s.UpdatedAt)
	}
}

func st11Tally(t testing.TB, c *contract.StateCounts, p contract.PageEntry) {
	t.Helper()
	switch p.State {
	case contract.StateUnchanged:
		c.Unchanged++
	case contract.StateModified:
		c.Modified++
	case contract.StateNew:
		c.New++
	case contract.StateIncoming:
		c.Incoming++
	case contract.StateIncomingNew:
		c.IncomingNew++
	case contract.StateConflict:
		c.Conflict++
	case contract.StateDeletedLocal:
		c.DeletedLocal++
	case contract.StateInvalid:
		c.Invalid++
	case contract.StateUnverified:
		c.Unverified++
	default:
		t.Errorf("%s: unknown state %q", p.Path, p.State)
	}
}

// st11Exists reports whether path exists.
func st11Exists(t testing.TB, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestStatus_Offline_ClassifiesLocal(t *testing.T) {
	env := st11Env(t, nil)
	const (
		same     = "wiki/concepts/same.md"
		edited   = "wiki/concepts/edited.md"
		fresh    = "wiki/entities/fresh.md"
		broken   = "wiki/concepts/broken.md"
		noauthor = "wiki/concepts/noauthor.md"
		moved    = "wiki/concepts/moved.md"
		gone     = "wiki/concepts/gone.md"
	)
	sameBytes := st11Page(testkit.PageSpec{Slug: "same", Title: "Same page", Tags: []string{"retry", "api"}})
	st11Write(t, env, same, sameBytes)
	st11SeedBase(t, env, same, sameBytes, st11SHA(1))

	st11SeedBase(t, env, edited, st11Page(testkit.PageSpec{Slug: "edited", Body: "v1"}), st11SHA(2))
	editedBytes := st11Page(testkit.PageSpec{Slug: "edited", Body: "v2"})
	st11Write(t, env, edited, editedBytes)

	st11Write(t, env, fresh, st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "fresh"}))
	st11Write(t, env, broken, []byte("---\nschema: [\n---\n"))
	na, err := schema.SerializePage(&schema.Page{
		Frontmatter: schema.PageFrontmatter{
			Schema: schema.PageSchemaV1, Slug: "noauthor", Type: schema.TypeConcept,
			Created: st11Now, Updated: st11Now,
		},
		Body: "# No author\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	st11Write(t, env, noauthor, na)
	st11Write(t, env, moved, st11Page(testkit.PageSpec{Slug: "elsewhere"}))
	goneBytes := st11Page(testkit.PageSpec{Slug: "gone"})
	st11SeedBase(t, env, gone, goneBytes, st11SHA(3))

	before := st11TreeSum(t, env.HubDir)
	lockExisted := st11Exists(t, syncstate.LockPath(env.HubDir))
	doc := st11Status(t, env, StatusOptions{Offline: true})
	if after := st11TreeSum(t, env.HubDir); after != before {
		t.Fatal("offline status changed the .contexo tree")
	}
	if !lockExisted && st11Exists(t, syncstate.LockPath(env.HubDir)) {
		t.Fatal("offline status left .sync/lock")
	}

	if !doc.Offline || doc.Remote.Source != contract.RemoteNone || doc.Remote.Error != nil {
		t.Fatalf("offline = %v, remote = %+v", doc.Offline, doc.Remote)
	}
	if doc.Server.CapabilitiesKnown || len(doc.Server.Capabilities) != 0 || doc.Server.Version != "" {
		t.Fatalf("server = %+v", doc.Server)
	}
	if doc.Auth.State != "" {
		t.Fatalf("auth.state = %q, want \"\" (no request made; FillStatusIdentity decides)", doc.Auth.State)
	}
	if doc.Root != env.Root || doc.ContexoDir != filepath.Join(env.Root, ".contexo") {
		t.Fatalf("root %q contexo_dir %q", doc.Root, doc.ContexoDir)
	}
	if doc.Project.RepoID != "acme-kb" || !doc.GeneratedAt.Equal(contract.TimeOf(st11Now)) {
		t.Fatalf("repo_id %q generated_at %v", doc.Project.RepoID, doc.GeneratedAt)
	}
	want := map[string]contract.PageState{
		same: contract.StateUnchanged, edited: contract.StateModified, fresh: contract.StateNew,
		broken: contract.StateInvalid, noauthor: contract.StateInvalid, moved: contract.StateInvalid,
		gone: contract.StateDeletedLocal,
	}
	if len(doc.Pages) != len(want) {
		t.Fatalf("pages = %v", st11Paths(doc))
	}
	for path, state := range want {
		if got := st11Entry(t, doc, path).State; got != state {
			t.Errorf("%s: state %s, want %s", path, got, state)
		}
	}
	wantCounts := contract.StateCounts{Unchanged: 1, Modified: 1, New: 1, DeletedLocal: 1, Invalid: 3, Ahead: 2}
	if doc.Counts != wantCounts {
		t.Errorf("counts = %+v, want %+v", doc.Counts, wantCounts)
	}

	e := st11Entry(t, doc, same)
	syncedAt := contract.TimeOf(st11Now)
	if e.Type != "concept" || e.Slug != "same" || e.Title != "Same page" || e.Author != "testkit" ||
		!reflect.DeepEqual(e.Tags, []string{"retry", "api"}) {
		t.Errorf("same frontmatter fields = %+v", e)
	}
	if e.Updated == nil || !e.Updated.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("same updated = %v", e.Updated)
	}
	if e.Local == nil || *e.Local != (contract.LocalSide{File: same, Hash: syncstate.Hash(sameBytes)}) {
		t.Errorf("same local = %+v", e.Local)
	}
	if e.Base == nil || e.Base.SHA != st11SHA(1) || e.Base.Hash != syncstate.Hash(sameBytes) ||
		e.Base.Stale || e.Base.SyncedAt == nil || !e.Base.SyncedAt.Equal(syncedAt) {
		t.Errorf("same base = %+v", e.Base)
	}
	if e.Remote != nil || len(e.Notes) != 0 {
		t.Errorf("same remote %+v notes %v", e.Remote, e.Notes)
	}

	e = st11Entry(t, doc, edited)
	if e.Local == nil || e.Local.Hash != syncstate.Hash(editedBytes) || e.Base == nil || e.Base.Hash == e.Local.Hash {
		t.Errorf("edited local %+v base %+v", e.Local, e.Base)
	}
	e = st11Entry(t, doc, fresh)
	if e.Type != "entity" || e.Title != "fresh" || e.Tags == nil || len(e.Tags) != 0 || e.Base != nil {
		t.Errorf("fresh = %+v", e)
	}

	for path, stage := range map[string]contract.InvalidStage{
		broken: contract.InvalidParse, noauthor: contract.InvalidValidate, moved: contract.InvalidPathMismatch,
	} {
		e := st11Entry(t, doc, path)
		if e.Invalid == nil || e.Invalid.Stage != stage || e.Invalid.Message == "" {
			t.Errorf("%s invalid = %+v, want stage %s", path, e.Invalid, stage)
			continue
		}
		if e.Local == nil || e.Local.Hash != "" || e.Type != "" || e.Slug != "" || e.Title != "" {
			t.Errorf("%s: invalid entry carries frontmatter or hash: %+v", path, e)
		}
	}
	if got := st11Entry(t, doc, moved).Invalid.ExpectedPath; got != "wiki/concepts/elsewhere.md" {
		t.Errorf("moved expected_path = %q", got)
	}
	if got := st11Entry(t, doc, broken).Invalid.ExpectedPath; got != "" {
		t.Errorf("broken expected_path = %q, want none", got)
	}

	e = st11Entry(t, doc, gone)
	if e.Local != nil || e.Base == nil || e.Base.SHA != st11SHA(3) || e.Remote != nil || len(e.Notes) != 0 {
		t.Errorf("gone = %+v", e)
	}

	// Without any base, offline status must not create .sync at all (no
	// .gitignore, no lock).
	env2 := st11Env(t, nil)
	st11Write(t, env2, fresh, st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "fresh"}))
	st11Write(t, env2, broken, []byte("---\nschema: [\n---\n"))
	before = st11TreeSum(t, env2.HubDir)
	doc = st11Status(t, env2, StatusOptions{Offline: true})
	if st11Exists(t, syncstate.SyncDir(env2.HubDir)) {
		t.Fatal("offline status created .sync")
	}
	if st11TreeSum(t, env2.HubDir) != before {
		t.Fatal("offline status changed the .contexo tree")
	}
	if doc.Counts.New != 1 || doc.Counts.Invalid != 1 {
		t.Fatalf("counts = %+v", doc.Counts)
	}
}

func TestStatus_NotInitialized(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	for _, offline := range []bool{true, false} {
		fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
		env := NewEnv(root, "acme-kb", "https://kb.acme.dev", Author{}, fr)
		doc, err := Status(context.Background(), env, StatusOptions{Offline: offline})
		if doc != nil {
			t.Fatalf("offline=%v: document returned with the error: %+v", offline, doc)
		}
		ce := wp01ContractError(t, err)
		if ce.Code != contract.CodeNotInitialized || !reflect.DeepEqual(ce.Advice, []string{"ctx init"}) ||
			!strings.HasPrefix(ce.Message, "status: ") {
			t.Fatalf("offline=%v: error = %+v", offline, ce)
		}
		if n := len(fr.Requests()); n != 0 {
			t.Fatalf("offline=%v: %d requests before the hub check", offline, n)
		}
	}
}

// st11OrderRemote wraps a FakeRemote and records the order of every Remote
// call, ServerInfo included. info, when set, replaces ServerInfo's answer.
type st11OrderRemote struct {
	*syncopstest.FakeRemote
	calls []string
	info  func() (string, []contract.ServerCapability, bool)
}

func (r *st11OrderRemote) PushPages(repoID string, req *sync.PushRequest) (*sync.PushResponse, error) {
	r.calls = append(r.calls, "PushPages")
	return r.FakeRemote.PushPages(repoID, req)
}

func (r *st11OrderRemote) PullPages(repoID, since string) (*sync.PullResponse, error) {
	r.calls = append(r.calls, "PullPages")
	return r.FakeRemote.PullPages(repoID, since)
}

func (r *st11OrderRemote) ListPages(repoID string) ([]sync.PageMeta, error) {
	r.calls = append(r.calls, "ListPages")
	return r.FakeRemote.ListPages(repoID)
}

func (r *st11OrderRemote) ReadPage(repoID, path string) ([]byte, string, error) {
	r.calls = append(r.calls, "ReadPage")
	return r.FakeRemote.ReadPage(repoID, path)
}

func (r *st11OrderRemote) ReadPageAt(repoID, path, sha string) ([]byte, string, error) {
	r.calls = append(r.calls, "ReadPageAt")
	return r.FakeRemote.ReadPageAt(repoID, path, sha)
}

func (r *st11OrderRemote) ServerInfo() (string, []contract.ServerCapability, bool) {
	r.calls = append(r.calls, "ServerInfo")
	if r.info != nil {
		return r.info()
	}
	return r.FakeRemote.ServerInfo()
}

// st11OverrideEnsureBase replaces the statusEnsureBase seam for the test.
func st11OverrideEnsureBase(t testing.TB, fn func(context.Context, Env, EnsureBaseOptions) (*EnsureBaseResult, error)) {
	t.Helper()
	prev := statusEnsureBase
	statusEnsureBase = fn
	t.Cleanup(func() { statusEnsureBase = prev })
}

// st11Listing is the fake's listing by path (one extra, counted request).
func st11Listing(t testing.TB, fr *syncopstest.FakeRemote) map[string]sync.PageMeta {
	t.Helper()
	listing, err := fr.ListPages("acme-kb")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]sync.PageMeta{}
	for _, m := range listing {
		out[m.Path] = m
	}
	return out
}

// st11LiveSide is the live remote side status must build from m.
func st11LiveSide(m sync.PageMeta) *contract.RemoteSide {
	return &contract.RemoteSide{SHA: m.SHA, Author: m.Author, Email: m.Email, Time: contract.TimePtr(m.Time), Message: m.Message}
}

func TestStatus_Online_IncomingAndConflict(t *testing.T) {
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	const a, b = "wiki/concepts/a.md", "wiki/concepts/b.md"

	a1 := st11Page(testkit.PageSpec{Slug: "a", Body: "one"})
	sa1 := fr.Seed(a, a1)
	st11SeedBase(t, env, a, a1, sa1)
	fr.Seed(a, st11Page(testkit.PageSpec{Slug: "a", Body: "two (teammate)"}))
	st11Write(t, env, a, a1)

	b1 := st11Page(testkit.PageSpec{Slug: "b", Body: "one"})
	sb1 := fr.Seed(b, b1)
	st11SeedBase(t, env, b, b1, sb1)
	fr.Seed(b, st11Page(testkit.PageSpec{Slug: "b", Body: "two (teammate)"}))
	st11Write(t, env, b, st11Page(testkit.PageSpec{Slug: "b", Body: "two (mine)"}))

	doc := st11Status(t, env, StatusOptions{})
	live := st11Listing(t, fr)

	ea := st11Entry(t, doc, a)
	if ea.State != contract.StateIncoming || ea.Base == nil || ea.Base.SHA != sa1 {
		t.Fatalf("a = %+v", ea)
	}
	if want := st11LiveSide(live[a]); !reflect.DeepEqual(ea.Remote, want) {
		t.Fatalf("a remote = %+v, want %+v", ea.Remote, want)
	}
	if ea.Remote.Author != "Seed" || ea.Remote.Email != "seed@acme.dev" || ea.Remote.Message != "seed "+a {
		t.Fatalf("a remote = %+v", ea.Remote)
	}
	eb := st11Entry(t, doc, b)
	if eb.State != contract.StateConflict || eb.ConflictKind != contract.ConflictBothModified {
		t.Fatalf("b = %+v", eb)
	}
	if want := st11LiveSide(live[b]); !reflect.DeepEqual(eb.Remote, want) {
		t.Fatalf("b remote = %+v, want %+v", eb.Remote, want)
	}
	if doc.Counts.Incoming != 1 || doc.Counts.Conflict != 1 || doc.Counts.Behind != 1 || doc.Counts.Ahead != 0 {
		t.Fatalf("counts = %+v", doc.Counts)
	}
	if doc.Offline || doc.Remote.Source != contract.RemoteLive {
		t.Fatalf("offline %v remote %+v", doc.Offline, doc.Remote)
	}
}

func TestStatus_RemoteOnlyIsIncomingNew(t *testing.T) {
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	const p = "wiki/concepts/remote.md"
	fr.Seed(p, st11Page(testkit.PageSpec{Slug: "remote"}))

	doc := st11Status(t, env, StatusOptions{})
	e := st11Entry(t, doc, p)
	if e.State != contract.StateIncomingNew || e.Local != nil || e.Base != nil || e.Type != "" {
		t.Fatalf("remote-only = %+v", e)
	}
	if want := st11LiveSide(st11Listing(t, fr)[p]); !reflect.DeepEqual(e.Remote, want) {
		t.Fatalf("remote side = %+v, want %+v", e.Remote, want)
	}
	if doc.Counts.IncomingNew != 1 || doc.Counts.Behind != 1 {
		t.Fatalf("counts = %+v", doc.Counts)
	}
}

func TestStatus_NewLocalExistingOnServerIsBothAdded(t *testing.T) {
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	const p = "wiki/entities/gateway.md"
	sr := fr.Seed(p, st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "gateway", Body: "theirs"}))
	mine := st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "gateway", Body: "mine"})
	st11Write(t, env, p, mine)

	doc := st11Status(t, env, StatusOptions{})
	e := st11Entry(t, doc, p)
	if e.State != contract.StateConflict || e.ConflictKind != contract.ConflictBothAdded {
		t.Fatalf("entry = %+v", e)
	}
	if e.Base != nil || e.Local == nil || e.Local.Hash != syncstate.Hash(mine) || e.Remote == nil || e.Remote.SHA != sr {
		t.Fatalf("sides: local %+v base %+v remote %+v", e.Local, e.Base, e.Remote)
	}
}

func TestStatus_OneRequestOnline(t *testing.T) {
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	for _, slug := range []string{"a", "b", "c"} {
		rel := "wiki/entities/" + slug + ".md"
		data := st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: slug})
		st11SeedBase(t, env, rel, data, fr.Seed(rel, data))
		st11Write(t, env, rel, data)
	}

	doc := st11Status(t, env, StatusOptions{})
	if got := fr.Requests(); !reflect.DeepEqual(got, map[string]int{"ListPages": 1}) {
		t.Fatalf("requests = %v, want exactly one ListPages", got)
	}
	if doc.Counts.Unchanged != 3 || len(doc.Pages) != 3 {
		t.Fatalf("pages = %v", st11Paths(doc))
	}
	r := doc.Remote
	if r.Source != contract.RemoteLive || r.RepoExists == nil || !*r.RepoExists || r.PageCount != 3 || r.Error != nil {
		t.Fatalf("remote = %+v", r)
	}
	if r.FetchedAt == nil || !r.FetchedAt.Equal(doc.GeneratedAt) {
		t.Fatalf("fetched_at %v != generated_at %v", r.FetchedAt, doc.GeneratedAt)
	}
	if r.Head != fr.Head() {
		t.Fatalf("head = %q, want newest seed %q", r.Head, fr.Head())
	}
	if doc.Auth.State != contract.AuthAuthenticated {
		t.Fatalf("auth.state = %q", doc.Auth.State)
	}
	wantCaps := []contract.ServerCapability{contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA}
	if doc.Server.Version != "0.7.0-fake" || !doc.Server.CapabilitiesKnown || !reflect.DeepEqual(doc.Server.Capabilities, wantCaps) {
		t.Fatalf("server = %+v", doc.Server)
	}
	if len(doc.Warnings) != 0 {
		t.Fatalf("warnings = %+v", doc.Warnings)
	}
}

func TestStatus_ServerInfoFromListingOnly(t *testing.T) {
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	or := &st11OrderRemote{FakeRemote: fr}
	env := st11Env(t, or)
	const a = "wiki/entities/a.md"
	data := st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "a"})
	st11SeedBase(t, env, a, data, fr.Seed(a, data))
	st11Write(t, env, a, data)
	st11OverrideEnsureBase(t, func(_ context.Context, env Env, _ EnsureBaseOptions) (*EnsureBaseResult, error) {
		if _, _, err := env.Remote.ReadPage(env.RepoID, a); err != nil {
			t.Errorf("ReadPage: %v", err)
		}
		return &EnsureBaseResult{}, nil
	})

	doc := st11Status(t, env, StatusOptions{})
	if want := []string{"ListPages", "ServerInfo", "ReadPage"}; !reflect.DeepEqual(or.calls, want) {
		t.Fatalf("call order = %v, want %v (ServerInfo once, right after the listing)", or.calls, want)
	}
	if st11Entry(t, doc, a).State != contract.StateUnchanged {
		t.Fatalf("pages = %v", st11Paths(doc))
	}
}

func TestStatus_HeadAmbiguousOmitted(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		listing []sync.PageMeta
		want    string
	}{
		{"empty", []sync.PageMeta{}, ""},
		{"nil", nil, ""},
		{"unique newest", []sync.PageMeta{
			{Path: "a", SHA: "s1", Time: t0}, {Path: "b", SHA: "s3", Time: t0.Add(2 * time.Second)},
			{Path: "c", SHA: "s2", Time: t0.Add(time.Second)},
		}, "s3"},
		{"newest shared by one commit", []sync.PageMeta{
			{Path: "a", SHA: "s2", Time: t0.Add(time.Second)}, {Path: "b", SHA: "s2", Time: t0.Add(time.Second)},
			{Path: "c", SHA: "s1", Time: t0},
		}, "s2"},
		{"newest time, different shas", []sync.PageMeta{
			{Path: "a", SHA: "s1", Time: t0.Add(time.Second)}, {Path: "b", SHA: "s2", Time: t0.Add(time.Second)},
		}, ""},
		{"tie below a unique newest", []sync.PageMeta{
			{Path: "a", SHA: "s1", Time: t0}, {Path: "b", SHA: "s2", Time: t0},
			{Path: "c", SHA: "s3", Time: t0.Add(time.Second)},
		}, "s3"},
	} {
		if got := statusHead(tc.listing); got != tc.want {
			t.Errorf("%s: statusHead = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestStatus_CallsEnsureBaseWithListingAndReloads(t *testing.T) {
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	const a, b = "wiki/entities/a.md", "wiki/entities/b.md"
	aBytes := st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "a"})
	sa := fr.Seed(a, aBytes)
	st11Write(t, env, a, aBytes)
	st11SetPageSHA(t, env, a, st11SHA(7)) // stale: no base record for that sha
	bBytes := st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "b"})
	sb := fr.Seed(b, bBytes)
	st11SeedBase(t, env, b, bBytes, sb)
	st11Write(t, env, b, bBytes)

	// A repair that changes nothing leaves the stale base unverified.
	st11OverrideEnsureBase(t, func(context.Context, Env, EnsureBaseOptions) (*EnsureBaseResult, error) {
		return &EnsureBaseResult{}, nil
	})
	if got := st11Entry(t, st11Status(t, env, StatusOptions{}), a).State; got != contract.StateUnverified {
		t.Fatalf("a without repair = %s, want unverified", got)
	}

	var got EnsureBaseOptions
	calls := 0
	st11OverrideEnsureBase(t, func(_ context.Context, env Env, o EnsureBaseOptions) (*EnsureBaseResult, error) {
		got = o
		calls++
		st11SeedBase(t, env, a, aBytes, sa) // rewrites bases.json and page_shas
		return &EnsureBaseResult{Repaired: []string{a}, Unverified: []string{}, Warnings: []contract.Warning{}}, nil
	})
	doc := st11Status(t, env, StatusOptions{})
	if calls != 1 {
		t.Fatalf("statusEnsureBase called %d times, want 1", calls)
	}
	if got.Locked || got.Paths != nil || len(got.Listing) != 2 ||
		got.Listing[0].Path != a || got.Listing[0].SHA != sa || got.Listing[1].Path != b || got.Listing[1].SHA != sb {
		t.Fatalf("EnsureBase options = %+v, want the live listing [a, b], Locked false", got)
	}
	ea := st11Entry(t, doc, a)
	if ea.State != contract.StateUnchanged || ea.Base == nil || ea.Base.SHA != sa || ea.Base.Stale {
		t.Fatalf("a after repair = %+v base %+v, want unchanged on %s", ea, ea.Base, sa)
	}
	if w := st11Warning(doc, contract.WarnBaseRepairSkipped); w != nil {
		t.Fatalf("base_repair_skipped after a full repair: %+v", w)
	}
}

func TestStatus_StaleBaseIsUnverified(t *testing.T) {
	// An old server cannot read past versions and the listing sha differs
	// from page_shas, with no older base record: even WP-1.5's real
	// EnsureBase cannot repair this base.
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{OldServer: true})
	env := st11Env(t, fr)
	const a = "wiki/concepts/a.md"
	s1 := fr.Seed(a, st11Page(testkit.PageSpec{Slug: "a", Body: "v1"}))
	v2 := st11Page(testkit.PageSpec{Slug: "a", Body: "v2"})
	s2 := fr.Seed(a, v2)
	st11SetPageSHA(t, env, a, s1)
	st11Write(t, env, a, v2)

	doc := st11Status(t, env, StatusOptions{})
	e := st11Entry(t, doc, a)
	if e.State != contract.StateUnverified {
		t.Fatalf("a = %s, want unverified", e.State)
	}
	if e.Base == nil || *e.Base != (contract.BaseSide{SHA: s1, Stale: true}) {
		t.Fatalf("a base = %+v, want {sha: %s, stale: true}", e.Base, s1)
	}
	if e.Remote == nil || e.Remote.SHA != s2 {
		t.Fatalf("a remote = %+v", e.Remote)
	}
	w := st11Warning(doc, contract.WarnBaseRepairSkipped)
	if w == nil || !reflect.DeepEqual(w.Paths, []string{a}) || w.Message == "" {
		t.Fatalf("base_repair_skipped = %+v, want paths [%s]", w, a)
	}
	if st11Warning(doc, contract.WarnServerOutdated) == nil {
		t.Fatalf("no server_outdated on an old server: %+v", doc.Warnings)
	}
	if !doc.Server.CapabilitiesKnown || len(doc.Server.Capabilities) != 0 || doc.Server.Version != "" {
		t.Fatalf("server = %+v", doc.Server)
	}
	if doc.Counts.Unverified != 1 {
		t.Fatalf("counts = %+v", doc.Counts)
	}
}

// st11StaleScenario is a current server holding rel at one sha while
// page_shas names another, with no base record: unverified until repaired.
func st11StaleScenario(t testing.TB, rel, slug string) (*syncopstest.FakeRemote, Env) {
	t.Helper()
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	data := st11Page(testkit.PageSpec{Slug: slug})
	fr.Seed(rel, data)
	st11SetPageSHA(t, env, rel, st11SHA(9))
	st11Write(t, env, rel, data)
	return fr, env
}

func TestStatus_EnsureBaseErrorIsWarning(t *testing.T) {
	boom := func(context.Context, Env, EnsureBaseOptions) (*EnsureBaseResult, error) {
		return nil, errors.New("boom")
	}

	const z = "wiki/concepts/z.md"
	_, env := st11StaleScenario(t, z, "z")
	st11OverrideEnsureBase(t, boom)
	doc := st11Status(t, env, StatusOptions{})
	if st11Entry(t, doc, z).State != contract.StateUnverified {
		t.Fatalf("pages = %v", st11Paths(doc))
	}
	w := st11Warning(doc, contract.WarnBaseRepairSkipped)
	if w == nil || !reflect.DeepEqual(w.Paths, []string{z}) || !strings.Contains(w.Message, "boom") {
		t.Fatalf("base_repair_skipped = %+v, want paths [%s] and the error", w, z)
	}

	// The error alone, with every page verified, still warns once.
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env = st11Env(t, fr)
	const a = "wiki/concepts/a.md"
	data := st11Page(testkit.PageSpec{Slug: "a"})
	st11SeedBase(t, env, a, data, fr.Seed(a, data))
	st11Write(t, env, a, data)
	doc = st11Status(t, env, StatusOptions{})
	w = st11Warning(doc, contract.WarnBaseRepairSkipped)
	if w == nil || len(w.Paths) != 0 || !strings.Contains(w.Message, "boom") {
		t.Fatalf("base_repair_skipped = %+v, want paths [] and the error", w)
	}
	if len(doc.Warnings) != 1 {
		t.Fatalf("warnings = %+v", doc.Warnings)
	}
}

// E12: inside status the whole EnsureBase is capped at 20 s, so a slow
// backfill cannot hold the extension's status call past its 30 s timeout.
func TestStatus_EnsureBaseCappedAt20s(t *testing.T) {
	const a = "wiki/concepts/a.md"
	_, env := st11StaleScenario(t, a, "a")
	var deadline time.Time
	var hasDeadline bool
	st11OverrideEnsureBase(t, func(ctx context.Context, _ Env, _ EnsureBaseOptions) (*EnsureBaseResult, error) {
		deadline, hasDeadline = ctx.Deadline()
		return &EnsureBaseResult{Repaired: []string{}, Unverified: []string{}, Warnings: []contract.Warning{}}, nil
	})
	start := time.Now()
	st11Status(t, env, StatusOptions{})
	end := time.Now()
	if !hasDeadline {
		t.Fatal("EnsureBase ran without a deadline; status must cap it at 20 s (E12)")
	}
	if deadline.Before(start.Add(20*time.Second)) || deadline.After(end.Add(20*time.Second)) {
		t.Fatalf("EnsureBase deadline %v is not 20 s after the call (%v .. %v)", deadline, start, end)
	}

	// A tighter caller deadline wins.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	want, _ := ctx.Deadline()
	if _, err := Status(ctx, env, StatusOptions{}); err != nil {
		t.Fatal(err)
	}
	if !deadline.Equal(want) {
		t.Fatalf("EnsureBase deadline = %v, want the caller's %v", deadline, want)
	}
}

func TestStatus_ServerOutdatedWarning(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts syncopstest.FakeOptions
		want string
	}{
		{"legacy no-op sha", syncopstest.FakeOptions{LegacyNoopHeadSHA: true}, "write.path_sha"},
		{"old server", syncopstest.FakeOptions{OldServer: true}, "pages.at, push.expect_absent, write.path_sha"},
	} {
		env := st11Env(t, syncopstest.NewFakeRemote(tc.opts))
		doc := st11Status(t, env, StatusOptions{})
		w := st11Warning(doc, contract.WarnServerOutdated)
		want := "https://kb.acme.dev does not advertise " + tc.want + "; update the Contexo server"
		if w == nil || w.Message != want || w.Paths == nil || len(w.Paths) != 0 {
			t.Fatalf("%s: server_outdated = %+v, want message %q and paths []", tc.name, w, want)
		}
	}

	// Unknown capabilities never claim the server is outdated.
	or := &st11OrderRemote{
		FakeRemote: syncopstest.NewFakeRemote(syncopstest.FakeOptions{OldServer: true}),
		info: func() (string, []contract.ServerCapability, bool) {
			return "", []contract.ServerCapability{}, false
		},
	}
	doc := st11Status(t, st11Env(t, or), StatusOptions{})
	if w := st11Warning(doc, contract.WarnServerOutdated); w != nil || doc.Server.CapabilitiesKnown {
		t.Fatalf("unknown capabilities: server %+v, server_outdated %+v", doc.Server, w)
	}
}

func TestStatus_WarningsMergedByCode(t *testing.T) {
	const x, y, z = "wiki/concepts/x.md", "wiki/concepts/y.md", "wiki/concepts/z.md"
	_, env := st11StaleScenario(t, z, "z")
	st11OverrideEnsureBase(t, func(context.Context, Env, EnsureBaseOptions) (*EnsureBaseResult, error) {
		return &EnsureBaseResult{Warnings: []contract.Warning{
			{Code: contract.WarnBaseRepairSkipped, Message: "y skipped", Paths: []string{y}},
			{Code: contract.WarnBaseRepairSkipped, Message: "x skipped", Paths: []string{x}},
		}}, nil
	})
	doc := st11Status(t, env, StatusOptions{})
	want := []contract.Warning{{Code: contract.WarnBaseRepairSkipped, Message: "y skipped", Paths: []string{x, y, z}}}
	if !reflect.DeepEqual(doc.Warnings, want) {
		t.Fatalf("warnings = %+v, want %+v", doc.Warnings, want)
	}
}

// st11ListError is the error the real client's ListPages returns for an
// HTTP answer of status with body.
func st11ListError(status int, body string) *sync.HTTPError {
	return sync.NewHTTPError(status, []byte(body), fmt.Sprintf("sync: list pages (%d): %s", status, body), nil, false)
}

// st11ForbidEnsureBase fails the test when status calls EnsureBase.
func st11ForbidEnsureBase(t testing.TB) {
	t.Helper()
	st11OverrideEnsureBase(t, func(context.Context, Env, EnsureBaseOptions) (*EnsureBaseResult, error) {
		t.Error("EnsureBase called without a live listing")
		return nil, nil
	})
}

// st11Sidecar records rec (and content) in .sync/conflicts.json.
func st11Sidecar(t testing.TB, env Env, rec syncstate.ConflictRecord, content []byte) {
	t.Helper()
	c, err := syncstate.OpenConflicts(env.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put(rec, content); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
}

// st11Fail* name st11FailureProject's pages: an edited page, an unchanged
// page whose sidecar names a newer server version, and a new page. Without
// a listing the E9 offline rule makes them modified, incoming (remote {sha}
// from the sidecar) and new.
const (
	st11FailEdited  = "wiki/entities/edited.md"
	st11FailSidecar = "wiki/entities/sidecar.md"
	st11FailFresh   = "wiki/entities/fresh.md"
)

var st11FailServerSHA = st11SHA(40)

func st11FailureProject(t testing.TB, env Env, fr *syncopstest.FakeRemote) {
	t.Helper()
	edited := st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "edited", Body: "v1"})
	st11SeedBase(t, env, st11FailEdited, edited, fr.Seed(st11FailEdited, edited))
	st11Write(t, env, st11FailEdited, st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "edited", Body: "v2"}))
	side := st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "sidecar"})
	sb := fr.Seed(st11FailSidecar, side)
	st11SeedBase(t, env, st11FailSidecar, side, sb)
	st11Write(t, env, st11FailSidecar, side)
	st11Sidecar(t, env, syncstate.ConflictRecord{
		Path: st11FailSidecar, ServerSHA: st11FailServerSHA, BaseSHA: sb,
		Kind: syncstate.KindBothModified, Source: syncstate.SourcePull,
	}, []byte("theirs"))
	st11Write(t, env, st11FailFresh, st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "fresh"}))
}

// st11CheckFailurePages asserts the E9 offline rule over st11FailureProject.
func st11CheckFailurePages(t testing.TB, doc *contract.StatusDoc) {
	t.Helper()
	if len(doc.Pages) != 3 {
		t.Fatalf("pages = %v", st11Paths(doc))
	}
	if e := st11Entry(t, doc, st11FailEdited); e.State != contract.StateModified || e.Remote != nil {
		t.Errorf("edited = %+v remote %+v, want modified without a remote side", e, e.Remote)
	}
	e := st11Entry(t, doc, st11FailSidecar)
	if e.State != contract.StateIncoming || !reflect.DeepEqual(e.Remote, &contract.RemoteSide{SHA: st11FailServerSHA}) {
		t.Errorf("sidecar = %+v remote %+v, want incoming with remote {sha} from the sidecar", e, e.Remote)
	}
	if e := st11Entry(t, doc, st11FailFresh); e.State != contract.StateNew || e.Remote != nil {
		t.Errorf("fresh = %+v", e)
	}
}

// st11FullCaps is a current server's capability list.
var st11FullCaps = []contract.ServerCapability{contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA}

func TestStatus_RemoteErrorIsReportedNotFatal(t *testing.T) {
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	st11FailureProject(t, env, fr)
	st11ForbidEnsureBase(t)
	fr.FailNext("ListPages", &sync.NetworkError{Op: "list pages", Err: errors.New("dial tcp: no such host")})

	doc, err := Status(context.Background(), env, StatusOptions{})
	if err != nil || doc == nil {
		t.Fatalf("Status = %v, %v; a remote failure must not fail status", doc, err)
	}
	st11CheckDoc(t, doc)
	want := &contract.ErrorBody{Code: contract.CodeNetwork, Message: "sync: list pages: dial tcp: no such host",
		Advice: []string{}, Retryable: true}
	if !reflect.DeepEqual(doc.Remote.Error, want) {
		t.Fatalf("remote.error = %+v, want %+v", doc.Remote.Error, want)
	}
	if doc.Offline || doc.Remote.Source != contract.RemoteNone || doc.Auth.State != contract.AuthUnchecked {
		t.Fatalf("offline %v remote %+v auth %q", doc.Offline, doc.Remote, doc.Auth.State)
	}
	if doc.Server.CapabilitiesKnown || len(doc.Server.Capabilities) != 0 || doc.Server.Version != "" {
		t.Fatalf("server = %+v", doc.Server)
	}
	st11CheckFailurePages(t, doc)
	if got := fr.Requests(); !reflect.DeepEqual(got, map[string]int{"ListPages": 1}) {
		t.Fatalf("requests = %v", got)
	}
	if len(doc.Warnings) != 0 {
		t.Fatalf("warnings = %+v", doc.Warnings)
	}
}

func TestStatus_AuthRejected(t *testing.T) {
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	st11FailureProject(t, env, fr)
	st11ForbidEnsureBase(t)
	const body = `{"error":"invalid or expired token"}`
	fr.FailNext("ListPages", st11ListError(401, body))

	doc := st11Status(t, env, StatusOptions{})
	want := &contract.ErrorBody{Code: contract.CodeAuthRejected, Message: "sync: list pages (401): " + body,
		Advice: []string{"ctx auth login"}, HTTPStatus: 401}
	if !reflect.DeepEqual(doc.Remote.Error, want) {
		t.Fatalf("remote.error = %+v, want %+v", doc.Remote.Error, want)
	}
	if doc.Auth.State != contract.AuthRejected || doc.Remote.Source != contract.RemoteNone {
		t.Fatalf("auth %q remote %+v", doc.Auth.State, doc.Remote)
	}
	// The 401 still carried the server's headers.
	if !doc.Server.CapabilitiesKnown || !reflect.DeepEqual(doc.Server.Capabilities, st11FullCaps) ||
		doc.Server.Version != "0.7.0-fake" {
		t.Fatalf("server = %+v", doc.Server)
	}
	st11CheckFailurePages(t, doc)
}

func TestStatus_RepoMissing(t *testing.T) {
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	const kept, stale, fresh = "wiki/entities/kept.md", "wiki/entities/stale.md", "wiki/entities/fresh.md"
	keptBytes := st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "kept"})
	st11SeedBase(t, env, kept, keptBytes, st11SHA(1))
	st11Write(t, env, kept, keptBytes)
	st11SetPageSHA(t, env, stale, st11SHA(2)) // no base record: stale
	st11Write(t, env, stale, st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "stale"}))
	st11Write(t, env, fresh, st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "fresh"}))
	st11ForbidEnsureBase(t)
	fr.FailNext("ListPages", st11ListError(404, `{"code":"repo_not_found","error":"repo not found"}`))

	doc := st11Status(t, env, StatusOptions{})
	r := doc.Remote
	if r.Source != contract.RemoteLive || r.RepoExists == nil || *r.RepoExists || r.PageCount != 0 ||
		r.Head != "" || r.Error != nil || r.FetchedAt == nil || !r.FetchedAt.Equal(doc.GeneratedAt) {
		t.Fatalf("remote = %+v", r)
	}
	if doc.Auth.State != contract.AuthAuthenticated {
		t.Fatalf("auth.state = %q", doc.Auth.State)
	}
	missing := []contract.NoteCode{contract.NoteMissingOnServer}
	for path, notes := range map[string][]contract.NoteCode{kept: missing, stale: missing, fresh: {}} {
		e := st11Entry(t, doc, path)
		if e.State != contract.StateNew || !reflect.DeepEqual(e.Notes, notes) || e.Remote != nil {
			t.Errorf("%s = %+v, want new with notes %v", path, e, notes)
		}
	}
	if doc.Counts.New != 3 || len(doc.Warnings) != 0 {
		t.Fatalf("counts %+v warnings %+v", doc.Counts, doc.Warnings)
	}
	if !doc.Server.CapabilitiesKnown || !reflect.DeepEqual(doc.Server.Capabilities, st11FullCaps) {
		t.Fatalf("server = %+v", doc.Server)
	}
}

func TestStatus_ServerErrorCapsUnknown(t *testing.T) {
	// An old server's 502 would report known capabilities and none of them:
	// a proxy error page must not claim the server is outdated (R3).
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{OldServer: true})
	env := st11Env(t, fr)
	st11FailureProject(t, env, fr)
	st11ForbidEnsureBase(t)
	fr.FailNext("ListPages", st11ListError(502, "<html>502 Bad Gateway</html>"))

	doc := st11Status(t, env, StatusOptions{})
	e := doc.Remote.Error
	if e == nil || e.Code != contract.CodeServerError || !e.Retryable || e.HTTPStatus != 502 {
		t.Fatalf("remote.error = %+v", e)
	}
	if doc.Server.CapabilitiesKnown || len(doc.Server.Capabilities) != 0 || doc.Server.Version != "" {
		t.Fatalf("server = %+v, want capabilities unknown", doc.Server)
	}
	if w := st11Warning(doc, contract.WarnServerOutdated); w != nil {
		t.Fatalf("server_outdated during an outage: %+v", w)
	}
	if doc.Auth.State != contract.AuthUnchecked || doc.Remote.Source != contract.RemoteNone {
		t.Fatalf("auth %q remote %+v", doc.Auth.State, doc.Remote)
	}
	st11CheckFailurePages(t, doc)
}

func TestStatus_ForbiddenIsRemoteError(t *testing.T) {
	// R5, with the other listing failures that keep the answer's headers.
	for _, tc := range []struct {
		status int
		body   string
		code   contract.ErrorCode
	}{
		{403, `{"error":"forbidden"}`, contract.CodeForbidden},
		{429, `{"error":"rate limited"}`, contract.CodeRateLimited},
		{404, "404 page not found", contract.CodeServerUnsupported},
		{402, `{"code":"repo_limit","error":"quota","limit":3}`, contract.CodeQuotaExceeded},
		{400, `{"error":"bad request"}`, contract.CodeUsage},
		{418, `{"error":"teapot"}`, contract.CodeInternal},
	} {
		fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
		env := st11Env(t, fr)
		st11FailureProject(t, env, fr)
		st11ForbidEnsureBase(t)
		fr.FailNext("ListPages", st11ListError(tc.status, tc.body))

		doc := st11Status(t, env, StatusOptions{})
		e := doc.Remote.Error
		if e == nil || e.Code != tc.code || e.HTTPStatus != tc.status || e.Advice == nil ||
			e.Message != fmt.Sprintf("sync: list pages (%d): %s", tc.status, tc.body) ||
			e.Retryable != contract.Retryable(tc.code) {
			t.Fatalf("%d: remote.error = %+v, want code %s", tc.status, e, tc.code)
		}
		if doc.Auth.State != contract.AuthUnchecked || doc.Remote.Source != contract.RemoteNone {
			t.Fatalf("%d: auth %q remote %+v", tc.status, doc.Auth.State, doc.Remote)
		}
		if !doc.Server.CapabilitiesKnown || !reflect.DeepEqual(doc.Server.Capabilities, st11FullCaps) {
			t.Fatalf("%d: server = %+v, want the answer's capabilities", tc.status, doc.Server)
		}
		st11CheckFailurePages(t, doc)
	}
}

func TestStatus_OfflineSidecarKeepsConflict(t *testing.T) {
	env := st11Env(t, nil)
	const a = "wiki/concepts/a.md"
	sb, s2 := st11SHA(1), st11SHA(2)
	st11SeedBase(t, env, a, st11Page(testkit.PageSpec{Slug: "a", Body: "base"}), sb)
	st11Write(t, env, a, st11Page(testkit.PageSpec{Slug: "a", Body: "mine"}))
	st11Sidecar(t, env, syncstate.ConflictRecord{
		Path: a, ServerSHA: s2, BaseSHA: sb, Kind: syncstate.KindBothModified, Source: syncstate.SourcePush,
	}, st11Page(testkit.PageSpec{Slug: "a", Body: "theirs"}))

	before := st11TreeSum(t, env.HubDir)
	doc := st11Status(t, env, StatusOptions{Offline: true})
	if st11TreeSum(t, env.HubDir) != before {
		t.Fatal("offline status changed the .contexo tree")
	}
	e := st11Entry(t, doc, a)
	if e.State != contract.StateConflict || e.ConflictKind != contract.ConflictBothModified {
		t.Fatalf("a = %+v, want conflict both_modified", e)
	}
	if !reflect.DeepEqual(e.Remote, &contract.RemoteSide{SHA: s2}) || e.Base == nil || e.Base.SHA != sb {
		t.Fatalf("a remote %+v base %+v, want remote {sha: %s} only", e.Remote, e.Base, s2)
	}
	if doc.Remote.Source != contract.RemoteNone || doc.Counts.Conflict != 1 {
		t.Fatalf("remote %+v counts %+v", doc.Remote, doc.Counts)
	}
}

func TestStatus_SidecarOverlayOnline(t *testing.T) {
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	const p = "wiki/entities/gateway.md"
	base := st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "gateway", Body: "base"})
	sb := fr.Seed(p, base)
	st11SeedBase(t, env, p, base, sb)
	st11Write(t, env, p, st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "gateway", Body: "mine"}))
	st11Sidecar(t, env, syncstate.ConflictRecord{
		Path: p, ServerSHA: st11SHA(9), Kind: syncstate.KindBothAdded, Source: syncstate.SourcePush,
	}, nil)

	doc := st11Status(t, env, StatusOptions{})
	e := st11Entry(t, doc, p)
	if e.State != contract.StateConflict || e.ConflictKind != contract.ConflictBothAdded {
		t.Fatalf("entry = %+v, want conflict both_added (E9 online overlay)", e)
	}
	// Online, R comes only from the listing: the live side, not the sidecar's.
	if want := st11LiveSide(st11Listing(t, fr)[p]); !reflect.DeepEqual(e.Remote, want) || e.Remote.SHA != sb {
		t.Fatalf("remote = %+v, want %+v", e.Remote, want)
	}
}

// st11Buffer writes a capture buffer for session with data and mtime.
func st11Buffer(t testing.TB, env Env, session, data string, mtime time.Time) {
	t.Helper()
	rel := "raw/sessions/_pending/" + session + ".jsonl"
	st11Write(t, env, rel, []byte(data))
	if err := os.Chtimes(filepath.Join(env.HubDir, filepath.FromSlash(rel)), mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// st11OverrideCheckGuard replaces the statusCheckGuard seam for the test.
func st11OverrideCheckGuard(t testing.TB, fn func(Env, []string) (*GuardResult, error)) {
	t.Helper()
	prev := statusCheckGuard
	statusCheckGuard = fn
	t.Cleanup(func() { statusCheckGuard = prev })
}

func TestStatus_CapturesCounted(t *testing.T) {
	t.Setenv("CONTEXO_DISTILL_DISABLE", "")
	env := st11Env(t, nil)
	// The batch holds only an entity page: neither the stub nor WP-1.2's
	// real guard (concept/analysis pages only) requires a distill.
	const m = "wiki/entities/gateway.md"
	st11SeedBase(t, env, m, st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "gateway", Body: "v1"}), st11SHA(1))
	st11Write(t, env, m, st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "gateway", Body: "v2"}))
	newAt, oldAt := st11Now.Add(-10*time.Minute), st11Now.Add(-7*time.Hour)
	st11Buffer(t, env, "new-sess", "{\"turn\":1}\n\n{\"turn\":2}\n   \n{\"turn\":3}\n\n", newAt)
	st11Buffer(t, env, "old-sess", "{\"turn\":1}\n{\"turn\":2}", oldAt)
	st11Write(t, env, "raw/sessions/_pending/_archive/done.jsonl", []byte("{\"turn\":1}\n"))
	st11Write(t, env, "raw/sessions/_pending/notes.txt", []byte("not a buffer\n"))

	doc := st11Status(t, env, StatusOptions{Offline: true})
	want := contract.CaptureInfo{Pending: 2, Fresh: 1, BlocksPush: false, Sessions: []contract.CaptureSession{
		{SessionID: "new-sess", Turns: 3, UpdatedAt: contract.TimeOf(newAt), Fresh: true},
		{SessionID: "old-sess", Turns: 2, UpdatedAt: contract.TimeOf(oldAt), Fresh: false},
	}}
	if !reflect.DeepEqual(doc.Captures, want) {
		t.Fatalf("captures = %+v, want %+v", doc.Captures, want)
	}
	if doc.Counts.Modified != 1 {
		t.Fatalf("pages = %v", st11Paths(doc))
	}

	// A fresh buffer without a turn is pending, not fresh; newest first.
	emptyAt := st11Now.Add(-time.Minute)
	st11Buffer(t, env, "empty-sess", "\n \n", emptyAt)
	doc = st11Status(t, env, StatusOptions{Offline: true})
	if c := doc.Captures; c.Pending != 3 || c.Fresh != 1 || len(c.Sessions) != 3 ||
		c.Sessions[0] != (contract.CaptureSession{SessionID: "empty-sess", Turns: 0, UpdatedAt: contract.TimeOf(emptyAt)}) {
		t.Fatalf("captures = %+v", c)
	}
}

func TestStatus_CapturesUnreadableDegrade(t *testing.T) {
	// R8: a pending directory that cannot be listed degrades captures to
	// nothing; status itself still succeeds.
	env := st11Env(t, nil)
	st11Write(t, env, "raw/sessions/_pending", []byte("a file where the directory belongs"))
	doc := st11Status(t, env, StatusOptions{Offline: true})
	want := contract.CaptureInfo{Sessions: []contract.CaptureSession{}}
	if !reflect.DeepEqual(doc.Captures, want) {
		t.Fatalf("captures = %+v, want %+v", doc.Captures, want)
	}
}

func TestStatus_BlocksPushHelper(t *testing.T) {
	t.Setenv("CONTEXO_DISTILL_DISABLE", "")
	if !statusBlocksPush(&GuardResult{Required: true}) {
		t.Fatal("Required guard does not block")
	}
	if statusBlocksPush(nil) || statusBlocksPush(&GuardResult{Required: false}) {
		t.Fatal("nil or non-required guard blocks")
	}
	t.Setenv("CONTEXO_DISTILL_DISABLE", "1")
	if statusBlocksPush(&GuardResult{Required: true}) {
		t.Fatal("CONTEXO_DISTILL_DISABLE=1 does not bypass the guard")
	}
	t.Setenv("CONTEXO_DISTILL_DISABLE", "")

	env := st11Env(t, nil)
	const (
		mod   = "wiki/concepts/mod.md"
		fresh = "wiki/analyses/fresh.md"
		same  = "wiki/concepts/same.md"
		bad   = "wiki/concepts/bad.md"
		added = "wiki/entities/added.md"
	)
	st11SeedBase(t, env, mod, st11Page(testkit.PageSpec{Slug: "mod", Body: "v1"}), st11SHA(1))
	st11Write(t, env, mod, st11Page(testkit.PageSpec{Slug: "mod", Body: "v2"}))
	st11Write(t, env, fresh, st11Page(testkit.PageSpec{Type: schema.TypeAnalysis, Slug: "fresh"}))
	sameBytes := st11Page(testkit.PageSpec{Slug: "same"})
	st11SeedBase(t, env, same, sameBytes, st11SHA(2))
	st11Write(t, env, same, sameBytes)
	st11Write(t, env, bad, []byte("---\nschema: [\n---\n"))
	st11Write(t, env, added, st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "added"}))

	var batches [][]string
	st11OverrideCheckGuard(t, func(_ Env, batch []string) (*GuardResult, error) {
		batches = append(batches, batch)
		return &GuardResult{Required: true}, nil
	})
	doc := st11Status(t, env, StatusOptions{Offline: true})
	if !doc.Captures.BlocksPush {
		t.Fatal("blocks_push false with a required guard")
	}
	if want := [][]string{{fresh, mod, added}}; !reflect.DeepEqual(batches, want) {
		t.Fatalf("guard batches = %v, want %v (modified ∪ new, sorted)", batches, want)
	}

	t.Setenv("CONTEXO_DISTILL_DISABLE", "1")
	if st11Status(t, env, StatusOptions{Offline: true}).Captures.BlocksPush {
		t.Fatal("blocks_push true with CONTEXO_DISTILL_DISABLE=1")
	}
	t.Setenv("CONTEXO_DISTILL_DISABLE", "")

	st11OverrideCheckGuard(t, func(Env, []string) (*GuardResult, error) {
		return &GuardResult{Required: true}, errors.New("guard: unreadable buffer")
	})
	if st11Status(t, env, StatusOptions{Offline: true}).Captures.BlocksPush {
		t.Fatal("blocks_push true although the guard failed")
	}
}

func TestStatus_CaseCollision(t *testing.T) {
	// Portable on case-insensitive file systems: one local file and a
	// server path that differ only by case.
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	const upper, lower, other = "wiki/concepts/Retry.md", "wiki/concepts/retry.md", "wiki/concepts/other.md"
	fr.Seed(lower, st11Page(testkit.PageSpec{Slug: "retry"}))
	st11Write(t, env, upper, st11Page(testkit.PageSpec{Slug: "retry"}))
	st11Write(t, env, other, st11Page(testkit.PageSpec{Type: schema.TypeConcept, Slug: "other"}))

	doc := st11Status(t, env, StatusOptions{})
	for _, p := range []string{upper, lower} {
		if e := st11Entry(t, doc, p); !slices.Contains(e.Notes, contract.NoteCaseCollision) {
			t.Errorf("%s (%s) notes = %v, want case_collision", p, e.State, e.Notes)
		}
	}
	if e := st11Entry(t, doc, other); len(e.Notes) != 0 {
		t.Errorf("other notes = %v", e.Notes)
	}
	w := st11Warning(doc, contract.WarnCaseCollision)
	if w == nil || !reflect.DeepEqual(w.Paths, []string{upper, lower}) || w.Message == "" {
		t.Fatalf("case_collision = %+v, want paths [%s %s]", w, upper, lower)
	}

	// strings.EqualFold classes: simple folding, Kelvin sign included,
	// never multi-rune folds.
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"Retry.md", "rETRY.md", true},
		{"k", "\u212a", true},
		{"K", "\u212a", true},
		{"\u00df", "ss", false},
		{"a.md", "b.md", false},
	} {
		if got := statusFoldKey(tc.a) == statusFoldKey(tc.b); got != tc.same || got != strings.EqualFold(tc.a, tc.b) {
			t.Errorf("statusFoldKey(%q) == statusFoldKey(%q): %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}

func TestStatus_CorruptStateIsWarning(t *testing.T) {
	const a, b = "wiki/concepts/a.md", "wiki/concepts/b.md"
	for _, tc := range []struct {
		name    string
		corrupt []string
	}{
		{"state", []string{"state.json"}},
		{"state and bases", []string{"state.json", "bases.json"}},
	} {
		env := st11Env(t, nil)
		aBytes := st11Page(testkit.PageSpec{Slug: "a"})
		st11SeedBase(t, env, a, aBytes, st11SHA(1))
		st11Write(t, env, a, aBytes)
		st11Write(t, env, b, st11Page(testkit.PageSpec{Slug: "b"}))
		for _, name := range tc.corrupt {
			st11Write(t, env, ".sync/"+name, []byte("{not json"))
		}

		doc := st11Status(t, env, StatusOptions{Offline: true})
		w := st11Warning(doc, contract.WarnStateUnreadable)
		if w == nil || w.Paths == nil || len(w.Paths) != 0 {
			t.Fatalf("%s: state_unreadable = %+v, want paths []", tc.name, w)
		}
		for _, name := range tc.corrupt {
			if !strings.Contains(w.Message, name) {
				t.Errorf("%s: message %q does not name %s", tc.name, w.Message, name)
			}
		}
		// page_shas is empty: no page has a base.
		for _, p := range []string{a, b} {
			if e := st11Entry(t, doc, p); e.State != contract.StateNew || e.Base != nil {
				t.Errorf("%s: %s = %+v base %+v, want new without a base", tc.name, p, e, e.Base)
			}
		}
	}

	// bases.json alone: the recorded sha has no base, so the page is
	// unverified (offline: no base_repair_skipped).
	env := st11Env(t, nil)
	aBytes := st11Page(testkit.PageSpec{Slug: "a"})
	st11SeedBase(t, env, a, aBytes, st11SHA(1))
	st11Write(t, env, a, aBytes)
	st11Write(t, env, ".sync/bases.json", []byte("{not json"))
	doc := st11Status(t, env, StatusOptions{Offline: true})
	w := st11Warning(doc, contract.WarnStateUnreadable)
	if w == nil || !strings.Contains(w.Message, "bases.json") || strings.Contains(w.Message, "state.json") {
		t.Fatalf("state_unreadable = %+v, want it to name bases.json only", w)
	}
	if e := st11Entry(t, doc, a); e.State != contract.StateUnverified || e.Base == nil || !e.Base.Stale {
		t.Fatalf("a = %+v base %+v", e, e.Base)
	}
}

// st11TimeRE is a contract time on the wire (E16).
var st11TimeRE = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)

func TestStatus_TimesWholeSecondsUTC(t *testing.T) {
	fr := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := st11Env(t, fr)
	now := time.Date(2026, 11, 3, 16, 14, 7, 987654321, time.FixedZone("+0700", 7*60*60))
	env.Now = func() time.Time { return now }
	const a = "wiki/entities/a.md"
	data := st11Page(testkit.PageSpec{Type: schema.TypeEntity, Slug: "a"})
	st11SeedBase(t, env, a, data, fr.Seed(a, data))
	st11Write(t, env, a, data)
	st11Buffer(t, env, "sess", "{\"turn\":1}\n", now.Add(-time.Minute))

	doc := st11Status(t, env, StatusOptions{})
	var out bytes.Buffer
	if err := contract.WriteDoc(&out, doc); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		GeneratedAt string `json:"generated_at"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GeneratedAt != "2026-11-03T09:14:07Z" {
		t.Fatalf("generated_at = %q", decoded.GeneratedAt)
	}
	// generated_at, fetched_at, updated, base.synced_at, remote.time and
	// the session's updated_at.
	times := regexp.MustCompile(`"(\d{4}-\d\d-\d\dT[^"]*)"`).FindAllStringSubmatch(out.String(), -1)
	if len(times) < 6 {
		t.Fatalf("only %d time strings in %s", len(times), out.String())
	}
	for _, m := range times {
		if !st11TimeRE.MatchString(m[1]) {
			t.Errorf("time %q is not YYYY-MM-DDTHH:MM:SSZ", m[1])
		}
	}
}

func BenchmarkStatus_Offline2000(b *testing.B) {
	env := st11Env(b, nil)
	bases, err := syncstate.OpenBases(env.HubDir)
	if err != nil {
		b.Fatal(err)
	}
	st, _, err := sync.LoadStateOrEmpty(env.HubDir)
	if err != nil {
		b.Fatal(err)
	}
	for i := range 2000 {
		slug := fmt.Sprintf("page-%04d", i)
		rel := "wiki/concepts/" + slug + ".md"
		data := st11Page(testkit.PageSpec{Slug: slug, Body: strings.Repeat("Some knowledge. ", 40)})
		st11Write(b, env, rel, data)
		if err := bases.Put(rel, data, st11SHA(i), st11Now); err != nil {
			b.Fatal(err)
		}
		st.PageSHAs[rel] = st11SHA(i)
	}
	if err := bases.Save(); err != nil {
		b.Fatal(err)
	}
	if err := sync.SaveState(env.HubDir, st); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for range b.N {
		doc, err := Status(context.Background(), env, StatusOptions{Offline: true})
		if err != nil || doc.Counts.Unchanged != 2000 {
			b.Fatalf("Status: %v, counts %+v", err, doc.Counts)
		}
	}
}

// st11IdentityDoc is an engine document as FillStatusIdentity receives it:
// an offline status of a project with a last pull and one other warning.
func st11IdentityDoc(t testing.TB) *contract.StatusDoc {
	t.Helper()
	env := st11Env(t, nil)
	st, _, err := sync.LoadStateOrEmpty(env.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	st.LastPullSHA = st11SHA(1)
	if err := sync.SaveState(env.HubDir, st); err != nil {
		t.Fatal(err)
	}
	doc := st11Status(t, env, StatusOptions{Offline: true})
	doc.Warnings = append(doc.Warnings, contract.Warning{Code: contract.WarnCaseCollision, Message: "x", Paths: []string{}})
	return doc
}

// st11Remote is a fully usable hosted remote with global PAT credentials.
func st11Remote() *config.Remote {
	return &config.Remote{
		ServerURL:           "https://api.contexo.live",
		URLSource:           "credentials",
		ConfiguredServerURL: "https://api.contexo.live",
		RepoID:              "acme-kb",
		DashboardURL:        "https://app.contexo.live",
		Token:               "ctxp_x",
		Creds:               &config.Credentials{Token: "ctxp_x", UserName: "Ana", UserEmail: "ana@acme.dev"},
		CredsSource:         "global",
	}
}

// st11Unusable is the error cli.openRemote returns for r: RequireAuth's,
// then RequireRepo's, wrapped and suggested the way openRemote does.
func st11Unusable(r *config.Remote) error {
	err := r.RequireAuth()
	if err == nil {
		err = r.RequireRepo()
	}
	if err == nil {
		return nil
	}
	var re *config.RemoteError
	if !errors.As(err, &re) {
		return err
	}
	return ui.Suggest(fmt.Errorf("status: %w", err), re.ErrorAdvice()...)
}

func TestFillStatusIdentity_Mapping(t *testing.T) {
	doc := st11IdentityDoc(t)
	doc.Auth.State = contract.AuthAuthenticated // as an online engine sets it
	FillStatusIdentity(doc, st11Remote(), nil)

	if doc.Server.URL != "https://api.contexo.live" || doc.Server.URLSource != contract.URLFromCredentials {
		t.Fatalf("server = %+v", doc.Server)
	}
	wantProject := contract.ProjectInfo{
		RepoID:                   "acme-kb",
		ConfigServerURL:          "https://api.contexo.live",
		DashboardURL:             "https://app.contexo.live",
		DashboardPageURLTemplate: "https://app.contexo.live/repos/acme-kb/page/{path}",
		LastPullSHA:              st11SHA(1),
	}
	if doc.Project != wantProject {
		t.Fatalf("project = %+v, want %+v", doc.Project, wantProject)
	}
	wantAuth := contract.AuthInfo{State: contract.AuthAuthenticated, CredentialsSource: "global", TokenKind: "pat",
		UserName: "Ana", UserEmail: "ana@acme.dev"}
	if doc.Auth != wantAuth {
		t.Fatalf("auth = %+v, want %+v", doc.Auth, wantAuth)
	}
	if doc.Remote.Error != nil || len(doc.Warnings) != 1 {
		t.Fatalf("remote.error %+v warnings %+v", doc.Remote.Error, doc.Warnings)
	}

	// A config server_url naming another server: the warning comes first.
	doc = st11IdentityDoc(t)
	r := st11Remote()
	r.ConfiguredServerURL, r.Mismatch = "https://contexo.acme.dev", true
	FillStatusIdentity(doc, r, nil)
	want := contract.Warning{
		Code: contract.WarnServerURLMismatch,
		Message: "config server_url https://contexo.acme.dev differs from https://api.contexo.live, " +
			"the server your credentials belong to; using https://api.contexo.live",
		Paths: []string{},
	}
	if len(doc.Warnings) != 2 || !reflect.DeepEqual(doc.Warnings[0], want) || doc.Warnings[1].Code != contract.WarnCaseCollision {
		t.Fatalf("warnings = %+v, want %+v first", doc.Warnings, want)
	}
	if doc.Project.ConfigServerURL != "https://contexo.acme.dev" || doc.Server.URL != "https://api.contexo.live" {
		t.Fatalf("project %+v server %+v", doc.Project, doc.Server)
	}

	// R12: the repo id is escaped as one path segment.
	doc = st11IdentityDoc(t)
	r = st11Remote()
	r.RepoID = "acme kb/x"
	FillStatusIdentity(doc, r, nil)
	if got := doc.Project.DashboardPageURLTemplate; got != "https://app.contexo.live/repos/acme%20kb%2Fx/page/{path}" {
		t.Fatalf("template = %q", got)
	}
}

func TestFillStatusIdentity_DashboardEmptyOmitsTemplate(t *testing.T) {
	doc := st11IdentityDoc(t)
	r := st11Remote()
	r.ServerURL, r.DashboardURL = "https://contexo.acme.dev", ""
	FillStatusIdentity(doc, r, nil)
	if doc.Project.DashboardURL != "" || doc.Project.DashboardPageURLTemplate != "" {
		t.Fatalf("project = %+v", doc.Project)
	}
	var out bytes.Buffer
	if err := contract.WriteDoc(&out, doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"dashboard_url": ""`) || strings.Contains(out.String(), "dashboard_page_url_template") {
		t.Fatalf("E23: dashboard_url must be present and empty, the template absent:\n%s", out.String())
	}

	doc = st11IdentityDoc(t)
	r = st11Remote()
	r.RepoID = ""
	FillStatusIdentity(doc, r, nil)
	if doc.Project.DashboardURL != "https://app.contexo.live" || doc.Project.DashboardPageURLTemplate != "" {
		t.Fatalf("no repo id: project = %+v", doc.Project)
	}
}

func TestFillStatusIdentity_RemoteUnusablePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(*config.Remote)
		code   contract.ErrorCode
		advice string
		auth   contract.AuthState
	}{
		{"no token, server or repo", func(r *config.Remote) {
			r.Token, r.ServerURL, r.URLSource, r.RepoID = "", "", "", ""
			r.Creds = &config.Credentials{UserName: "Ana", UserEmail: "ana@acme.dev"}
		}, contract.CodeNotAuthenticated, "ctx auth login", contract.AuthNotAuthenticated},
		{"no token", func(r *config.Remote) {
			r.Token, r.Creds, r.CredsSource = "", nil, ""
		}, contract.CodeNotAuthenticated, "ctx auth login", contract.AuthNotAuthenticated},
		{"token, no server or repo", func(r *config.Remote) {
			r.ServerURL, r.URLSource, r.RepoID = "", "", ""
		}, contract.CodeServerUnresolved, "ctx auth login --server <url>", contract.AuthUnchecked},
		{"token and server, no repo", func(r *config.Remote) {
			r.RepoID = ""
		}, contract.CodeRepoNotConfigured, "ctx remote set-repo <id>", contract.AuthUnchecked},
	} {
		r := st11Remote()
		tc.edit(r)
		unusable := st11Unusable(r)
		doc := st11IdentityDoc(t)
		FillStatusIdentity(doc, r, unusable)

		want := ui.ToContractError(unusable).Message
		e := doc.Remote.Error
		if e == nil || e.Code != tc.code || !reflect.DeepEqual(e.Advice, []string{tc.advice}) || e.Retryable ||
			e.Message != want || e.HTTPStatus != 0 {
			t.Fatalf("%s: remote.error = %+v, want code %s message %q advice [%s]", tc.name, e, tc.code, want, tc.advice)
		}
		if doc.Auth.State != tc.auth || doc.Remote.Source != contract.RemoteNone {
			t.Fatalf("%s: auth %q remote %+v", tc.name, doc.Auth.State, doc.Remote)
		}
		if r.Token == "" && doc.Auth != (contract.AuthInfo{State: contract.AuthNotAuthenticated}) {
			t.Fatalf("%s: credentials fields without a token: %+v", tc.name, doc.Auth)
		}
	}

	// The case comes from the remote itself: a missing token outranks the
	// repo error the caller passed, and the message is then status's own.
	r := st11Remote()
	r.Token, r.RepoID = "", ""
	doc := st11IdentityDoc(t)
	FillStatusIdentity(doc, r, st11Unusable(&config.Remote{ServerURL: "https://x.dev", Token: "ctxp_y"}))
	if e := doc.Remote.Error; e == nil || e.Code != contract.CodeNotAuthenticated || e.Message != "status: not authenticated" ||
		!reflect.DeepEqual(e.Advice, []string{"ctx auth login"}) {
		t.Fatalf("remote.error = %+v", e)
	}
}

func TestFillStatusIdentity_NeverOverwritesEngineAuth(t *testing.T) {
	for _, s := range []contract.AuthState{contract.AuthAuthenticated, contract.AuthRejected, contract.AuthUnchecked} {
		doc := st11IdentityDoc(t)
		doc.Auth.State = s
		FillStatusIdentity(doc, st11Remote(), nil)
		if doc.Auth.State != s {
			t.Errorf("engine auth.state %q became %q", s, doc.Auth.State)
		}
	}
	doc := st11IdentityDoc(t)
	FillStatusIdentity(doc, st11Remote(), nil)
	if doc.Auth.State != contract.AuthUnchecked || doc.Remote.Error != nil {
		t.Fatalf("token, no request: auth %q remote.error %+v", doc.Auth.State, doc.Remote.Error)
	}
	r := st11Remote()
	r.Token, r.Creds = "", nil
	doc = st11IdentityDoc(t)
	FillStatusIdentity(doc, r, nil) // --offline passes no error
	if doc.Auth.State != contract.AuthNotAuthenticated || doc.Remote.Error != nil {
		t.Fatalf("no token, offline: auth %q remote.error %+v", doc.Auth.State, doc.Remote.Error)
	}

	// An engine remote.error (a failed listing) is never replaced.
	doc = st11IdentityDoc(t)
	doc.Auth.State = contract.AuthUnchecked
	netErr := contract.New(contract.CodeNetwork, "sync: list pages: offline").Body()
	doc.Remote.Error = &netErr
	FillStatusIdentity(doc, st11Remote(), errors.New("unexpected"))
	if doc.Remote.Error != &netErr || doc.Remote.Error.Code != contract.CodeNetwork {
		t.Fatalf("remote.error = %+v", doc.Remote.Error)
	}
}

func TestFillStatusIdentity_NilRemote(t *testing.T) {
	doc := st11IdentityDoc(t)
	FillStatusIdentity(doc, nil, errors.New("config: parse: bad"))
	want := &contract.ErrorBody{Code: contract.CodeInternal, Message: "config: parse: bad", Advice: []string{}}
	if !reflect.DeepEqual(doc.Remote.Error, want) {
		t.Fatalf("remote.error = %+v, want %+v", doc.Remote.Error, want)
	}
	if doc.Auth != (contract.AuthInfo{State: contract.AuthNotAuthenticated}) {
		t.Fatalf("auth = %+v", doc.Auth)
	}
	if doc.Project.RepoID != "acme-kb" || doc.Server.URL != "https://kb.acme.dev" || doc.Project.LastPullSHA != st11SHA(1) {
		t.Fatalf("engine identity lost: project %+v server %+v", doc.Project, doc.Server)
	}

	doc = st11IdentityDoc(t)
	FillStatusIdentity(doc, nil, nil)
	if doc.Auth.State != contract.AuthNotAuthenticated || doc.Remote.Error != nil {
		t.Fatalf("auth %q remote.error %+v", doc.Auth.State, doc.Remote.Error)
	}
}
