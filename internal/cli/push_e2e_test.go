package cli

// WP-1.2 Task D: `ctx push --json` end to end, in-process through Execute
// (the binary's entry point) against a real testkit server. Every project
// uses project credentials with the server's legacy key; a second user is a
// second testkit.NewProject on the same repo.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	gosync "sync"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
)

// p12RecProxy is a reverse proxy in front of a server. It records every
// request as "METHOD /path" and strictly decodes the body of every
// POST .../sync/push, so a test sees exactly which files a push sent.
type p12RecProxy struct {
	URL string

	mu     gosync.Mutex
	hits   []string
	pushes []sync.PushRequest
}

func p12StartRecProxy(t *testing.T, target string) *p12RecProxy {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	px := &p12RecProxy{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var push *sync.PushRequest
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sync/push") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.DisallowUnknownFields()
			var req sync.PushRequest
			if err := dec.Decode(&req); err != nil {
				t.Errorf("push body is not a strict PushRequest: %v\n%s", err, body)
			} else {
				push = &req
			}
		}
		px.mu.Lock()
		px.hits = append(px.hits, r.Method+" "+r.URL.Path)
		if push != nil {
			px.pushes = append(px.pushes, *push)
		}
		px.mu.Unlock()
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	px.URL = ts.URL
	return px
}

// Hits returns the recorded requests.
func (px *p12RecProxy) Hits() []string {
	px.mu.Lock()
	defer px.mu.Unlock()
	return append([]string{}, px.hits...)
}

// Pushes returns the decoded push request bodies, oldest first.
func (px *p12RecProxy) Pushes() []sync.PushRequest {
	px.mu.Lock()
	defer px.mu.Unlock()
	return append([]sync.PushRequest{}, px.pushes...)
}

// Reset forgets the recorded requests and push bodies.
func (px *p12RecProxy) Reset() {
	px.mu.Lock()
	px.hits, px.pushes = nil, nil
	px.mu.Unlock()
}

// p12Exec runs ctx in-process through Execute and returns the exit code and
// both streams. --root writes the package-level rootDir, which NewRootCmd
// only resets, so it is restored at cleanup.
func p12Exec(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	prev := rootDir
	t.Cleanup(func() { rootDir = prev })
	var out, errb bytes.Buffer
	code = Execute(args, &out, &errb)
	return code, out.String(), errb.String()
}

// p12E2EPush runs `push --json --root <root>` plus extra in-process, checks
// the exit code and returns the strictly decoded document and both raw
// streams.
func p12E2EPush(t *testing.T, p *testkit.Project, wantExit int, extra ...string) (doc contract.PushDoc, stdout, stderr string) {
	t.Helper()
	args := append([]string{"push", "--json", "--root", p.Root}, extra...)
	code, stdout, stderr := p12Exec(t, args...)
	if code != wantExit {
		t.Fatalf("push %q: exit %d, want %d\nstdout: %s\nstderr: %s", extra, code, wantExit, stdout, stderr)
	}
	return p12DecodePushDoc(t, stdout), stdout, stderr
}

// p12Clone makes bob's hub a clone of alice's for rel: the page file and
// alice's whole .sync tree (state, bases, base files), as if bob had
// pulled it. The lock files are left out.
func p12Clone(t *testing.T, alice, bob *testkit.Project, rel string) {
	t.Helper()
	bob.WriteRaw(rel, alice.Read(rel))
	src := syncstate.SyncDir(alice.HubDir)
	dst := syncstate.SyncDir(bob.HubDir)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		sub, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, sub), 0o755)
		}
		if sub == "lock" || sub == "lock.guard" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, sub), b, 0o644)
	})
	if err != nil {
		t.Fatalf("clone .sync: %v", err)
	}
}

// p12PageSHA returns state.json's page_shas[rel] ("" when absent).
func p12PageSHA(t *testing.T, hub, rel string) string {
	t.Helper()
	st, err := sync.LoadState(hub)
	if err != nil {
		t.Fatal(err)
	}
	return st.PageSHAs[rel]
}

// p12Sidecar strictly reads conflicts.json and returns rel's raw record
// (nil when absent) and the bytes of .sync/conflicts/<rel> (nil when
// absent).
func p12Sidecar(t *testing.T, hub, rel string) (map[string]any, []byte) {
	t.Helper()
	data, err := os.ReadFile(syncstate.ConflictsPath(hub))
	if err != nil {
		t.Fatalf("conflicts.json: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var idx struct {
		Schema    string                    `json:"schema"`
		Conflicts map[string]map[string]any `json:"conflicts"`
	}
	if err := dec.Decode(&idx); err != nil {
		t.Fatalf("conflicts.json: %v\n%s", err, data)
	}
	if idx.Schema != syncstate.SchemaConflicts {
		t.Errorf("conflicts.json schema = %q, want %q", idx.Schema, syncstate.SchemaConflicts)
	}
	content, err := os.ReadFile(syncstate.ConflictFile(hub, rel))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return idx.Conflicts[rel], content
}

// p12WholeSecondUTC matches a contract time on the wire.
var p12WholeSecondUTC = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)

// p12CheckSidecar asserts rel's push sidecar: the record fields (base_sha
// omitted when baseSHA is "") and content == SanitizeContent(server bytes).
func p12CheckSidecar(t *testing.T, hub, rel, kind, serverSHA, baseSHA string, server []byte) {
	t.Helper()
	rec, content := p12Sidecar(t, hub, rel)
	if rec == nil {
		t.Fatalf("conflicts.json has no record for %s", rel)
	}
	want := map[string]any{"kind": kind, "source": "push", "server_sha": serverSHA, "has_content": true}
	if baseSHA != "" {
		want["base_sha"] = baseSHA
	}
	detected, _ := rec["detected_at"].(string)
	if !p12WholeSecondUTC.MatchString(detected) {
		t.Errorf("sidecar detected_at = %v, want a whole-second UTC time", rec["detected_at"])
	}
	delete(rec, "detected_at")
	if !reflect.DeepEqual(rec, want) {
		t.Errorf("sidecar record = %v, want %v (+ detected_at)", rec, want)
	}
	if wantContent := []byte(schema.SanitizeContent(string(server))); !bytes.Equal(content, wantContent) {
		t.Errorf("sidecar content = %q, want the teammate's server content %q", content, wantContent)
	}
}

// p12NoPushRequest fails when the recorded requests include a push.
func p12NoPushRequest(t *testing.T, px *p12RecProxy) {
	t.Helper()
	for _, h := range px.Hits() {
		if strings.HasSuffix(h, "/sync/push") {
			t.Errorf("a push request was sent: %q", px.Hits())
			return
		}
	}
}

// p12BinPush runs `ctx push --json --root root` with the built binary and
// returns its exit code and streams; err is a failure to run it at all. It
// never touches t, so a proxy hook on another goroutine may call it.
func p12BinPush(bin, root string) (code int, stdout, stderr string, err error) {
	cmd := exec.Command(bin, "push", "--json", "--root", root)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CONTEXO_NO_UPDATE_CHECK=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return 0, "", "", err
		}
		code = ee.ExitCode()
	}
	return code, out.String(), errb.String(), nil
}

func TestPushE2E_PartialEditThenNoopPush(t *testing.T) {
	p12Env(t)
	srv := testkit.StartServer(t)
	px := p12StartRecProxy(t, srv.URL)
	p := p12Project(t, px.URL, srv.Key)
	a := p.WritePage(p12Entity("p12-a", "v1 of a"))
	b := p.WritePage(p12Entity("p12-b", "v1 of b"))

	// First push: both pages are new, so both go out as guarded creates.
	doc, _, _ := p12E2EPush(t, p, 0)
	pushes := px.Pushes()
	if len(pushes) != 1 || len(pushes[0].Files) != 2 {
		t.Fatalf("first push requests = %+v, want one request with 2 files", pushes)
	}
	for _, f := range pushes[0].Files {
		if !f.ExpectAbsent || f.ParentSHA != "" {
			t.Errorf("first push file %s: expect_absent %v parent %q, want a guarded create", f.Path, f.ExpectAbsent, f.ParentSHA)
		}
	}
	shaA := p12PushedSHA(t, doc, a)
	p12PushedSHA(t, doc, b)
	if len(doc.Conflicts) != 0 || len(doc.Skipped) != 0 {
		t.Errorf("first push conflicts %+v skipped %+v, want none", doc.Conflicts, doc.Skipped)
	}

	// Edit one page: only that page is sent, as an update on its base.
	p.WritePage(p12Entity("p12-a", "v2 of a"))
	px.Reset()
	doc, _, _ = p12E2EPush(t, p, 0)
	pushes = px.Pushes()
	if len(pushes) != 1 || len(pushes[0].Files) != 1 {
		t.Fatalf("second push requests = %+v, want one request with 1 file", pushes)
	}
	want, err := syncstate.Normalize(p.Read(a))
	if err != nil {
		t.Fatal(err)
	}
	wantFile := sync.PushFile{Path: a, Content: string(want), ParentSHA: shaA}
	if got := pushes[0].Files[0]; got != wantFile {
		t.Errorf("second push sent %+v, want %+v", got, wantFile)
	}
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != a || doc.Pushed[0].Kind != contract.WriteUpdate {
		t.Errorf("second push pushed = %+v, want only %s (update)", doc.Pushed, a)
	}
	newA := p12PushedSHA(t, doc, a)
	if got := p12PageSHA(t, p.HubDir, a); got != newA {
		t.Errorf("page_shas[%s] = %q, want the new sha %q", a, got, newA)
	}

	// Nothing changed since: no push request at all, nothing pushed.
	head := p12Head(t, srv)
	px.Reset()
	doc, _, _ = p12E2EPush(t, p, 0)
	p12NoPushRequest(t, px)
	if len(doc.Pushed) != 0 || len(doc.Conflicts) != 0 || len(doc.Skipped) != 0 || doc.NewHead != "" {
		t.Errorf("no-op push doc = %+v, want nothing pushed, no conflicts, no new_head", doc)
	}
	if got := p12Head(t, srv); got != head {
		t.Errorf("no-op push committed: HEAD %s -> %s", head, got)
	}
}

func TestPushE2E_TeammateConflict(t *testing.T) {
	p12Env(t)
	bin := wp01BuildCtx(t)
	srv := testkit.StartServer(t)
	rec := p12StartRecProxy(t, srv.URL)
	hook := p12StartProxy(t, rec.URL)
	alice := p12Project(t, hook.URL, srv.Key)
	x := alice.WritePage(p12Entity("p12-shared", "v1"))
	doc, _, _ := p12E2EPush(t, alice, 0)
	aliceSHA := p12PushedSHA(t, doc, x)
	aliceBase, err := os.ReadFile(syncstate.BaseFile(alice.HubDir, x))
	if err != nil {
		t.Fatal(err)
	}

	// bob clones alice's project and edits x; alice edits x too.
	bob := p12Project(t, srv.URL, srv.Key)
	p12Clone(t, alice, bob, x)
	bob.WritePage(p12Entity("p12-shared", "bob's edit"))
	alice.WritePage(p12Entity("p12-shared", "alice's edit"))

	// bob's ctx push lands right after alice's listing was fetched: her
	// listing still shows x at her base, so she sends her edit on that base
	// and the server answers 409. (Had bob's commit been in her listing, x
	// would classify as conflict and never be sent: the last step.)
	var (
		mu                   gosync.Mutex
		bobCode              int
		bobStdout, bobStderr string
		bobRunErr            error
	)
	hook.AfterList(func() {
		code, stdout, stderr, err := p12BinPush(bin, bob.Root)
		mu.Lock()
		bobCode, bobStdout, bobStderr, bobRunErr = code, stdout, stderr, err
		mu.Unlock()
	})
	rec.Reset()
	code, stdout, stderr := p12Exec(t, "push", "--json", "--root", alice.Root)
	mu.Lock()
	if bobRunErr != nil || bobCode != 0 {
		t.Fatalf("bob's push: exit %d, %v\nstdout: %s\nstderr: %s", bobCode, bobRunErr, bobStdout, bobStderr)
	}
	bobDoc := p12DecodePushDoc(t, bobStdout)
	mu.Unlock()
	if len(bobDoc.Pushed) != 1 || bobDoc.Pushed[0].Kind != contract.WriteUpdate {
		t.Fatalf("bob's push = %+v, want one update of %s", bobDoc.Pushed, x)
	}
	bobSHA := p12PushedSHA(t, bobDoc, x)
	bobServer, _, err := srv.Store.Read(p12RepoID, x)
	if err != nil {
		t.Fatal(err)
	}

	// alice: exit 6 with the document and the envelope; the sidecar holds
	// bob's version; her page_shas and base do not move.
	if code != 6 {
		t.Fatalf("alice's push: exit %d, want 6\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	doc = p12DecodePushDoc(t, stdout)
	wantConflicts := []contract.PushConflict{{Path: x, Kind: contract.ConflictBothModified, BaseSHA: aliceSHA, ServerSHA: bobSHA}}
	if !reflect.DeepEqual(doc.Conflicts, wantConflicts) {
		t.Errorf("conflicts = %+v, want %+v", doc.Conflicts, wantConflicts)
	}
	if len(doc.Pushed) != 0 {
		t.Errorf("pushed = %+v, want []", doc.Pushed)
	}
	if pushes := rec.Pushes(); len(pushes) != 1 || len(pushes[0].Files) != 1 ||
		pushes[0].Files[0].ParentSHA != aliceSHA || pushes[0].Files[0].ExpectAbsent {
		t.Errorf("alice's push requests = %+v, want one update of %s on parent %s", pushes, x, aliceSHA)
	}
	env := wp01Envelope(t, stderr)
	if env.Error.Code != contract.CodeConflict || env.ExitCode != 6 {
		t.Errorf("envelope code %q exit %d, want conflict / 6", env.Error.Code, env.ExitCode)
	}
	var d contract.ConflictDetails
	p12EnvelopeDetails(t, stderr, &d)
	if !reflect.DeepEqual(d.Paths, []string{x}) {
		t.Errorf("details.paths = %q, want [%s]", d.Paths, x)
	}
	p12CheckSidecar(t, alice.HubDir, x, syncstate.KindBothModified, bobSHA, aliceSHA, bobServer)
	if got := p12PageSHA(t, alice.HubDir, x); got != aliceSHA {
		t.Errorf("alice page_shas[%s] = %q, want it unchanged at %q", x, got, aliceSHA)
	}
	if got, err := os.ReadFile(syncstate.BaseFile(alice.HubDir, x)); err != nil || !bytes.Equal(got, aliceBase) {
		t.Errorf("alice's base file changed: %q, %v", got, err)
	}
	if got, _, err := srv.Store.Read(p12RepoID, x); err != nil || !bytes.Equal(got, bobServer) {
		t.Errorf("server %s = %q, %v; want bob's version kept", x, got, err)
	}

	// Pushing again sends nothing for x until it is resolved.
	rec.Reset()
	doc, _, _ = p12E2EPush(t, alice, 0)
	p12NoPushRequest(t, rec)
	wantSkipped := []contract.SkippedPage{{Path: x, State: contract.StateConflict, Reason: contract.SkipConflict, ServerSHA: bobSHA}}
	if !reflect.DeepEqual(doc.Skipped, wantSkipped) || len(doc.Pushed) != 0 || len(doc.Conflicts) != 0 {
		t.Errorf("second push pushed %+v conflicts %+v skipped %+v, want only skipped %+v",
			doc.Pushed, doc.Conflicts, doc.Skipped, wantSkipped)
	}
	if got := p12PageSHA(t, alice.HubDir, x); got != aliceSHA {
		t.Errorf("alice page_shas[%s] = %q after the second push, want %q", x, got, aliceSHA)
	}
}

func TestPushE2E_SameSlugCreateRace(t *testing.T) {
	p12Env(t)
	srv := testkit.StartServer(t)
	rec := p12StartRecProxy(t, srv.URL)
	hook := p12StartProxy(t, rec.URL)
	alice := p12Project(t, hook.URL, srv.Key)
	// The repo exists and alice has synced, so her next listing is real.
	seed := alice.WritePage(p12Entity("p12-seed", "seed"))
	doc, _, _ := p12E2EPush(t, alice, 0)
	p12PushedSHA(t, doc, seed)

	// alice and bob both create x. bob's create lands right after alice's
	// listing was fetched (which therefore lacks x) and before her push.
	x := alice.WritePage(p12Entity("p12-race", "alice's x"))
	bobContent := testkit.PageBytes(p12Entity("p12-race", "bob's x"))
	var mu gosync.Mutex
	var bobSHA string
	hook.AfterList(func() {
		resp, err := sync.NewClient(srv.URL, srv.Key).PushPages(p12RepoID, &sync.PushRequest{
			AuthorName: "bob", AuthorEmail: "bob@example.test", Message: "bob creates x",
			Files: []sync.PushFile{{Path: x, Content: string(bobContent), ExpectAbsent: true}},
		})
		if err != nil || len(resp.Pushed) != 1 || len(resp.Conflicts) != 0 {
			t.Errorf("bob's create: %+v, %v", resp, err)
			return
		}
		mu.Lock()
		bobSHA = resp.Pushed[0].SHA
		mu.Unlock()
	})
	rec.Reset()
	doc, stdout, stderr := p12E2EPush(t, alice, 6)
	mu.Lock()
	wantSHA := bobSHA
	mu.Unlock()
	if wantSHA == "" {
		t.Fatal("bob's create did not run before alice's push")
	}

	pushes := rec.Pushes()
	if len(pushes) != 1 || len(pushes[0].Files) != 1 ||
		pushes[0].Files[0].Path != x || !pushes[0].Files[0].ExpectAbsent || pushes[0].Files[0].ParentSHA != "" {
		t.Errorf("alice's push requests = %+v, want one guarded create of %s", pushes, x)
	}
	wantConflicts := []contract.PushConflict{{Path: x, Kind: contract.ConflictBothAdded, ServerSHA: wantSHA}}
	if !reflect.DeepEqual(doc.Conflicts, wantConflicts) {
		t.Errorf("conflicts = %+v, want %+v (no base_sha)", doc.Conflicts, wantConflicts)
	}
	if len(doc.Pushed) != 0 {
		t.Errorf("pushed = %+v, want []", doc.Pushed)
	}
	if strings.Contains(stdout, `"base_sha"`) {
		t.Errorf("both_added must omit base_sha: %s", stdout)
	}
	env := wp01Envelope(t, stderr)
	if env.Error.Code != contract.CodeConflict {
		t.Errorf("envelope code = %q, want conflict", env.Error.Code)
	}

	got, sha, err := srv.Store.Read(p12RepoID, x)
	if err != nil || !bytes.Equal(got, bobContent) || sha != wantSHA {
		t.Errorf("server %s = %q @ %s, %v; want bob's content @ %s", x, got, sha, err, wantSHA)
	}
	p12CheckSidecar(t, alice.HubDir, x, syncstate.KindBothAdded, wantSHA, "", bobContent)
	if s := p12PageSHA(t, alice.HubDir, x); s != "" {
		t.Errorf("alice page_shas[%s] = %q, want no entry", x, s)
	}
	if _, err := os.Stat(syncstate.BaseFile(alice.HubDir, x)); !os.IsNotExist(err) {
		t.Errorf("alice has a base for %s after a refused create: %v", x, err)
	}
}
