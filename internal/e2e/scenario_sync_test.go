package e2e

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
)

// sy is the helper prefix of this file (scenarios 01, 02 and 03).

// syWantCaps is the exact X-Contexo-Capabilities value every v1 server
// response carries (E13; pages.etag is never advertised).
const syWantCaps = "pages.at,push.expect_absent,write.path_sha"

// syWantSrvCaps is syWantCaps as status reports it.
var syWantSrvCaps = []contract.ServerCapability{
	contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA,
}

// syPushedPaths returns the paths of doc.Pushed, in document order.
func syPushedPaths(doc contract.PushDoc) []string {
	out := []string{}
	for _, p := range doc.Pushed {
		out = append(out, p.Path)
	}
	return out
}

// syFilesSince returns the push files[] the proxy recorded after the first
// n.
func syFilesSince(px *recordingProxy, n int) []pushFile {
	all := px.PushedFiles()
	if n > len(all) {
		return nil
	}
	return all[n:]
}

// syPOSTs counts every POST the proxy recorded.
func syPOSTs(px *recordingProxy) int { return px.Count(http.MethodPost, "") }

// syOnlyPushed checks that doc pushed exactly rel with kind and returns its
// new sha.
func syOnlyPushed(t testing.TB, doc contract.PushDoc, rel string, kind contract.WriteKind) string {
	t.Helper()
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != rel || doc.Pushed[0].Kind != kind || doc.Pushed[0].SHA == "" {
		t.Fatalf("pushed = %+v, want exactly [{%s %s <sha>}]", doc.Pushed, rel, kind)
	}
	return doc.Pushed[0].SHA
}

// syCheckOneFile checks that files is exactly one push file for rel with
// parent and expect_absent false.
func syCheckOneFile(t testing.TB, files []pushFile, rel, parent string) {
	t.Helper()
	want := pushFile{Path: rel, ParentSHA: parent}
	if len(files) != 1 || files[0] != want {
		t.Fatalf("proxy push files = %+v, want exactly [%+v]", files, want)
	}
}

// syNoSidecar checks that rel has neither a conflicts.json record nor a
// .sync/conflicts/<rel> file.
func syNoSidecar(t testing.TB, u *e2eUser, rel string) {
	t.Helper()
	if rec, ok := readConflicts(t, u)[rel]; ok {
		t.Errorf("conflicts.json still has %s: %+v", rel, rec)
	}
	if _, err := os.Stat(syncstate.ConflictFile(u.Hub, rel)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(".sync/conflicts/%s: want absent, stat err = %v", rel, err)
	}
}

// syConflict is the both_modified setup shared by scenarios 02 and 03.
type syConflict struct {
	srv        *e2eServer
	alice, bob *e2eUser
	rel        string
	aliceSHA   string // the sha alice pushed P at (her base)
	bobSHA     string // the teammate's sha (the server's)
	aliceBytes []byte // alice's edited working file
}

// sySetupConflict: alice pushes P, bob joins the repo and pulls it, bob
// edits and pushes P, and alice first sees P incoming (behind 1) and then,
// after her own edit, conflict both_modified.
func sySetupConflict(t *testing.T, slug string) *syConflict {
	t.Helper()
	srv := startServer(t)
	spec := conceptPage(slug, "Shared v1.")
	rel := pageRel(spec)
	alice := newUser(t, srv, "alice", withPages(spec))
	aliceSHA := syOnlyPushed(t, mustPush(t, alice), rel, contract.WriteCreate)

	bob := newUser(t, srv, "bob")
	pd := mustPull(t, bob)
	if len(pd.Written) != 1 || pd.Written[0] != (contract.PulledPage{Path: rel, SHA: aliceSHA, Kind: contract.WriteCreate}) {
		t.Fatalf("bob's first pull written = %+v, want [{%s %s create}]", pd.Written, rel, aliceSHA)
	}
	editPage(t, bob, rel, "Bob's teammate edit.")
	bobSHA := syOnlyPushed(t, mustPush(t, bob, "--yes"), rel, contract.WriteUpdate)
	if bobSHA == aliceSHA {
		t.Fatalf("bob's push did not move %s (sha %s)", rel, bobSHA)
	}

	st := mustStatus(t, alice, false)
	e := mustPage(t, st, rel)
	if e.State != contract.StateIncoming || e.Base == nil || e.Base.SHA != aliceSHA || e.Remote == nil || e.Remote.SHA != bobSHA {
		t.Fatalf("alice %s = %+v (base %+v remote %+v), want incoming base %s remote %s",
			rel, e, e.Base, e.Remote, aliceSHA, bobSHA)
	}
	if st.Counts.Incoming != 1 || st.Counts.Behind != 1 || st.Counts.Ahead != 0 {
		t.Fatalf("alice counts = %+v, want incoming 1 behind 1 ahead 0", st.Counts)
	}

	editPage(t, alice, rel, "Alice's own edit.")
	st = mustStatus(t, alice, false)
	e = mustPage(t, st, rel)
	if e.State != contract.StateConflict || e.ConflictKind != contract.ConflictBothModified {
		t.Fatalf("alice %s after her edit = %s/%s, want conflict/both_modified", rel, e.State, e.ConflictKind)
	}
	if e.Remote == nil || e.Remote.SHA != bobSHA || e.Base == nil || e.Base.SHA != aliceSHA {
		t.Fatalf("alice %s conflict sides base %+v remote %+v, want base %s remote %s", rel, e.Base, e.Remote, aliceSHA, bobSHA)
	}
	if st.Counts.Conflict != 1 || st.Counts.Behind != 0 || st.Counts.Ahead != 0 {
		t.Fatalf("alice counts = %+v, want conflict 1 behind 0 ahead 0", st.Counts)
	}
	return &syConflict{srv: srv, alice: alice, bob: bob, rel: rel,
		aliceSHA: aliceSHA, bobSHA: bobSHA, aliceBytes: alice.P.Read(rel)}
}

// TestScenario01_PartialPushNoFalseDrift: after a five-page push only the
// edited page is sent (push --json without --yes, and the E25 argv with
// named paths), a no-op push makes no request, status shows no drift, every
// request carries the client name and every response the v1 capabilities.
func TestScenario01_PartialPushNoFalseDrift(t *testing.T) {
	srv := startServer(t)
	specs := []testkit.PageSpec{
		conceptPage("alpha", "Alpha v1."),
		conceptPage("bravo", "Bravo v1."),
		conceptPage("charlie", "Charlie v1."),
		entityPage("delta", "Delta v1."),
		entityPage("echo", "Echo v1."),
	}
	rels := make([]string, len(specs))
	for i, s := range specs {
		rels[i] = pageRel(s)
	}
	sortStrings(rels)
	alice := newUser(t, srv, "alice", withPages(specs...))

	// The first push creates the repo (R1) and sends all five create-only.
	doc := mustPush(t, alice)
	if got := syPushedPaths(doc); !equalStrings(got, rels) {
		t.Fatalf("first push pushed %q, want %q", got, rels)
	}
	shas := map[string]string{}
	for _, p := range doc.Pushed {
		if p.Kind != contract.WriteCreate || p.SHA == "" {
			t.Errorf("first push %s = %+v, want kind create with a sha", p.Path, p)
		}
		shas[p.Path] = p.SHA
	}
	if doc.Schema != contract.SchemaPush || doc.DryRun || doc.RepoID != defaultRepo || doc.ServerURL != srv.URL ||
		doc.NewHead == "" || len(doc.Conflicts) != 0 || len(doc.Skipped) != 0 || len(doc.Warnings) != 0 ||
		doc.Distill != (contract.DistillCheck{}) {
		t.Errorf("first push document = %+v", doc)
	}
	for _, f := range srv.Proxy.PushedFiles() {
		if !f.ExpectAbsent || f.ParentSHA != "" {
			t.Errorf("first push file %+v, want expect_absent true and no parent (E7)", f)
		}
	}
	if got := srv.Proxy.PushedPaths(); len(got) != len(rels) {
		t.Errorf("proxy saw pushed paths %q, want the %d pages once", got, len(rels))
	}

	// E41: .sync ignores itself after the first write.
	gi, err := os.ReadFile(filepath.Join(syncstate.SyncDir(alice.Hub), ".gitignore"))
	if err != nil || string(gi) != "*\n" {
		t.Errorf(".contexo/.sync/.gitignore = %q (err %v), want \"*\\n\" (E41)", gi, err)
	}

	// Edit one page: push --json without --yes never prompts and sends
	// exactly that page with its base as parent.
	editPage(t, alice, rels[0], "Alpha edit 1.")
	n := len(srv.Proxy.PushedFiles())
	doc = mustPush(t, alice)
	newSHA := syOnlyPushed(t, doc, rels[0], contract.WriteUpdate)
	if newSHA == shas[rels[0]] {
		t.Errorf("%s pushed at its old sha %s", rels[0], newSHA)
	}
	if len(doc.Skipped) != 0 || len(doc.Conflicts) != 0 {
		t.Errorf("partial push skipped %+v conflicts %+v, want none", doc.Skipped, doc.Conflicts)
	}
	syCheckOneFile(t, syFilesSince(srv.Proxy, n), rels[0], shas[rels[0]])
	shas[rels[0]] = newSHA

	// Edit another page: the extension argv push --json --yes -- <path>.
	editPage(t, alice, rels[1], "Bravo edit 1.")
	n = len(srv.Proxy.PushedFiles())
	doc = mustPush(t, alice, "--yes", "--", rels[1])
	newSHA = syOnlyPushed(t, doc, rels[1], contract.WriteUpdate)
	if len(doc.Skipped) != 0 || len(doc.Conflicts) != 0 {
		t.Errorf("named push skipped %+v conflicts %+v, want none", doc.Skipped, doc.Conflicts)
	}
	syCheckOneFile(t, syFilesSince(srv.Proxy, n), rels[1], shas[rels[1]])
	shas[rels[1]] = newSHA

	// A second push has nothing to send and makes no POST.
	posts := syPOSTs(srv.Proxy)
	doc = mustPush(t, alice, "--yes")
	if len(doc.Pushed) != 0 || doc.NewHead != "" || len(doc.Skipped) != 0 || len(doc.Conflicts) != 0 {
		t.Errorf("no-op push = %+v, want pushed [] and no new_head", doc)
	}
	if got := syPOSTs(srv.Proxy); got != posts {
		t.Errorf("no-op push made %d POST(s), want 0", got-posts)
	}

	// No false drift: every page unchanged at its own last-touch sha.
	st := mustStatus(t, alice, false)
	if len(st.Pages) != len(rels) {
		t.Fatalf("status lists %d pages, want %d: %+v", len(st.Pages), len(rels), st.Pages)
	}
	for _, rel := range rels {
		e := mustPage(t, st, rel)
		if e.State != contract.StateUnchanged || e.Base == nil || e.Base.SHA != shas[rel] || e.Base.Stale ||
			e.Remote == nil || e.Remote.SHA != shas[rel] {
			t.Errorf("%s = %s base %+v remote %+v, want unchanged at %s", rel, e.State, e.Base, e.Remote, shas[rel])
		}
	}
	if want := (contract.StateCounts{Unchanged: len(rels)}); st.Counts != want {
		t.Errorf("counts = %+v, want %+v (ahead 0)", st.Counts, want)
	}
	if st.Remote.Source != contract.RemoteLive || !st.Server.CapabilitiesKnown || len(st.Warnings) != 0 {
		t.Errorf("status remote %+v server %+v warnings %+v", st.Remote, st.Server, st.Warnings)
	}
	if !syEqualCaps(st.Server.Capabilities, syWantSrvCaps) {
		t.Errorf("server.capabilities = %q, want %q", st.Server.Capabilities, syWantSrvCaps)
	}

	// A pull with nothing new writes no page.
	pd := mustPull(t, alice)
	if len(pd.Written) != 0 || len(pd.Adopted) != 0 || len(pd.Skipped) != 0 || len(pd.Warnings) != 0 {
		t.Errorf("pull = %+v, want nothing written, adopted or skipped", pd)
	}
	if head := serverHead(t, srv, defaultRepo); pd.Head != head {
		t.Errorf("pull head = %s, want the server HEAD %s", pd.Head, head)
	}

	// The proxy saw the client name on the listing, the push and the pull
	// requests, and every response advertised exactly the v1 capabilities.
	var lists, pushes, pulls int
	for _, rec := range srv.Proxy.Records() {
		if rec.Client != clientName {
			t.Errorf("%s %s: X-Contexo-Client %q, want %q", rec.Method, rec.Path, rec.Client, clientName)
		}
		if rec.Caps != syWantCaps {
			t.Errorf("%s %s (%d): X-Contexo-Capabilities %q, want exactly %q (E13)", rec.Method, rec.Path, rec.Status, rec.Caps, syWantCaps)
		}
		switch {
		case rec.Method == http.MethodGet && strings.HasSuffix(rec.Path, "/repos/"+defaultRepo+"/pages"):
			lists++
		case rec.Method == http.MethodPost && strings.HasSuffix(rec.Path, "/sync/push"):
			pushes++
		case rec.Method == http.MethodGet && strings.HasSuffix(rec.Path, "/sync/pull"):
			pulls++
		}
	}
	if lists == 0 || pushes == 0 || pulls == 0 {
		t.Errorf("proxy saw %d listing(s), %d push(es), %d pull(s); want each at least once", lists, pushes, pulls)
	}
}

// syEqualCaps reports whether a and b hold the same capabilities in order.
func syEqualCaps(a, b []contract.ServerCapability) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// syBackupRel matches ResolveDoc.backup_file for page rel.
func syBackupRel(rel string) *regexp.Regexp {
	return regexp.MustCompile(`^\.sync/backup/` + regexp.QuoteMeta(rel) + `\.\d{8}T\d{6}Z$`)
}

// TestScenario02_IncomingConflictPullSkipResolveTheirs: a teammate's edit
// is incoming, a local edit on top makes it a conflict that pull skips
// (writing a pull sidecar, E10) and offline status still shows (E9), and
// resolve --theirs with the extension's stale guard backs up the local file
// and clears the sidecar.
func TestScenario02_IncomingConflictPullSkipResolveTheirs(t *testing.T) {
	c := sySetupConflict(t, "shared-theirs")
	alice, rel := c.alice, c.rel

	// pull skips the conflict and keeps alice's bytes.
	pd := mustPull(t, alice)
	want := contract.SkippedPage{Path: rel, State: contract.StateConflict, Reason: contract.SkipConflict, ServerSHA: c.bobSHA}
	if len(pd.Skipped) != 1 || pd.Skipped[0] != want {
		t.Fatalf("pull skipped = %+v, want [%+v]", pd.Skipped, want)
	}
	if len(pd.Written) != 0 || len(pd.Adopted) != 0 {
		t.Errorf("pull written %+v adopted %+v, want none", pd.Written, pd.Adopted)
	}
	if got := alice.P.Read(rel); !bytes.Equal(got, c.aliceBytes) {
		t.Fatalf("pull changed %s:\n%s\nwant\n%s", rel, got, c.aliceBytes)
	}
	rec, ok := readConflicts(t, alice)[rel]
	if !ok {
		t.Fatalf("conflicts.json has no record for %s after the pull skip (E10)", rel)
	}
	if rec.Source != "pull" || rec.ServerSHA != c.bobSHA || rec.BaseSHA != c.aliceSHA ||
		rec.Kind != string(contract.ConflictBothModified) || !rec.HasContent || !reStrictTime.MatchString(rec.DetectedAt) {
		t.Errorf("conflicts.json[%s] = %+v, want source pull, server %s, base %s, both_modified, content", rel, rec, c.bobSHA, c.aliceSHA)
	}
	side, err := os.ReadFile(syncstate.ConflictFile(alice.Hub, rel))
	if err != nil {
		t.Fatalf("pull sidecar content: %v", err)
	}
	server := serverContent(t, c.srv, defaultRepo, rel)
	if !bytes.Equal(side, server) {
		t.Errorf(".sync/conflicts/%s = %q, want the server bytes %q", rel, side, server)
	}

	// Offline, the sidecar alone keeps the conflict, with remote {sha} only.
	off := mustStatus(t, alice, true)
	e := mustPage(t, off, rel)
	if e.State != contract.StateConflict || e.ConflictKind != contract.ConflictBothModified {
		t.Errorf("offline %s = %s/%s, want conflict/both_modified (E9)", rel, e.State, e.ConflictKind)
	}
	if e.Remote == nil || *e.Remote != (contract.RemoteSide{SHA: c.bobSHA}) {
		t.Errorf("offline %s remote = %+v, want {sha: %s} only (E9)", rel, e.Remote, c.bobSHA)
	}
	if !off.Offline || off.Remote.Source != contract.RemoteNone || off.Counts.Conflict != 1 {
		t.Errorf("offline status offline %v remote %+v counts %+v", off.Offline, off.Remote, off.Counts)
	}

	// resolve --theirs with the extension's --if-server-sha guard.
	var rd contract.ResolveDoc
	assertOK(t, runCtx(t, alice, "resolve", rel, "--theirs", "--json", "--if-server-sha", c.bobSHA), &rd)
	if rd.Schema != contract.SchemaResolve || rd.Path != rel || rd.Strategy != contract.ResolveTheirs ||
		rd.PreviousState != contract.StateConflict || rd.State != contract.StateUnchanged ||
		rd.BaseSHA != c.bobSHA || !rd.FileWritten || len(rd.Warnings) != 0 {
		t.Errorf("resolve --theirs = %+v", rd)
	}
	if !syBackupRel(rel).MatchString(rd.BackupFile) {
		t.Fatalf("backup_file = %q, want .sync/backup/%s.<YYYYMMDDTHHMMSSZ> relative to .contexo", rd.BackupFile, rel)
	}
	if got := readHubFile(t, alice, rd.BackupFile); !bytes.Equal(got, c.aliceBytes) {
		t.Errorf("backup %s = %q, want alice's edited bytes", rd.BackupFile, got)
	}
	if got := alice.P.Read(rel); !bytes.Equal(got, server) {
		t.Errorf("%s after --theirs = %q, want the server bytes %q", rel, got, server)
	}
	syNoSidecar(t, alice, rel)
	if sha := loadState(t, alice).PageSHAs[rel]; sha != c.bobSHA {
		t.Errorf("page_shas[%s] = %s, want %s", rel, sha, c.bobSHA)
	}

	st := mustStatus(t, alice, false)
	if e := mustPage(t, st, rel); e.State != contract.StateUnchanged {
		t.Errorf("status %s after --theirs = %s, want unchanged", rel, e.State)
	}
	if want := (contract.StateCounts{Unchanged: 1}); st.Counts != want {
		t.Errorf("counts = %+v, want %+v", st.Counts, want)
	}
}

// TestScenario03_ResolveOursPushTeammatePulls: resolve --ours keeps the
// local bytes and re-bases them onto the server sha, the next push sends
// them with that parent, and the teammate pulls alice's content.
func TestScenario03_ResolveOursPushTeammatePulls(t *testing.T) {
	c := sySetupConflict(t, "shared-ours")
	alice, bob, rel := c.alice, c.bob, c.rel

	var rd contract.ResolveDoc
	assertOK(t, runCtx(t, alice, "resolve", rel, "--ours", "--json"), &rd)
	want := contract.ResolveDoc{Schema: contract.SchemaResolve, Path: rel, Strategy: contract.ResolveOurs,
		PreviousState: contract.StateConflict, State: contract.StateModified, BaseSHA: c.bobSHA,
		Warnings: []contract.Warning{}}
	if rd.Schema != want.Schema || rd.Path != want.Path || rd.Strategy != want.Strategy ||
		rd.PreviousState != want.PreviousState || rd.State != want.State || rd.BaseSHA != want.BaseSHA ||
		rd.FileWritten || rd.BackupFile != "" || len(rd.Warnings) != 0 {
		t.Fatalf("resolve --ours = %+v, want %+v", rd, want)
	}
	if got := alice.P.Read(rel); !bytes.Equal(got, c.aliceBytes) {
		t.Fatalf("resolve --ours rewrote %s:\n%s", rel, got)
	}
	if sha := loadState(t, alice).PageSHAs[rel]; sha != c.bobSHA {
		t.Errorf("page_shas[%s] = %s, want the server sha %s", rel, sha, c.bobSHA)
	}
	if b := readBases(t, alice)[rel]; b.SHA != c.bobSHA {
		t.Errorf("bases.json[%s].sha = %s, want %s", rel, b.SHA, c.bobSHA)
	}
	syNoSidecar(t, alice, rel)
	st := mustStatus(t, alice, false)
	e := mustPage(t, st, rel)
	if e.State != contract.StateModified || e.Base == nil || e.Base.SHA != c.bobSHA || e.Remote == nil || e.Remote.SHA != c.bobSHA {
		t.Fatalf("status %s after --ours = %s base %+v remote %+v, want modified on %s", rel, e.State, e.Base, e.Remote, c.bobSHA)
	}

	// The push sends P with parent = the teammate's sha.
	n := len(c.srv.Proxy.PushedFiles())
	doc := mustPush(t, alice, "--yes")
	aliceSHA := syOnlyPushed(t, doc, rel, contract.WriteUpdate)
	syCheckOneFile(t, syFilesSince(c.srv.Proxy, n), rel, c.bobSHA)
	if got := serverSHA(t, c.srv, defaultRepo, rel); got != aliceSHA {
		t.Errorf("server sha of %s = %s, want alice's %s", rel, got, aliceSHA)
	}
	server := serverContent(t, c.srv, defaultRepo, rel)
	if !bytes.Contains(server, []byte("Alice's own edit.")) || bytes.Contains(server, []byte("Bob's teammate edit.")) {
		t.Errorf("server %s = %q, want alice's version (it supersedes bob's)", rel, server)
	}

	// The teammate pulls alice's content.
	pd := mustPull(t, bob)
	wantW := contract.PulledPage{Path: rel, SHA: aliceSHA, Kind: contract.WriteUpdate}
	if len(pd.Written) != 1 || pd.Written[0] != wantW {
		t.Fatalf("bob pull written = %+v, want [%+v]", pd.Written, wantW)
	}
	if len(pd.Skipped) != 0 || len(pd.Adopted) != 0 {
		t.Errorf("bob pull skipped %+v adopted %+v, want none", pd.Skipped, pd.Adopted)
	}
	if got := bob.P.Read(rel); !bytes.Equal(got, server) {
		t.Errorf("bob's %s = %q, want the server bytes %q", rel, got, server)
	}
	if got := bob.P.Read(rel); !bytes.Equal(got, c.aliceBytes) {
		t.Errorf("bob's %s differs from alice's working file:\n%s\nwant\n%s", rel, got, c.aliceBytes)
	}
	if e := mustPage(t, mustStatus(t, bob, false), rel); e.State != contract.StateUnchanged {
		t.Errorf("bob status %s = %s, want unchanged", rel, e.State)
	}
}
