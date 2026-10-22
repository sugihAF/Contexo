package cli

// WP-1.1 end-to-end status tests against the real server (testkit). The
// pushes go through sync.Client directly and the returned SHAs are recorded
// as base and page_shas, as push does, so these tests do not depend on the
// push engine.

import (
	"reflect"
	"testing"

	ctxsync "github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/testkit"
)

// st11Entry returns doc's entry for path, failing the test when it is
// missing.
func st11Entry(t *testing.T, doc *contract.StatusDoc, path string) *contract.PageEntry {
	t.Helper()
	for i := range doc.Pages {
		if doc.Pages[i].Path == path {
			return &doc.Pages[i]
		}
	}
	t.Fatalf("no entry for %s in %+v", path, doc.Pages)
	return nil
}

// st11HasWarning reports whether doc carries a warning with code.
func st11HasWarning(doc *contract.StatusDoc, code contract.WarningCode) bool {
	for _, w := range doc.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

// After a push of a and b, an edit of b and a push of both (a is an
// unchanged re-push), the listing's last-touch sha of a equals the sha the
// server reported for it (write.path_sha), so a is unchanged, not incoming.
func TestStatusE2E_NoFalseIncomingAfterPartialPush(t *testing.T) {
	srv := testkit.StartServer(t)
	const repo = "st11-e2e"
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: srv.URL, Key: srv.Key, RepoID: repo,
		Pages: []testkit.PageSpec{{Slug: "a"}, {Slug: "b"}},
	})
	a, b := "wiki/concepts/a.md", "wiki/concepts/b.md"
	files := map[string][]byte{a: p.Read(a), b: p.Read(b)}
	first := st11Push(t, srv, repo, files, nil)
	st11RecordBases(t, p.HubDir, files, first)

	p.WritePage(testkit.PageSpec{Slug: "b", Body: "second version"})
	files[b] = p.Read(b)
	second := st11Push(t, srv, repo, files, first)
	if second[b] == first[b] {
		t.Fatalf("editing b made no new commit: %s", second[b])
	}
	if second[a] != first[a] {
		t.Errorf("the unchanged re-push of a reported %s, want its own last commit %s (write.path_sha)", second[a], first[a])
	}
	st11RecordBases(t, p.HubDir, files, second)

	doc, raw := st11StatusJSON(t, p.Root)
	st11CheckRaw(t, raw)
	for _, path := range []string{a, b} {
		e := st11Entry(t, doc, path)
		if e.State != contract.StateUnchanged {
			t.Errorf("%s: state %q, want unchanged (entry %+v)", path, e.State, e)
		}
		if e.Base == nil || e.Base.SHA != second[path] || e.Base.Stale {
			t.Errorf("%s: base %+v, want valid at %s", path, e.Base, second[path])
		}
		if e.Remote == nil || e.Remote.SHA != second[path] {
			t.Errorf("%s: remote %+v, want sha %s", path, e.Remote, second[path])
		}
	}
	if doc.Counts.Behind != 0 || doc.Counts.Incoming != 0 || doc.Counts.Unchanged != 2 {
		t.Errorf("counts = %+v, want 2 unchanged and nothing behind", doc.Counts)
	}
	if st11HasWarning(doc, contract.WarnServerOutdated) {
		t.Errorf("a v1 server must not be outdated: %+v", doc.Warnings)
	}
	want := []contract.ServerCapability{contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA}
	if !doc.Server.CapabilitiesKnown || !reflect.DeepEqual(doc.Server.Capabilities, want) {
		t.Errorf("server = %+v, want capabilities %q known", doc.Server, want)
	}
	r := doc.Remote
	if r.Source != contract.RemoteLive || r.RepoExists == nil || !*r.RepoExists || r.PageCount != 2 || r.Error != nil {
		t.Errorf("remote = %+v, want live, repo_exists, 2 pages, no error", r)
	}
	if doc.Auth.State != contract.AuthAuthenticated || doc.Auth.TokenKind != "legacy" {
		t.Errorf("auth = %+v, want authenticated with a legacy key", doc.Auth)
	}
}

// A repo the server does not have is a live answer, not a remote error: the
// real server's 404 repo_not_found reaches status through sync.Client as
// repo_exists false, and every local page is new.
func TestStatusE2E_RepoMissing(t *testing.T) {
	srv := testkit.StartServer(t)
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: srv.URL, Key: srv.Key, RepoID: "st11-nowhere",
		Pages: []testkit.PageSpec{{Slug: "a"}, {Slug: "b"}},
	})
	doc, raw := st11StatusJSON(t, p.Root)
	st11CheckRaw(t, raw)
	r := doc.Remote
	if r.Source != contract.RemoteLive || r.RepoExists == nil || *r.RepoExists || r.PageCount != 0 || r.Head != "" || r.Error != nil {
		t.Errorf("remote = %+v, want live, repo_exists false, no pages, no error", r)
	}
	if doc.Auth.State != contract.AuthAuthenticated {
		t.Errorf("auth.state = %q, want authenticated", doc.Auth.State)
	}
	for _, path := range []string{"wiki/concepts/a.md", "wiki/concepts/b.md"} {
		if e := st11Entry(t, doc, path); e.State != contract.StateNew || e.Remote != nil {
			t.Errorf("%s: state %q remote %+v, want new with no remote side", path, e.State, e.Remote)
		}
	}
	if doc.Counts.Ahead != 2 || len(doc.Warnings) != 0 {
		t.Errorf("counts = %+v warnings = %+v, want 2 ahead and no warning", doc.Counts, doc.Warnings)
	}
}

// A token the real server rejects: auth rejected, remote.error auth_rejected
// (401, advice to log in), local pages still classified, exit 0. The 401
// carries the v1 capability headers (E13), so the server is not outdated.
func TestStatusE2E_AuthRejected(t *testing.T) {
	srv := testkit.StartServer(t)
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: srv.URL, Key: "st11-wrong-key", RepoID: "st11-rejected",
		Pages: []testkit.PageSpec{{Slug: "a"}},
	})
	doc, raw := st11StatusJSON(t, p.Root)
	st11CheckRaw(t, raw)
	e := doc.Remote.Error
	if e == nil || e.Code != contract.CodeAuthRejected || e.HTTPStatus != 401 ||
		!reflect.DeepEqual(e.Advice, []string{"ctx auth login"}) || e.Retryable {
		t.Errorf("remote.error = %+v, want auth_rejected 401 with advice [ctx auth login]", e)
	}
	if doc.Auth.State != contract.AuthRejected || doc.Remote.Source != contract.RemoteNone {
		t.Errorf("auth/source = %q/%q, want rejected/none", doc.Auth.State, doc.Remote.Source)
	}
	want := []contract.ServerCapability{contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA}
	if !doc.Server.CapabilitiesKnown || !reflect.DeepEqual(doc.Server.Capabilities, want) || st11HasWarning(doc, contract.WarnServerOutdated) {
		t.Errorf("server = %+v warnings = %+v, want the v1 capabilities and no server_outdated", doc.Server, doc.Warnings)
	}
	if e := st11Entry(t, doc, "wiki/concepts/a.md"); e.State != contract.StateNew {
		t.Errorf("a.md: state %q, want new", e.State)
	}
	st11AssertNoSecret(t, "rejected", "st11-wrong-key", raw)
}

// A server from before contract v1 sends no capability header: the
// capabilities are known and empty, and status warns server_outdated.
func TestStatusE2E_OldServerOutdated(t *testing.T) {
	srv := testkit.StartOldServer(t)
	const repo = "st11-old"
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: srv.URL, Key: srv.Key, RepoID: repo,
		Pages: []testkit.PageSpec{{Slug: "a"}},
	})
	a := "wiki/concepts/a.md"
	files := map[string][]byte{a: p.Read(a)}
	st11RecordBases(t, p.HubDir, files, st11Push(t, srv, repo, files, nil))

	doc, raw := st11StatusJSON(t, p.Root)
	st11CheckRaw(t, raw)
	if !doc.Server.CapabilitiesKnown || doc.Server.Capabilities == nil || len(doc.Server.Capabilities) != 0 {
		t.Errorf("server = %+v, want capabilities [] known", doc.Server)
	}
	if !st11HasWarning(doc, contract.WarnServerOutdated) {
		t.Errorf("warnings = %+v, want server_outdated", doc.Warnings)
	}
	if e := st11Entry(t, doc, a); e.State != contract.StateUnchanged {
		t.Errorf("%s: state %q, want unchanged", a, e.State)
	}

	// The human form says so too.
	code, out, stderr := st11Run(t, "status", "--root", p.Root)
	if code != 0 {
		t.Fatalf("human status: exit %d, stderr %q", code, stderr)
	}
	for _, w := range doc.Warnings {
		if w.Code == contract.WarnServerOutdated {
			st11AssertContains(t, "old server (human)", out, w.Message)
		}
	}
}

// A stale base the server cannot repair (page_shas names an older commit,
// there is no base record, and an old server cannot read past versions) is
// unverified, named by base_repair_skipped (deviation 13), and the human
// form points at ctx repair. This holds with WP-1.5's real EnsureBase too.
func TestStatusE2E_OldServerStaleBaseUnverified(t *testing.T) {
	srv := testkit.StartOldServer(t)
	const repo = "st11-stale"
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: srv.URL, Key: srv.Key, RepoID: repo,
		Pages: []testkit.PageSpec{{Slug: "a"}},
	})
	a := "wiki/concepts/a.md"
	first := st11Push(t, srv, repo, map[string][]byte{a: p.Read(a)}, nil)
	p.WritePage(testkit.PageSpec{Slug: "a", Body: "second version"})
	second := st11Push(t, srv, repo, map[string][]byte{a: p.Read(a)}, first)
	if second[a] == first[a] {
		t.Fatalf("the second push made no new commit: %s", second[a])
	}
	// page_shas only (no base record), at the older commit; then a local edit.
	st, _, err := ctxsync.LoadStateOrEmpty(p.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	st.PageSHAs[a] = first[a]
	if err := ctxsync.SaveState(p.HubDir, st); err != nil {
		t.Fatal(err)
	}
	p.WritePage(testkit.PageSpec{Slug: "a", Body: "third version, local only"})

	doc, raw := st11StatusJSON(t, p.Root)
	st11CheckRaw(t, raw)
	e := st11Entry(t, doc, a)
	if e.State != contract.StateUnverified || e.Base == nil || !e.Base.Stale || e.Base.SHA != first[a] {
		t.Errorf("%s: state %q base %+v, want unverified with a stale base at %s", a, e.State, e.Base, first[a])
	}
	if e.Remote == nil || e.Remote.SHA != second[a] {
		t.Errorf("%s: remote %+v, want the listing sha %s", a, e.Remote, second[a])
	}
	var brs *contract.Warning
	for i := range doc.Warnings {
		if doc.Warnings[i].Code == contract.WarnBaseRepairSkipped {
			brs = &doc.Warnings[i]
		}
	}
	if brs == nil || !reflect.DeepEqual(brs.Paths, []string{a}) {
		t.Errorf("base_repair_skipped = %+v, want paths [%s]", brs, a)
	}
	if !st11HasWarning(doc, contract.WarnServerOutdated) || doc.Counts.Unverified != 1 {
		t.Errorf("warnings %+v counts %+v, want server_outdated and 1 unverified", doc.Warnings, doc.Counts)
	}

	code, out, stderr := st11Run(t, "status", "--root", p.Root)
	if code != 0 {
		t.Fatalf("human status: exit %d, stderr %q", code, stderr)
	}
	st11AssertContains(t, "stale base (human)", out, "UNVERIFIED", a, "ctx repair", brs.Message)
}
