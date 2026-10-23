package syncops

// Owner: WP-1.2. Tests of the push engine on syncopstest.FakeRemote. p12Env
// replaces the pushEnsureBase seam with a recording no-op (restored by
// t.Cleanup), so these tests survive WP-1.5's real EnsureBase; the Resolved
// pre-step tests override pushResolveFn the same way.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncops/syncopstest"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// p12Remote wraps a FakeRemote: it records every PushRequest body (the fake
// only counts calls) and lets a test rewrite, or act right after, the
// listing.
type p12Remote struct {
	*syncopstest.FakeRemote
	pushes   []sync.PushRequest
	listHook func(pages []sync.PageMeta, err error) ([]sync.PageMeta, error)
}

func (r *p12Remote) PushPages(repoID string, req *sync.PushRequest) (*sync.PushResponse, error) {
	if req != nil {
		cp := *req
		cp.Files = append([]sync.PushFile(nil), req.Files...)
		r.pushes = append(r.pushes, cp)
	}
	return r.FakeRemote.PushPages(repoID, req)
}

func (r *p12Remote) ListPages(repoID string) ([]sync.PageMeta, error) {
	pages, err := r.FakeRemote.ListPages(repoID)
	if r.listHook != nil {
		return r.listHook(pages, err)
	}
	return pages, err
}

// p12Fix is one push test project: an Env on a p12Remote and the
// EnsureBase calls the default seam recorded.
type p12Fix struct {
	t      *testing.T
	env    Env
	hub    string
	fake   *syncopstest.FakeRemote
	remote *p12Remote
	ensure []EnsureBaseOptions
}

// p12Env builds a push fixture (p12GuardEnv plus a FakeRemote) and replaces
// pushEnsureBase with a recording no-op until the test ends.
func p12Env(t *testing.T) *p12Fix {
	t.Helper()
	env := p12GuardEnv(t)
	fake := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	r := &p12Remote{FakeRemote: fake}
	env.Remote = r
	fx := &p12Fix{t: t, env: env, hub: env.HubDir, fake: fake, remote: r}
	old := pushEnsureBase
	pushEnsureBase = func(ctx context.Context, env Env, o EnsureBaseOptions) (*EnsureBaseResult, error) {
		fx.ensure = append(fx.ensure, o)
		return &EnsureBaseResult{Repaired: []string{}, Unverified: []string{}, Warnings: []contract.Warning{}}, nil
	}
	t.Cleanup(func() { pushEnsureBase = old })
	return fx
}

// p12OverrideResolve replaces pushResolveFn until the test ends.
func p12OverrideResolve(t *testing.T, fn func(ctx context.Context, env Env, o ResolveOptions) (*contract.ResolveDoc, error)) {
	t.Helper()
	old := pushResolveFn
	pushResolveFn = fn
	t.Cleanup(func() { pushResolveFn = old })
}

func p12Norm(t *testing.T, data []byte) []byte {
	t.Helper()
	n, err := syncstate.Normalize(data)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return n
}

// page writes a valid page of typ and slug and returns its path and bytes.
func (fx *p12Fix) page(typ schema.PageType, slug, body string, tags ...string) (string, []byte) {
	fx.t.Helper()
	rel := (&schema.PageFrontmatter{Type: typ, Slug: slug}).RelPath()
	data := p12Page(fx.t, typ, slug, body, tags...)
	p12WriteFile(fx.t, fx.hub, rel, data)
	return rel, data
}

// edit rewrites the local page rel (type typ, slug from the path) with body.
func (fx *p12Fix) edit(rel string, typ schema.PageType, body string) []byte {
	fx.t.Helper()
	slug := strings.TrimSuffix(filepath.Base(filepath.FromSlash(rel)), ".md")
	data := p12Page(fx.t, typ, slug, body)
	p12WriteFile(fx.t, fx.hub, rel, data)
	return data
}

// synced makes rel synced at data: the server gets Normalize(data) as one
// commit, and the base and page_shas point at it.
func (fx *p12Fix) synced(rel string, data []byte) string {
	fx.t.Helper()
	norm := p12Norm(fx.t, data)
	sha := fx.fake.Seed(rel, norm)
	fx.setBase(rel, norm, sha)
	return sha
}

func (fx *p12Fix) setBase(rel string, content []byte, sha string) {
	fx.t.Helper()
	bases, err := syncstate.OpenBases(fx.hub)
	if err != nil {
		fx.t.Fatal(err)
	}
	if err := bases.Put(rel, content, sha, p12Now); err != nil {
		fx.t.Fatal(err)
	}
	if err := bases.Save(); err != nil {
		fx.t.Fatal(err)
	}
	fx.setPageSHA(rel, sha)
}

func (fx *p12Fix) setPageSHA(rel, sha string) {
	fx.t.Helper()
	st, err := sync.LoadState(fx.hub)
	if err != nil {
		fx.t.Fatal(err)
	}
	st.PageSHAs[rel] = sha
	if err := sync.SaveState(fx.hub, st); err != nil {
		fx.t.Fatal(err)
	}
}

func (fx *p12Fix) pageSHAs() map[string]string {
	fx.t.Helper()
	st, err := sync.LoadState(fx.hub)
	if err != nil {
		fx.t.Fatal(err)
	}
	return st.PageSHAs
}

func (fx *p12Fix) baseRecord(rel string) (syncstate.BaseRecord, bool) {
	fx.t.Helper()
	bases, err := syncstate.OpenBases(fx.hub)
	if err != nil {
		fx.t.Fatal(err)
	}
	return bases.Get(rel)
}

func (fx *p12Fix) baseBytes(rel string) []byte {
	fx.t.Helper()
	b, err := os.ReadFile(syncstate.BaseFile(fx.hub, rel))
	if err != nil {
		fx.t.Fatalf("read base of %s: %v", rel, err)
	}
	return b
}

func (fx *p12Fix) conflicts() *syncstate.Conflicts {
	fx.t.Helper()
	c, err := syncstate.OpenConflicts(fx.hub)
	if err != nil {
		fx.t.Fatal(err)
	}
	return c
}

func (fx *p12Fix) push(o PushOptions) (*contract.PushDoc, error) {
	return Push(context.Background(), fx.env, o)
}

func (fx *p12Fix) requests(method string) int {
	return fx.fake.Requests()[method]
}

// lastFiles returns the files of the last PushPages request.
func (fx *p12Fix) lastFiles() []sync.PushFile {
	fx.t.Helper()
	if len(fx.remote.pushes) == 0 {
		fx.t.Fatal("no PushPages request was made")
	}
	return fx.remote.pushes[len(fx.remote.pushes)-1].Files
}

// listing returns the server's path -> last-touch sha. It is a ListPages
// call: use it after the request-count assertions.
func (fx *p12Fix) listing() map[string]string {
	fx.t.Helper()
	pages, err := fx.fake.ListPages("acme-kb")
	if err != nil {
		fx.t.Fatal(err)
	}
	out := map[string]string{}
	for _, p := range pages {
		out[p.Path] = p.SHA
	}
	return out
}

// p12AssertDoc checks the fixed fields of a push document and that no
// array is nil.
func p12AssertDoc(t *testing.T, doc *contract.PushDoc) {
	t.Helper()
	if doc == nil {
		t.Fatal("Push returned a nil document")
	}
	if doc.Schema != contract.SchemaPush || doc.ServerURL != "https://kb.acme.dev" || doc.RepoID != "acme-kb" {
		t.Fatalf("doc header = {%q %q %q}", doc.Schema, doc.ServerURL, doc.RepoID)
	}
	if doc.Pushed == nil || doc.Conflicts == nil || doc.Skipped == nil || doc.Warnings == nil {
		t.Fatalf("doc has a nil array: %+v", doc)
	}
}

func p12AssertSkipped(t *testing.T, got, want []contract.SkippedPage) {
	t.Helper()
	if want == nil {
		want = []contract.SkippedPage{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("skipped = %+v\nwant      %+v", got, want)
	}
}

func p12AssertFiles(t *testing.T, got, want []sync.PushFile) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request files = %+v\nwant            %+v", got, want)
	}
}

func TestPush_OnlyChangedPagesSent(t *testing.T) {
	fx := p12Env(t)
	relA, dataA := fx.page(schema.TypeConcept, "a", "# A\n")
	shaA := fx.synced(relA, dataA)
	relB, dataB := fx.page(schema.TypeEntity, "b", "# B\n")
	shaB := fx.synced(relB, dataB)
	dataB2 := fx.edit(relB, schema.TypeEntity, "# B\n\nEdited.\n")
	relD, dataD := fx.page(schema.TypeSource, "d", "# D\n")

	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	p12AssertDoc(t, doc)
	if got := fx.requests("PushPages"); got != 1 {
		t.Fatalf("PushPages calls = %d, want 1", got)
	}
	req := fx.remote.pushes[0]
	if req.AuthorName != "Ana" || req.AuthorEmail != "ana@acme.dev" || req.Message != "ctx push (2 pages)" {
		t.Fatalf("request header = {%q %q %q}", req.AuthorName, req.AuthorEmail, req.Message)
	}
	p12AssertFiles(t, req.Files, []sync.PushFile{
		{Path: relD, Content: string(p12Norm(t, dataD)), ExpectAbsent: true},
		{Path: relB, Content: string(p12Norm(t, dataB2)), ParentSHA: shaB},
	})
	if len(fx.ensure) != 1 || !fx.ensure[0].Locked {
		t.Fatalf("EnsureBase calls = %+v, want one with Locked", fx.ensure)
	}

	live := fx.listing()
	wantPushed := []contract.PushedPage{
		{Path: relD, Kind: contract.WriteCreate, SHA: live[relD]},
		{Path: relB, Kind: contract.WriteUpdate, SHA: live[relB]},
	}
	if !reflect.DeepEqual(doc.Pushed, wantPushed) {
		t.Fatalf("pushed = %+v, want %+v", doc.Pushed, wantPushed)
	}
	if doc.DryRun || doc.Message != "ctx push (2 pages)" || doc.NewHead != fx.fake.Head() {
		t.Fatalf("doc = {dry_run %v, message %q, new_head %q}, want {false, ctx push (2 pages), %q}",
			doc.DryRun, doc.Message, doc.NewHead, fx.fake.Head())
	}
	if len(doc.Conflicts) != 0 || len(doc.Skipped) != 0 || len(doc.Warnings) != 0 {
		t.Fatalf("doc = %+v, want no conflicts, skips or warnings", doc)
	}
	if doc.Distill != (contract.DistillCheck{}) {
		t.Fatalf("distill = %+v, want {false false}", doc.Distill)
	}

	shas := fx.pageSHAs()
	if shas[relA] != shaA || shas[relB] != live[relB] || shas[relD] != live[relD] || live[relB] == shaB {
		t.Fatalf("page_shas = %v, listing %v", shas, live)
	}
	for _, c := range []struct {
		rel  string
		data []byte
	}{{relB, dataB2}, {relD, dataD}} {
		norm := p12Norm(t, c.data)
		if got := fx.baseBytes(c.rel); string(got) != string(norm) {
			t.Fatalf("base of %s = %q, want Normalize(local) %q", c.rel, got, norm)
		}
		rec, ok := fx.baseRecord(c.rel)
		if !ok || rec.SHA != live[c.rel] || rec.Hash != syncstate.Hash(norm) || !rec.SyncedAt.Equal(contract.TimeOf(p12Now)) {
			t.Fatalf("bases.json[%s] = %+v (ok %v), want sha %s hash %s", c.rel, rec, ok, live[c.rel], syncstate.Hash(norm))
		}
	}
}

func TestPush_SecondPushSendsNothing(t *testing.T) {
	fx := p12Env(t)
	relA, dataA := fx.page(schema.TypeEntity, "a", "# A\n")
	fx.synced(relA, dataA)
	fx.edit(relA, schema.TypeEntity, "# A\n\nEdited.\n")
	fx.page(schema.TypeEntity, "b", "# B\n")

	if _, err := fx.push(PushOptions{}); err != nil {
		t.Fatalf("first Push: %v", err)
	}
	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatalf("second Push: %v", err)
	}
	p12AssertDoc(t, doc)
	if got := fx.requests("PushPages"); got != 1 {
		t.Fatalf("PushPages calls = %d after two pushes, want 1", got)
	}
	if len(doc.Pushed) != 0 || len(doc.Conflicts) != 0 || len(doc.Skipped) != 0 || doc.NewHead != "" {
		t.Fatalf("second doc = %+v, want nothing pushed, no conflicts or skips, no new_head", doc)
	}
	if doc.Message != "ctx push (0 pages)" {
		t.Fatalf("message = %q", doc.Message)
	}
}

func TestPush_ExpectAbsentSentWhenNoBase(t *testing.T) {
	fx := p12Env(t)
	relX, dataX := fx.page(schema.TypeEntity, "x", "# X\n")
	relY, dataY := fx.page(schema.TypeEntity, "y", "# Y\n")
	shaY := fx.synced(relY, dataY)
	dataY2 := fx.edit(relY, schema.TypeEntity, "# Y\n\nEdited.\n")
	// A stale base the server no longer lists: new (missing_on_server), with
	// a parent but no expect_absent.
	relZ, dataZ := fx.page(schema.TypeEntity, "z", "# Z\n")
	const staleSHA = "0123456789abcdef0123456789abcdef01234567"
	fx.setPageSHA(relZ, staleSHA)

	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	p12AssertDoc(t, doc)
	p12AssertFiles(t, fx.lastFiles(), []sync.PushFile{
		{Path: relX, Content: string(p12Norm(t, dataX)), ExpectAbsent: true},
		{Path: relY, Content: string(p12Norm(t, dataY2)), ParentSHA: shaY},
		{Path: relZ, Content: string(p12Norm(t, dataZ)), ParentSHA: staleSHA},
	})
	kinds := map[string]contract.WriteKind{}
	for _, p := range doc.Pushed {
		kinds[p.Path] = p.Kind
	}
	want := map[string]contract.WriteKind{relX: contract.WriteCreate, relY: contract.WriteUpdate, relZ: contract.WriteCreate}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("pushed kinds = %v, want %v", kinds, want)
	}
}

func TestPush_ExpectAbsent409IsBothAdded(t *testing.T) {
	fx := p12Env(t)
	relX, dataX := fx.page(schema.TypeEntity, "x", "# X ours\n")
	theirs := []byte("---\nteammate: x\n---\n# X theirs \x1b[2J\n")
	seeded := fx.fake.Seed(relX, theirs)
	// The listing raced the teammate's create: it does not show X yet.
	fx.remote.listHook = func(pages []sync.PageMeta, err error) ([]sync.PageMeta, error) {
		out := []sync.PageMeta{}
		for _, p := range pages {
			if p.Path != relX {
				out = append(out, p)
			}
		}
		return out, err
	}
	shasBefore := fx.pageSHAs()

	doc, err := fx.push(PushOptions{})
	ce := wp01ContractError(t, err)
	if ce.Code != contract.CodeConflict {
		t.Fatalf("code = %q, want conflict", ce.Code)
	}
	p12AssertDoc(t, doc)
	p12AssertFiles(t, fx.lastFiles(), []sync.PushFile{
		{Path: relX, Content: string(p12Norm(t, dataX)), ExpectAbsent: true},
	})
	wantConflicts := []contract.PushConflict{{Path: relX, Kind: contract.ConflictBothAdded, ServerSHA: seeded}}
	if !reflect.DeepEqual(doc.Conflicts, wantConflicts) {
		t.Fatalf("conflicts = %+v, want %+v", doc.Conflicts, wantConflicts)
	}
	if len(doc.Pushed) != 0 {
		t.Fatalf("pushed = %+v, want none", doc.Pushed)
	}
	rec, ok := fx.conflicts().Get(relX)
	want := syncstate.ConflictRecord{
		Path: relX, ServerSHA: seeded, Kind: syncstate.KindBothAdded, Source: syncstate.SourcePush,
		DetectedAt: contract.TimeOf(p12Now), HasContent: true,
	}
	if !ok || !rec.DetectedAt.Equal(want.DetectedAt) {
		t.Fatalf("sidecar = %+v (ok %v), want %+v", rec, ok, want)
	}
	rec.DetectedAt = want.DetectedAt
	if rec != want {
		t.Fatalf("sidecar = %+v, want %+v", rec, want)
	}
	content, err := fx.conflicts().Content(relX)
	if err != nil || string(content) != schema.SanitizeContent(string(theirs)) {
		t.Fatalf("sidecar content = %q (%v), want SanitizeContent(theirs)", content, err)
	}
	if got := fx.pageSHAs(); !reflect.DeepEqual(got, shasBefore) {
		t.Fatalf("page_shas = %v, want unchanged %v", got, shasBefore)
	}
	if _, ok := fx.baseRecord(relX); ok {
		t.Fatal("a base was recorded for the conflicting create")
	}
}

func TestPush_ConflictPersistedStateNotBumped(t *testing.T) {
	fx := p12Env(t)
	relX, dataX := fx.page(schema.TypeConcept, "x", "# X\n")
	sb := fx.synced(relX, dataX)
	relY, dataY := fx.page(schema.TypeEntity, "y", "# Y\n")
	fx.synced(relY, dataY)
	fx.edit(relX, schema.TypeConcept, "# X\n\nOur edit.\n")
	dataY2 := fx.edit(relY, schema.TypeEntity, "# Y\n\nOur edit.\n")
	baseX := fx.baseBytes(relX)
	theirs := []byte("---\nslug: x\n---\n# X\n\nTeammate \x1b[31medit\x1b[0m ‮hidden​.\n")
	var srv string
	// A teammate pushes X right after our listing was fetched.
	fx.remote.listHook = func(pages []sync.PageMeta, err error) ([]sync.PageMeta, error) {
		srv = fx.fake.Seed(relX, theirs)
		return pages, err
	}

	doc, err := fx.push(PushOptions{})
	ce := wp01ContractError(t, err)
	if ce.Code != contract.CodeConflict || ce.HTTPStatus != 409 {
		t.Fatalf("error = {%q, %d}, want {conflict, 409}", ce.Code, ce.HTTPStatus)
	}
	if ce.Message != "push: 1 conflict(s) remain; other pages were pushed" {
		t.Fatalf("message = %q", ce.Message)
	}
	if want := []string{"ctx resolve " + relX + " --ours", "ctx resolve " + relX + " --theirs"}; !reflect.DeepEqual(ce.Advice, want) {
		t.Fatalf("advice = %#v, want %#v", ce.Advice, want)
	}
	if d, ok := ce.Details.(contract.ConflictDetails); !ok || !reflect.DeepEqual(d.Paths, []string{relX}) {
		t.Fatalf("details = %#v, want ConflictDetails{[%s]}", ce.Details, relX)
	}
	p12AssertDoc(t, doc)
	wantConflicts := []contract.PushConflict{{Path: relX, Kind: contract.ConflictBothModified, BaseSHA: sb, ServerSHA: srv}}
	if !reflect.DeepEqual(doc.Conflicts, wantConflicts) {
		t.Fatalf("conflicts = %+v, want %+v", doc.Conflicts, wantConflicts)
	}
	live := fx.listing()
	if want := []contract.PushedPage{{Path: relY, Kind: contract.WriteUpdate, SHA: live[relY]}}; !reflect.DeepEqual(doc.Pushed, want) {
		t.Fatalf("pushed = %+v, want %+v", doc.Pushed, want)
	}
	if doc.NewHead != fx.fake.Head() {
		t.Fatalf("new_head = %q, want %q", doc.NewHead, fx.fake.Head())
	}

	rec, ok := fx.conflicts().Get(relX)
	if !ok || rec.ServerSHA != srv || rec.BaseSHA != sb || rec.Kind != syncstate.KindBothModified ||
		rec.Source != syncstate.SourcePush || !rec.HasContent || !rec.DetectedAt.Equal(contract.TimeOf(p12Now)) {
		t.Fatalf("sidecar = %+v (ok %v), want server %s base %s both_modified push", rec, ok, srv, sb)
	}
	content, err := fx.conflicts().Content(relX)
	if err != nil {
		t.Fatal(err)
	}
	if want := schema.SanitizeContent(string(theirs)); string(content) != want || want == string(theirs) {
		t.Fatalf("sidecar content = %q, want SanitizeContent(current_content) %q", content, want)
	}
	shas := fx.pageSHAs()
	if shas[relX] != sb {
		t.Fatalf("page_shas[%s] = %q, want unchanged %q", relX, shas[relX], sb)
	}
	if shas[relY] != live[relY] {
		t.Fatalf("page_shas[%s] = %q, want %q", relY, shas[relY], live[relY])
	}
	if got := fx.baseBytes(relX); string(got) != string(baseX) {
		t.Fatalf("base of %s changed: %q", relX, got)
	}
	if rec, _ := fx.baseRecord(relX); rec.SHA != sb {
		t.Fatalf("bases.json[%s].sha = %q, want %q", relX, rec.SHA, sb)
	}
	if got := fx.baseBytes(relY); string(got) != string(p12Norm(t, dataY2)) {
		t.Fatalf("base of %s = %q", relY, got)
	}
}

func TestPush_SidecarBlocksPushOnline(t *testing.T) {
	fx := p12Env(t)
	relX, dataX := fx.page(schema.TypeEntity, "x", "# X\n")
	sb := fx.synced(relX, dataX)
	fx.edit(relX, schema.TypeEntity, "# X\n\nOur edit.\n")
	const sidecarSHA = "feedfacefeedfacefeedfacefeedfacefeedface"
	c := fx.conflicts()
	if err := c.Put(syncstate.ConflictRecord{
		Path: relX, ServerSHA: sidecarSHA, BaseSHA: sb, Kind: syncstate.KindBothModified,
		Source: syncstate.SourcePush, DetectedAt: p12Now,
	}, []byte("theirs\n")); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	want := []contract.SkippedPage{{Path: relX, State: contract.StateConflict, Reason: contract.SkipConflict, ServerSHA: sidecarSHA}}

	for _, o := range []PushOptions{{}, {Paths: []string{relX}}} {
		doc, err := fx.push(o)
		if err != nil {
			t.Fatalf("Push(%+v): %v", o, err)
		}
		p12AssertDoc(t, doc)
		p12AssertSkipped(t, doc.Skipped, want)
		if len(doc.Pushed) != 0 {
			t.Fatalf("pushed = %+v, want none", doc.Pushed)
		}
	}
	if got := fx.requests("PushPages"); got != 0 {
		t.Fatalf("PushPages calls = %d, want 0", got)
	}
}

func TestPush_SentBytesAreNormalizeNotSanitized(t *testing.T) {
	fx := p12Env(t)
	rel := "wiki/entities/tricky.md"
	page := p12Page(t, schema.TypeEntity, "tricky", "# Tricky\n\nesc \x1b[31mred\x1b[0m, bidi ‮revert‬, zero​width.\n")
	// BOM and CRLF: Normalize differs from the working file.
	local := append([]byte{0xEF, 0xBB, 0xBF}, []byte(strings.ReplaceAll(string(page), "\n", "\r\n"))...)
	p12WriteFile(t, fx.hub, rel, local)
	norm := p12Norm(t, local)
	if string(norm) == string(local) || !strings.Contains(string(norm), "\x1b[31m") ||
		!strings.Contains(string(norm), "‮") || !strings.Contains(string(norm), "​") {
		t.Fatalf("precondition: Normalize(local) = %q", norm)
	}

	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	p12AssertDoc(t, doc)
	p12AssertFiles(t, fx.lastFiles(), []sync.PushFile{{Path: rel, Content: string(norm), ExpectAbsent: true}})
	if got := fx.baseBytes(rel); string(got) != string(norm) {
		t.Fatalf("base = %q, want the sent bytes %q", got, norm)
	}
	if got, _ := fx.fake.Content(rel); string(got) != string(norm) {
		t.Fatalf("server content = %q, want %q", got, norm)
	}

	live := fx.listing()
	view, err := syncstate.LoadView(fx.hub, live)
	if err != nil {
		t.Fatal(err)
	}
	f := view.Facts[rel]
	res, err := contract.ClassifySides(contract.Side(f.Local), contract.Side(f.Base), contract.Side(f.Remote))
	if err != nil {
		t.Fatal(err)
	}
	if res.State != contract.StateUnchanged {
		t.Fatalf("state after push = %q (local %+v base %+v remote %+v), want unchanged", res.State, f.Local, f.Base, f.Remote)
	}
	doc, err = fx.push(PushOptions{})
	if err != nil || len(doc.Pushed) != 0 || fx.requests("PushPages") != 1 {
		t.Fatalf("second push = %+v, %v; PushPages %d, want nothing sent", doc, err, fx.requests("PushPages"))
	}
}

func TestPush_BlockingStatesNeverSent(t *testing.T) {
	fx := p12Env(t)
	// conflict: both sides moved.
	relC, dataC := fx.page(schema.TypeConcept, "c", "# C\n")
	fx.synced(relC, dataC)
	fx.edit(relC, schema.TypeConcept, "# C\n\nOurs.\n")
	srvC := fx.fake.Seed(relC, []byte("theirs c\n"))
	// invalid: does not parse.
	relI := "wiki/concepts/bad.md"
	p12WriteFile(t, fx.hub, relI, []byte("not a page\n"))
	// deleted_local.
	relD, dataD := fx.page(schema.TypeEntity, "d", "# D\n")
	fx.synced(relD, dataD)
	if err := os.Remove(filepath.Join(fx.hub, filepath.FromSlash(relD))); err != nil {
		t.Fatal(err)
	}
	// unverified: page_shas without a base record (the seam repairs nothing).
	relU, dataU := fx.page(schema.TypeAnalysis, "u", "# U\n")
	shaU := fx.fake.Seed(relU, p12Norm(t, dataU))
	fx.setPageSHA(relU, shaU)
	// incoming and unchanged are not listed without paths.
	relN, dataN := fx.page(schema.TypeEntity, "n", "# N\n")
	fx.synced(relN, dataN)
	fx.fake.Seed(relN, []byte("theirs n\n"))
	relS, dataS := fx.page(schema.TypeEntity, "s", "# S\n")
	fx.synced(relS, dataS)
	// The one page that goes out.
	relM, dataM := fx.page(schema.TypeEntity, "m", "# M\n")
	shaM := fx.synced(relM, dataM)
	dataM2 := fx.edit(relM, schema.TypeEntity, "# M\n\nEdited.\n")

	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	p12AssertDoc(t, doc)
	p12AssertFiles(t, fx.lastFiles(), []sync.PushFile{{Path: relM, Content: string(p12Norm(t, dataM2)), ParentSHA: shaM}})
	p12AssertSkipped(t, doc.Skipped, []contract.SkippedPage{
		{Path: relU, State: contract.StateUnverified, Reason: contract.SkipUnverified},
		{Path: relI, State: contract.StateInvalid, Reason: contract.SkipInvalid},
		{Path: relC, State: contract.StateConflict, Reason: contract.SkipConflict, ServerSHA: srvC},
		{Path: relD, State: contract.StateDeletedLocal, Reason: contract.SkipDeletedLocal},
	})
	_ = relS
}

func TestPush_PositionalPaths(t *testing.T) {
	fx := p12Env(t)
	relX, dataX := fx.page(schema.TypeConcept, "x", "# X\n")
	relI, dataI := fx.page(schema.TypeEntity, "inc", "# I\n")
	fx.synced(relI, dataI)
	shaI2 := fx.fake.Seed(relI, []byte("theirs inc\n"))
	relN := "wiki/entities/remote-only.md"
	shaN := fx.fake.Seed(relN, p12Page(t, schema.TypeEntity, "remote-only", "# R\n"))
	relU, dataU := fx.page(schema.TypeEntity, "same", "# U\n")
	fx.synced(relU, dataU)
	// A modified page that is not named stays home.
	relM, dataM := fx.page(schema.TypeEntity, "m", "# M\n")
	fx.synced(relM, dataM)
	fx.edit(relM, schema.TypeEntity, "# M\n\nEdited.\n")
	unknown := "wiki/concepts/nope.md"

	doc, err := fx.push(PushOptions{Paths: []string{
		relX, ".contexo/" + relX, filepath.Join(fx.hub, filepath.FromSlash(relX)),
		unknown, relI, relN, relU,
	}})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	p12AssertDoc(t, doc)
	p12AssertFiles(t, fx.lastFiles(), []sync.PushFile{{Path: relX, Content: string(p12Norm(t, dataX)), ExpectAbsent: true}})
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != relX || doc.Pushed[0].Kind != contract.WriteCreate {
		t.Fatalf("pushed = %+v, want only %s (create)", doc.Pushed, relX)
	}
	p12AssertSkipped(t, doc.Skipped, []contract.SkippedPage{
		{Path: unknown, Reason: contract.SkipNotFound},
		{Path: relI, State: contract.StateIncoming, Reason: contract.SkipIncoming, ServerSHA: shaI2},
		{Path: relN, State: contract.StateIncomingNew, Reason: contract.SkipIncoming, ServerSHA: shaN},
		{Path: relU, State: contract.StateUnchanged, Reason: contract.SkipUnchanged},
	})

	lists := fx.requests("ListPages")
	for _, bad := range []string{"../x.md", "wiki/concepts/x.txt", filepath.Join(t.TempDir(), "x.md")} {
		doc, err := fx.push(PushOptions{Paths: []string{relM, bad}})
		ce := wp01ContractError(t, err)
		if ce.Code != contract.CodeInvalidPath || doc != nil {
			t.Fatalf("Push(%q) = %+v, code %q; want invalid_path and no doc", bad, doc, ce.Code)
		}
	}
	if got := fx.requests("ListPages"); got != lists {
		t.Fatalf("ListPages calls went from %d to %d for invalid paths, want none", lists, got)
	}
	if got := fx.requests("PushPages"); got != 1 {
		t.Fatalf("PushPages calls = %d, want 1", got)
	}
}

func TestPush_FiltersApplyBeforeBatch(t *testing.T) {
	setup := func(t *testing.T) (*p12Fix, map[string][]byte) {
		fx := p12Env(t)
		pages := map[string][]byte{}
		rel, data := fx.page(schema.TypeConcept, "a", "# A\n", "Auth")
		pages[rel] = data
		rel, data = fx.page(schema.TypeEntity, "e", "# E\n", "billing")
		pages[rel] = data
		rel, data = fx.page(schema.TypeAnalysis, "z", "# Z\n")
		pages[rel] = data
		// A parse-invalid page has no frontmatter to match a filter.
		p12WriteFile(t, fx.hub, "wiki/concepts/bad.md", []byte("not a page\n"))
		// An unverified entity page: blocking, typed entity.
		relU, dataU := fx.page(schema.TypeEntity, "u", "# U\n")
		fx.setPageSHA(relU, fx.fake.Seed(relU, p12Norm(t, dataU)))
		return fx, pages
	}
	sent := func(fx *p12Fix) []string {
		var out []string
		for _, f := range fx.lastFiles() {
			out = append(out, f.Path)
		}
		return out
	}
	unverified := contract.SkippedPage{Path: "wiki/entities/u.md", State: contract.StateUnverified, Reason: contract.SkipUnverified}
	invalid := contract.SkippedPage{Path: "wiki/concepts/bad.md", State: contract.StateInvalid, Reason: contract.SkipInvalid}

	t.Run("type", func(t *testing.T) {
		fx, pages := setup(t)
		doc, err := fx.push(PushOptions{Type: "ENTITY"})
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		p12AssertFiles(t, fx.lastFiles(), []sync.PushFile{
			{Path: "wiki/entities/e.md", Content: string(p12Norm(t, pages["wiki/entities/e.md"])), ExpectAbsent: true},
		})
		p12AssertSkipped(t, doc.Skipped, []contract.SkippedPage{unverified})
	})
	t.Run("tag", func(t *testing.T) {
		fx, _ := setup(t)
		doc, err := fx.push(PushOptions{Tag: "auth"})
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		if got := sent(fx); !reflect.DeepEqual(got, []string{"wiki/concepts/a.md"}) {
			t.Fatalf("sent %v, want [wiki/concepts/a.md]", got)
		}
		p12AssertSkipped(t, doc.Skipped, nil)
	})
	t.Run("feature wins over tag", func(t *testing.T) {
		fx, _ := setup(t)
		if _, err := fx.push(PushOptions{Feature: " Billing ", Tag: "auth"}); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if got := sent(fx); !reflect.DeepEqual(got, []string{"wiki/entities/e.md"}) {
			t.Fatalf("sent %v, want [wiki/entities/e.md]", got)
		}
	})
	t.Run("no filter", func(t *testing.T) {
		fx, _ := setup(t)
		doc, err := fx.push(PushOptions{})
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		if got := sent(fx); !reflect.DeepEqual(got, []string{"wiki/analyses/z.md", "wiki/concepts/a.md", "wiki/entities/e.md"}) {
			t.Fatalf("sent %v", got)
		}
		p12AssertSkipped(t, doc.Skipped, []contract.SkippedPage{invalid, unverified})
	})
	t.Run("paths with a filter is usage", func(t *testing.T) {
		fx, _ := setup(t)
		for _, o := range []PushOptions{
			{Paths: []string{"wiki/entities/e.md"}, Type: "entity"},
			{Paths: []string{"wiki/entities/e.md"}, Tag: "billing"},
			{Paths: []string{"wiki/entities/e.md"}, Feature: "billing"},
		} {
			doc, err := fx.push(o)
			ce := wp01ContractError(t, err)
			if ce.Code != contract.CodeUsage || doc != nil {
				t.Fatalf("Push(%+v) = %+v, code %q; want usage", o, doc, ce.Code)
			}
		}
		if n := fx.requests("ListPages") + fx.requests("PushPages"); n != 0 {
			t.Fatalf("requests = %v, want none", fx.fake.Requests())
		}
	})
}

func TestPush_LockBeforeListAndEnsureBaseLocked(t *testing.T) {
	fx := p12Env(t)
	rel, data := fx.page(schema.TypeEntity, "e", "# E\n")
	sha := fx.synced(rel, data)
	fx.edit(rel, schema.TypeEntity, "# E\n\nEdited.\n")

	probe := func(hub string) bool {
		unlock, ok, err := syncstate.TryLock(hub, "probe")
		if err != nil {
			t.Errorf("TryLock: %v", err)
		}
		if ok {
			unlock()
		}
		return !ok
	}
	var listLocked []bool
	fx.remote.listHook = func(pages []sync.PageMeta, err error) ([]sync.PageMeta, error) {
		listLocked = append(listLocked, probe(fx.hub))
		return pages, err
	}
	type call struct {
		o      EnsureBaseOptions
		lists  int
		locked bool
	}
	var calls []call
	seamWarn := contract.Warning{Code: contract.WarnBaseRepairSkipped, Message: "seam", Paths: []string{rel}}
	pushEnsureBase = func(ctx context.Context, env Env, o EnsureBaseOptions) (*EnsureBaseResult, error) {
		calls = append(calls, call{o, fx.requests("ListPages"), probe(env.HubDir)})
		return &EnsureBaseResult{Warnings: []contract.Warning{seamWarn}}, nil
	}

	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !reflect.DeepEqual(listLocked, []bool{true}) {
		t.Fatalf("lock held during ListPages = %v, want [true]", listLocked)
	}
	if len(calls) != 1 {
		t.Fatalf("EnsureBase calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if !c.o.Locked || !c.locked || c.lists != 1 {
		t.Fatalf("EnsureBase call = {Locked %v, lock held %v, ListPages so far %d}, want {true, true, 1}", c.o.Locked, c.locked, c.lists)
	}
	if len(c.o.Listing) != 1 || c.o.Listing[0].Path != rel || c.o.Listing[0].SHA != sha {
		t.Fatalf("EnsureBase listing = %+v, want the live listing [%s@%s]", c.o.Listing, rel, sha)
	}
	if !reflect.DeepEqual(doc.Warnings, []contract.Warning{seamWarn}) {
		t.Fatalf("warnings = %+v, want the EnsureBase warning", doc.Warnings)
	}
	if len(doc.Pushed) != 1 {
		t.Fatalf("pushed = %+v", doc.Pushed)
	}
}

// R9: an EnsureBase failure is a warning, unless the context is done.
func TestPush_EnsureBaseErrorWarnsAndContinues(t *testing.T) {
	fx := p12Env(t)
	rel, _ := fx.page(schema.TypeEntity, "e", "# E\n")
	pushEnsureBase = func(ctx context.Context, env Env, o EnsureBaseOptions) (*EnsureBaseResult, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("repair exploded")
	}
	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(doc.Warnings) != 1 || doc.Warnings[0].Code != contract.WarnBaseRepairSkipped ||
		doc.Warnings[0].Paths == nil || !strings.Contains(doc.Warnings[0].Message, "repair exploded") {
		t.Fatalf("warnings = %+v, want one base_repair_skipped naming the error", doc.Warnings)
	}
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != rel {
		t.Fatalf("pushed = %+v", doc.Pushed)
	}

	fx.page(schema.TypeEntity, "f", "# F\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	doc, err = Push(ctx, fx.env, PushOptions{})
	if !errors.Is(err, context.Canceled) || doc != nil {
		t.Fatalf("Push with a cancelled context = %+v, %v; want context.Canceled", doc, err)
	}
	if got := fx.requests("PushPages"); got != 1 {
		t.Fatalf("PushPages calls = %d, want 1", got)
	}
}

func TestPush_ResolvedPreStepCallsResolveOurs(t *testing.T) {
	fx := p12Env(t)
	rel, data := fx.page(schema.TypeEntity, "e", "# E\n")
	fx.synced(rel, data)
	fx.edit(rel, schema.TypeEntity, "# E\n\nEdited.\n")

	type call struct {
		path     string
		strategy contract.ResolveStrategy
		lockFree bool
		lists    int
		pushes   int
	}
	var calls []call
	var answer func(p string) error
	p12OverrideResolve(t, func(ctx context.Context, env Env, o ResolveOptions) (*contract.ResolveDoc, error) {
		unlock, ok, err := syncstate.TryLock(env.HubDir, "probe")
		if err != nil {
			t.Errorf("TryLock: %v", err)
		}
		if ok {
			unlock()
		}
		calls = append(calls, call{o.Path, o.Strategy, ok, fx.requests("ListPages"), fx.requests("PushPages")})
		if err := answer(o.Path); err != nil {
			return nil, err
		}
		return &contract.ResolveDoc{Schema: contract.SchemaResolve, Path: o.Path, Strategy: o.Strategy, Warnings: []contract.Warning{}}, nil
	})
	notResolvable := func(p string, st contract.PageState, ptr bool) error {
		d := contract.NotResolvableDetails{Path: p, State: st, Strategy: contract.ResolveOurs}
		e := &contract.Error{Code: contract.CodeNotResolvable, Message: "not resolvable"}
		e.Details = d
		if ptr {
			e.Details = &d
		}
		return e
	}
	answer = func(p string) error {
		switch p {
		case "wiki/concepts/mod.md":
			return notResolvable(p, contract.StateModified, false)
		case "wiki/concepts/new.md":
			return notResolvable(p, contract.StateNew, true)
		case "wiki/concepts/same.md":
			return notResolvable(p, contract.StateUnchanged, false)
		}
		return nil
	}
	resolved := []string{"wiki/concepts/c.md", "wiki/concepts/mod.md", "wiki/concepts/new.md", "wiki/concepts/same.md"}
	doc, err := fx.push(PushOptions{Resolved: resolved})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	var want []call
	for _, p := range resolved {
		want = append(want, call{p, contract.ResolveOurs, true, 0, 0})
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("resolve calls = %+v\nwant %+v", calls, want)
	}
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != rel {
		t.Fatalf("pushed = %+v", doc.Pushed)
	}

	fx.edit(rel, schema.TypeEntity, "# E\n\nEdited again.\n")
	lists, pushes := fx.requests("ListPages"), fx.requests("PushPages")
	for name, abort := range map[string]error{
		"not_resolvable incoming": notResolvable("wiki/concepts/x.md", contract.StateIncoming, false),
		"not_resolvable conflict": notResolvable("wiki/concepts/x.md", contract.StateConflict, true),
		"stale":                   contract.New(contract.CodeStale, "stale"),
		"plain":                   errors.New("disk on fire"),
	} {
		answer = func(string) error { return abort }
		doc, err := fx.push(PushOptions{Resolved: []string{"wiki/concepts/x.md"}})
		if !errors.Is(err, abort) || doc != nil {
			t.Fatalf("%s: Push = %+v, %v; want the resolve error as is", name, doc, err)
		}
	}
	if fx.requests("ListPages") != lists || fx.requests("PushPages") != pushes {
		t.Fatalf("an aborted pre-step made requests: %v", fx.fake.Requests())
	}
}

// p12PageNotFound is the error WP-1.4's Resolve returns when its ReadPage
// finds no server page: a *contract.Error page_not_found over the client's
// 404, wrapped once more by the caller.
func p12PageNotFound(p string) error {
	e := contract.New(contract.CodePageNotFound, "resolve: page not found")
	e.Err = sync.NewHTTPError(404, []byte(`{"code":"page_not_found","error":"page not found"}`),
		sync.ErrPageNotFound.Error(), sync.ErrPageNotFound, false)
	return fmt.Errorf("resolve %s: %w", p, e)
}

// WP-1.4 relay: the real Resolve reads the page first, so a local new page
// the server lacks answers page_not_found. The pre-step tolerates that for
// a valid working file without a base, and the push sends the page with
// expect_absent; every other page_not_found still aborts with nothing sent.
func TestPush_ResolvedNewPageIsPushed(t *testing.T) {
	fx := p12Env(t)
	relN, dataN := fx.page(schema.TypeConcept, "fresh", "# Fresh\n")
	relB, dataB := fx.page(schema.TypeEntity, "based", "# B\n")
	fx.synced(relB, dataB)
	fx.edit(relB, schema.TypeEntity, "# B\n\nEdited.\n")
	relBad := "wiki/concepts/bad.md"
	p12WriteFile(t, fx.hub, relBad, []byte("not a page\n"))
	relMis := "wiki/concepts/mismatch.md"
	p12WriteFile(t, fx.hub, relMis, p12Page(t, schema.TypeConcept, "elsewhere", "# M\n"))
	relGhost := "wiki/concepts/ghost.md"

	var calls []string
	errs := map[string]error{}
	p12OverrideResolve(t, func(ctx context.Context, env Env, o ResolveOptions) (*contract.ResolveDoc, error) {
		calls = append(calls, o.Path)
		if o.Strategy != contract.ResolveOurs {
			t.Errorf("strategy = %q, want ours", o.Strategy)
		}
		errs[o.Path] = p12PageNotFound(o.Path)
		return nil, errs[o.Path]
	})

	doc, err := fx.push(PushOptions{Paths: []string{relN}, Resolved: []string{".contexo/" + relN}})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{relN}) {
		t.Fatalf("resolve calls = %v, want [%s] (the normalized path)", calls, relN)
	}
	p12AssertFiles(t, fx.lastFiles(), []sync.PushFile{{Path: relN, Content: string(p12Norm(t, dataN)), ExpectAbsent: true}})
	live := fx.listing()
	if want := []contract.PushedPage{{Path: relN, Kind: contract.WriteCreate, SHA: live[relN]}}; !reflect.DeepEqual(doc.Pushed, want) {
		t.Fatalf("pushed = %+v, want %+v", doc.Pushed, want)
	}

	lists, pushes := fx.requests("ListPages"), fx.requests("PushPages")
	for name, p := range map[string]string{
		"path with a base":         relB,
		"no local file":            relGhost,
		"parse-invalid local file": relBad,
		"path-mismatched file":     relMis,
	} {
		calls = nil
		doc, err := fx.push(PushOptions{Resolved: []string{p}})
		if doc != nil || err == nil || !errors.Is(err, errs[p]) {
			t.Fatalf("%s: Push = %+v, %v; want the page_not_found error as is", name, doc, err)
		}
		if ce := wp01ContractError(t, err); ce.Code != contract.CodePageNotFound {
			t.Fatalf("%s: code = %q", name, ce.Code)
		}
		if !reflect.DeepEqual(calls, []string{p}) {
			t.Fatalf("%s: resolve calls = %v", name, calls)
		}
	}
	calls = nil
	doc, err = fx.push(PushOptions{Resolved: []string{"../x.md"}})
	if ce := wp01ContractError(t, err); ce.Code != contract.CodeInvalidPath || doc != nil || len(calls) != 0 {
		t.Fatalf("Push(resolved ../x.md) = %+v, code %q, resolve calls %v; want invalid_path before any call", doc, ce.Code, calls)
	}
	if fx.requests("ListPages") != lists || fx.requests("PushPages") != pushes {
		t.Fatalf("an aborted pre-step made requests: %v", fx.fake.Requests())
	}
}

// A busy error from Resolve (a wrapped *syncstate.BusyError, no contract
// error in the chain) is push's own busy.
func TestPush_ResolvedBusyMapsToBusy(t *testing.T) {
	fx := p12Env(t)
	rel, _ := fx.page(schema.TypeEntity, "e", "# E\n")
	started := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	busy := &syncstate.BusyError{Holder: syncstate.LockInfo{PID: 4242, Host: "build-box", Command: "resolve", StartedAt: started}}
	p12OverrideResolve(t, func(ctx context.Context, env Env, o ResolveOptions) (*contract.ResolveDoc, error) {
		return nil, fmt.Errorf("resolve %s: %w", o.Path, busy)
	})

	doc, err := fx.push(PushOptions{Resolved: []string{rel}})
	ce := wp01ContractError(t, err)
	if ce.Code != contract.CodeBusy || doc != nil {
		t.Fatalf("Push = %+v, code %q; want busy", doc, ce.Code)
	}
	if ce.Message != "push: "+busy.Error() {
		t.Fatalf("message = %q", ce.Message)
	}
	d, ok := ce.Details.(contract.BusyDetails)
	if !ok || d.PID != 4242 || d.Host != "build-box" || d.Command != "resolve" || !d.StartedAt.Equal(started) ||
		d.StartedAt.Location() != time.UTC {
		t.Fatalf("details = %#v, want the holder's BusyDetails", ce.Details)
	}
	var got *syncstate.BusyError
	if !errors.As(err, &got) || got != busy {
		t.Fatalf("error does not wrap the *BusyError")
	}
	if n := fx.requests("ListPages") + fx.requests("PushPages"); n != 0 {
		t.Fatalf("requests = %v while resolve was busy, want none", fx.fake.Requests())
	}
}

// 402 is quota_exceeded with the server's details, and nothing is written.
func TestPush_QuotaExceededWritesNothing(t *testing.T) {
	fx := p12Env(t)
	fx.page(schema.TypeEntity, "e", "# E\n")
	before := p12Tree(t, fx.hub)
	fx.fake.FailNext("PushPages", sync.NewHTTPError(402,
		[]byte(`{"code":"quota_pages","error":"page quota exceeded","limit":50}`),
		"sync: push failed (402)", nil, false))

	doc, err := fx.push(PushOptions{})
	ce := wp01ContractError(t, err)
	if ce.Code != contract.CodeQuotaExceeded || doc != nil || ce.HTTPStatus != 402 {
		t.Fatalf("Push = %+v, {%q %d}; want quota_exceeded", doc, ce.Code, ce.HTTPStatus)
	}
	if d, ok := ce.Details.(contract.QuotaDetails); !ok || d.QuotaCode != "quota_pages" || d.Limit != 50 {
		t.Fatalf("details = %#v", ce.Details)
	}
	if after := p12Tree(t, fx.hub); !reflect.DeepEqual(before, after) {
		t.Fatalf("a refused send wrote files:\nbefore %v\nafter  %v", before, after)
	}
}

func TestPush_NetworkAfterSendWritesNothing(t *testing.T) {
	fx := p12Env(t)
	rel, data := fx.page(schema.TypeEntity, "e", "# E\n")
	fx.synced(rel, data)
	fx.edit(rel, schema.TypeEntity, "# E\n\nEdited.\n")
	fx.page(schema.TypeEntity, "f", "# F\n")
	before := p12Tree(t, fx.hub)
	fx.fake.FailNext("PushPages", &sync.NetworkError{Op: "push", Err: errors.New("connection reset by peer")})

	doc, err := fx.push(PushOptions{})
	ce := wp01ContractError(t, err)
	if ce.Code != contract.CodeNetwork || doc != nil {
		t.Fatalf("Push = %+v, code %q; want network and no doc", doc, ce.Code)
	}
	if !strings.HasPrefix(ce.Message, "push: ") {
		t.Fatalf("message = %q", ce.Message)
	}
	if got := fx.requests("PushPages"); got != 1 {
		t.Fatalf("PushPages calls = %d, want 1", got)
	}
	if after := p12Tree(t, fx.hub); !reflect.DeepEqual(before, after) {
		t.Fatalf("a failed send wrote files:\nbefore %v\nafter  %v", before, after)
	}
}

func TestPush_NoDistillBypasses(t *testing.T) {
	fx := p12Env(t)
	rel, _ := fx.page(schema.TypeConcept, "c", "# C\n")
	mtime := p12Now.Add(-time.Minute)
	p12WriteBuffer(t, fx.hub, "sess-1", 2, mtime)
	before := p12Tree(t, fx.hub)

	for _, dry := range []bool{false, true} {
		doc, err := fx.push(PushOptions{DryRun: dry})
		ce := wp01ContractError(t, err)
		if ce.Code != contract.CodeDistillRequired || doc != nil {
			t.Fatalf("dry %v: Push = %+v, code %q; want distill_required", dry, doc, ce.Code)
		}
		const msg = "push: 1 un-distilled agent session is pending; share these pages through your agent so the decision record is captured"
		if ce.Message != msg || len(ce.Advice) != 0 {
			t.Fatalf("dry %v: message %q advice %v", dry, ce.Message, ce.Advice)
		}
		d, ok := ce.Details.(*contract.DistillRequiredDetails)
		if !ok || !reflect.DeepEqual(d.Pages, []string{rel}) || d.AgentPrompt != p12Prompt(rel) {
			t.Fatalf("dry %v: details = %#v", dry, ce.Details)
		}
		p12AssertSessions(t, d.Sessions, []contract.CaptureSession{
			{SessionID: "sess-1", Turns: 2, UpdatedAt: contract.TimeOf(mtime), Fresh: true},
		})
	}
	if got := fx.requests("PushPages"); got != 0 {
		t.Fatalf("PushPages calls = %d, want 0", got)
	}
	if after := p12Tree(t, fx.hub); !reflect.DeepEqual(before, after) {
		t.Fatalf("a refused push wrote files:\nbefore %v\nafter  %v", before, after)
	}

	p12WriteBuffer(t, fx.hub, "sess-2", 1, p12Now.Add(-2*time.Minute))
	_, err := fx.push(PushOptions{})
	if ce := wp01ContractError(t, err); !strings.HasPrefix(ce.Message, "push: 2 un-distilled agent sessions are pending; ") {
		t.Fatalf("message = %q", ce.Message)
	}

	doc, err := fx.push(PushOptions{NoDistill: true})
	if err != nil {
		t.Fatalf("Push --no-distill: %v", err)
	}
	if doc.Distill != (contract.DistillCheck{Required: true, Bypassed: true}) {
		t.Fatalf("distill = %+v, want {true true}", doc.Distill)
	}
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != rel {
		t.Fatalf("pushed = %+v, want %s", doc.Pushed, rel)
	}
}

func TestPush_DistillDisableEnvBypasses(t *testing.T) {
	fx := p12Env(t)
	t.Setenv("CONTEXO_DISTILL_DISABLE", "1")
	rel, _ := fx.page(schema.TypeAnalysis, "a", "# A\n")
	p12WriteBuffer(t, fx.hub, "sess-1", 2, p12Now.Add(-time.Minute))
	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if doc.Distill != (contract.DistillCheck{Required: true, Bypassed: true}) {
		t.Fatalf("distill = %+v, want {true true}", doc.Distill)
	}
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != rel {
		t.Fatalf("pushed = %+v", doc.Pushed)
	}

	// Nothing to push: the guard still runs and reports the bypass.
	doc, err = fx.push(PushOptions{})
	if err != nil || doc.Distill != (contract.DistillCheck{Required: false, Bypassed: true}) {
		t.Fatalf("empty push = %+v, %v; want distill {false true}", doc, err)
	}
	t.Setenv("CONTEXO_DISTILL_DISABLE", "")
	doc, err = fx.push(PushOptions{NoDistill: true})
	if err != nil || doc.Distill != (contract.DistillCheck{Required: false, Bypassed: true}) {
		t.Fatalf("empty --no-distill push = %+v, %v; want distill {false true}", doc, err)
	}
}

func TestPush_DryRunWritesNothing(t *testing.T) {
	t.Run("synced project", func(t *testing.T) {
		fx := p12Env(t)
		relX, _ := fx.page(schema.TypeEntity, "x", "# X\n")
		relY, dataY := fx.page(schema.TypeEntity, "y", "# Y\n")
		fx.synced(relY, dataY)
		fx.edit(relY, schema.TypeEntity, "# Y\n\nEdited.\n")
		before := p12Tree(t, fx.hub)

		doc, err := fx.push(PushOptions{DryRun: true})
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		p12AssertDoc(t, doc)
		want := []contract.PushedPage{{Path: relX, Kind: contract.WriteCreate}, {Path: relY, Kind: contract.WriteUpdate}}
		if !doc.DryRun || !reflect.DeepEqual(doc.Pushed, want) || len(doc.Conflicts) != 0 || doc.NewHead != "" ||
			doc.Message != "ctx push (2 pages)" {
			t.Fatalf("doc = %+v, want dry_run with pushed %+v", doc, want)
		}
		if fx.requests("PushPages") != 0 || fx.requests("ListPages") != 1 || len(fx.ensure) != 0 {
			t.Fatalf("requests %v, EnsureBase calls %d; want only one ListPages", fx.fake.Requests(), len(fx.ensure))
		}
		if after := p12Tree(t, fx.hub); !reflect.DeepEqual(before, after) {
			t.Fatalf("dry run wrote files:\nbefore %v\nafter  %v", before, after)
		}
	})
	t.Run("fresh hub", func(t *testing.T) {
		fx := p12Env(t)
		fx.page(schema.TypeEntity, "x", "# X\n")
		before := p12Tree(t, fx.hub)
		doc, err := fx.push(PushOptions{DryRun: true, Message: "mine"})
		if err != nil || doc.Message != "mine" || len(doc.Pushed) != 1 {
			t.Fatalf("Push = %+v, %v", doc, err)
		}
		if after := p12Tree(t, fx.hub); !reflect.DeepEqual(before, after) {
			t.Fatalf("dry run wrote files:\nbefore %v\nafter  %v", before, after)
		}
	})
}

func TestPush_BusyReturnsBusy(t *testing.T) {
	fx := p12Env(t)
	fx.page(schema.TypeEntity, "x", "# X\n")
	t.Setenv("CONTEXO_LOCK_TIMEOUT", "50ms")
	unlock, err := syncstate.Lock(fx.hub, "test", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	doc, err := fx.push(PushOptions{})
	ce := wp01ContractError(t, err)
	if ce.Code != contract.CodeBusy || doc != nil {
		t.Fatalf("Push = %+v, code %q; want busy", doc, ce.Code)
	}
	d, ok := ce.Details.(contract.BusyDetails)
	if !ok || d.PID != os.Getpid() || d.Command != "test" || d.Host == "" ||
		d.StartedAt.Location() != time.UTC || d.StartedAt.Nanosecond() != 0 || d.StartedAt.IsZero() {
		t.Fatalf("details = %#v, want BusyDetails of this process", ce.Details)
	}
	var busy *syncstate.BusyError
	if !errors.As(err, &busy) || !strings.HasPrefix(ce.Message, "push: ") {
		t.Fatalf("error %q does not wrap the *BusyError", ce.Message)
	}
	if n := fx.requests("ListPages"); n != 0 {
		t.Fatalf("ListPages calls = %d while busy, want 0", n)
	}
}

func TestPush_FirstPushRepoMissing(t *testing.T) {
	for name, listErr := range map[string]error{
		"404 repo_not_found": sync.NewHTTPError(404, []byte(`{"code":"repo_not_found","error":"repo not found"}`),
			"sync: list pages: repo not found", sync.ErrRepoNotFound, false),
		"403 forbidden": sync.NewHTTPError(403, []byte(`{"error":"forbidden"}`),
			"sync: list pages failed (403): forbidden", nil, false),
	} {
		t.Run(name, func(t *testing.T) {
			fx := p12Env(t)
			relX, dataX := fx.page(schema.TypeEntity, "x", "# X\n")
			fx.fake.FailNext("ListPages", listErr)
			doc, err := fx.push(PushOptions{})
			if err != nil {
				t.Fatalf("Push: %v", err)
			}
			p12AssertFiles(t, fx.lastFiles(), []sync.PushFile{{Path: relX, Content: string(p12Norm(t, dataX)), ExpectAbsent: true}})
			if len(doc.Pushed) != 1 || doc.Pushed[0].Kind != contract.WriteCreate {
				t.Fatalf("pushed = %+v", doc.Pushed)
			}
			if len(fx.ensure) != 1 || fx.ensure[0].Listing == nil || len(fx.ensure[0].Listing) != 0 {
				t.Fatalf("EnsureBase listing = %#v, want empty non-nil", fx.ensure)
			}
		})
	}
	t.Run("other listing error", func(t *testing.T) {
		fx := p12Env(t)
		fx.page(schema.TypeEntity, "x", "# X\n")
		before := p12Tree(t, fx.hub)
		fx.fake.FailNext("ListPages", sync.NewHTTPError(500, []byte(`{"error":"boom"}`), "sync: list pages failed (500)", nil, false))
		doc, err := fx.push(PushOptions{})
		ce := wp01ContractError(t, err)
		if ce.Code != contract.CodeServerError || doc != nil || !strings.HasPrefix(ce.Message, "push: ") {
			t.Fatalf("Push = %+v, {%q %q}; want server_error", doc, ce.Code, ce.Message)
		}
		if fx.requests("PushPages") != 0 || len(fx.ensure) != 0 {
			t.Fatalf("requests %v, EnsureBase calls %d after a listing error", fx.fake.Requests(), len(fx.ensure))
		}
		if after := p12Tree(t, fx.hub); !reflect.DeepEqual(before, after) {
			t.Fatalf("a failed listing wrote files")
		}
	})
}

func TestPush_ConfirmHook(t *testing.T) {
	fx := p12Env(t)
	relX, dataX := fx.page(schema.TypeEntity, "x", "# X\n")
	relY, dataY := fx.page(schema.TypeEntity, "y", "# Y\n")
	sy := fx.synced(relY, dataY)
	dataY2 := fx.edit(relY, schema.TypeEntity, "# Y\n\nEdited.\n")
	wantPlan := []PushPlanFile{
		{Kind: contract.WriteCreate, File: sync.PushFile{Path: relX, Content: string(p12Norm(t, dataX)), ExpectAbsent: true}},
		{Kind: contract.WriteUpdate, File: sync.PushFile{Path: relY, Content: string(p12Norm(t, dataY2)), ParentSHA: sy}},
	}
	before := p12Tree(t, fx.hub)

	var plans [][]PushPlanFile
	var locked []bool
	confirm := func(answer bool, err error) func([]PushPlanFile) (bool, error) {
		return func(plan []PushPlanFile) (bool, error) {
			plans = append(plans, plan)
			unlock, ok, _ := syncstate.TryLock(fx.hub, "probe")
			if ok {
				unlock()
			}
			locked = append(locked, !ok)
			return answer, err
		}
	}

	doc, err := fx.push(PushOptions{Confirm: confirm(false, nil)})
	if !errors.Is(err, ErrPushDeclined) || doc != nil {
		t.Fatalf("declined Push = %+v, %v; want ErrPushDeclined", doc, err)
	}
	boom := errors.New("prompt failed")
	doc, err = fx.push(PushOptions{Confirm: confirm(true, boom)})
	if err != boom || doc != nil {
		t.Fatalf("Push with a Confirm error = %+v, %v; want that error unchanged", doc, err)
	}
	if fx.requests("PushPages") != 0 {
		t.Fatalf("PushPages calls = %d before confirmation", fx.requests("PushPages"))
	}
	if after := p12Tree(t, fx.hub); !reflect.DeepEqual(before, after) {
		t.Fatalf("an unconfirmed push wrote files")
	}
	doc, err = fx.push(PushOptions{DryRun: true, Confirm: confirm(true, nil)})
	if err != nil || !doc.DryRun || len(doc.Pushed) != 2 {
		t.Fatalf("confirmed dry run = %+v, %v", doc, err)
	}
	doc, err = fx.push(PushOptions{Confirm: confirm(true, nil)})
	if err != nil || len(doc.Pushed) != 2 {
		t.Fatalf("confirmed Push = %+v, %v", doc, err)
	}
	if len(plans) != 4 {
		t.Fatalf("Confirm calls = %d, want 4", len(plans))
	}
	for i, plan := range plans {
		if !reflect.DeepEqual(plan, wantPlan) {
			t.Fatalf("plan %d = %+v\nwant %+v", i, plan, wantPlan)
		}
		if !locked[i] {
			t.Fatalf("Confirm call %d ran without the lock", i)
		}
	}
	p12AssertFiles(t, fx.lastFiles(), []sync.PushFile{wantPlan[0].File, wantPlan[1].File})
}

func TestPush_RawHashPageSkippedInvalid(t *testing.T) {
	fx := p12Env(t)
	// A valid page whose Normalize has no fixed point (WP-0.4): ScanLocal
	// calls it valid with a raw-sha256 hash, but it cannot be sent.
	relX := "wiki/concepts/x.md"
	raw := []byte("---\nschema: ctx.page.v1\nslug: x\ntype: concept\nauthor: a\n" +
		"created: 2026-01-01T00:00:00Z\nreasoning_summary: |+\n  x\n" + strings.Repeat("\n", 100) + "---\n# X\n")
	p12WriteFile(t, fx.hub, relX, raw)
	scan, err := syncstate.ScanLocal(fx.hub)
	if err != nil {
		t.Fatal(err)
	}
	if f := scan.Files[relX]; f == nil || f.Invalid != nil || !strings.HasPrefix(f.Hash, "raw-sha256:") {
		t.Fatalf("precondition: scan of %s = %+v, want valid with a raw-sha256 hash", relX, f)
	}
	relE, dataE := fx.page(schema.TypeEntity, "e", "# E\n")
	want := []contract.SkippedPage{{Path: relX, State: contract.StateNew, Reason: contract.SkipInvalid}}

	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	p12AssertFiles(t, fx.lastFiles(), []sync.PushFile{{Path: relE, Content: string(p12Norm(t, dataE)), ExpectAbsent: true}})
	p12AssertSkipped(t, doc.Skipped, want)

	doc, err = fx.push(PushOptions{Paths: []string{relX}})
	if err != nil {
		t.Fatalf("Push(%s): %v", relX, err)
	}
	p12AssertSkipped(t, doc.Skipped, want)
	if got := fx.requests("PushPages"); got != 1 {
		t.Fatalf("PushPages calls = %d, want 1", got)
	}
}

// A successful push clears a sidecar that did not block it (its server sha
// is the base).
func TestPush_SuccessClearsSidecar(t *testing.T) {
	fx := p12Env(t)
	rel, data := fx.page(schema.TypeEntity, "x", "# X\n")
	sb := fx.synced(rel, data)
	fx.edit(rel, schema.TypeEntity, "# X\n\nEdited.\n")
	c := fx.conflicts()
	if err := c.Put(syncstate.ConflictRecord{
		Path: rel, ServerSHA: sb, BaseSHA: sb, Kind: syncstate.KindBothModified, Source: syncstate.SourcePull,
	}, []byte("old theirs\n")); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	doc, err := fx.push(PushOptions{})
	if err != nil || len(doc.Pushed) != 1 {
		t.Fatalf("Push = %+v, %v", doc, err)
	}
	if _, ok := fx.conflicts().Get(rel); ok {
		t.Fatal("the sidecar survived a successful push")
	}
	if _, err := os.Stat(syncstate.ConflictFile(fx.hub, rel)); !os.IsNotExist(err) {
		t.Fatalf("conflicts/%s still exists: %v", rel, err)
	}
}

// R11: a corrupt index is a warning; push continues.
func TestPush_CorruptIndexWarns(t *testing.T) {
	fx := p12Env(t)
	rel, data := fx.page(schema.TypeEntity, "x", "# X\n")
	p12WriteFile(t, fx.hub, ".sync/state.json", []byte("{not json"))
	p12WriteFile(t, fx.hub, ".sync/bases.json", []byte("[]"))
	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(doc.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want one state_unreadable", doc.Warnings)
	}
	w := doc.Warnings[0]
	if w.Code != contract.WarnStateUnreadable || w.Paths == nil || len(w.Paths) != 0 ||
		!strings.Contains(w.Message, "state.json") || !strings.Contains(w.Message, "bases.json") {
		t.Fatalf("warning = %+v", w)
	}
	p12AssertFiles(t, fx.lastFiles(), []sync.PushFile{{Path: rel, Content: string(p12Norm(t, data)), ExpectAbsent: true}})
	if shas := fx.pageSHAs(); shas[rel] == "" {
		t.Fatalf("page_shas = %v after push", shas)
	}
}

func TestPush_PreconditionErrors(t *testing.T) {
	fx := p12Env(t)
	offline := fx.env
	offline.Remote = nil
	doc, err := Push(context.Background(), offline, PushOptions{})
	if ce := wp01ContractError(t, err); ce.Code != contract.CodeInternal || doc != nil {
		t.Fatalf("offline Push = %+v, code %q; want internal", doc, ce.Code)
	}
	noHub := fx.env
	noHub.Root = t.TempDir()
	noHub.HubDir = filepath.Join(noHub.Root, ".contexo")
	doc, err = Push(context.Background(), noHub, PushOptions{})
	if ce := wp01ContractError(t, err); ce.Code != contract.CodeNotInitialized || doc != nil {
		t.Fatalf("Push without a hub = %+v, code %q; want not_initialized", doc, ce.Code)
	}
	if n := fx.requests("ListPages"); n != 0 {
		t.Fatalf("ListPages calls = %d, want 0", n)
	}
}
