package cli

// CLI tests for the safe `ctx pull [--json] [--full]` (WP-1.3). They run the
// real command in-process through Execute against a testkit server, so the
// exit codes, the stream rules and the ctx.pull.v1 document are pinned end
// to end. The engine itself is covered by internal/syncops/pull_test.go.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
)

// pl13RepoID is the repo every pl13 project and wire push uses. The testkit
// server creates it on the first push.
const pl13RepoID = "pl13-repo"

// pl13Server starts an in-process Contexo server (closed at cleanup).
func pl13Server(t *testing.T) *testkit.Server {
	t.Helper()
	return testkit.StartServer(t)
}

// pl13Project builds a project wired to srv and pl13RepoID, with project
// credentials carrying srv's key. testkit.NewProject isolates HOME. opts
// adjust the options before the project is built.
func pl13Project(t *testing.T, srv *testkit.Server, opts ...func(*testkit.ProjectOptions)) *testkit.Project {
	t.Helper()
	o := testkit.ProjectOptions{ServerURL: srv.URL, Key: srv.Key, RepoID: pl13RepoID}
	for _, fn := range opts {
		fn(&o)
	}
	return testkit.NewProject(t, o)
}

// pl13NoCreds is a pl13Project option: no credentials at all.
func pl13NoCreds(o *testkit.ProjectOptions) { o.NoCreds = true }

// pl13Run runs ctx in-process through Execute with "--root", root appended
// and the update check off, and returns the exit code and both streams. It
// restores the package-level rootDir that --root writes.
func pl13Run(t *testing.T, root string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	t.Setenv("CONTEXO_NO_UPDATE_CHECK", "1")
	prev := rootDir
	t.Cleanup(func() { rootDir = prev })
	full := append(append([]string{}, args...), "--root", root)
	var out, errb bytes.Buffer
	code = Execute(full, &out, &errb)
	return code, out.String(), errb.String()
}

// pl13Rel is the hub-relative path of the concept page slug.
func pl13Rel(slug string) string { return "wiki/concepts/" + slug + ".md" }

// pl13Page is a valid concept page for slug with body.
func pl13Page(slug, body string) []byte {
	return testkit.PageBytes(testkit.PageSpec{Slug: slug, Body: body})
}

// pl13Sanitized is what pull writes for server bytes b (E6).
func pl13Sanitized(b []byte) []byte { return []byte(schema.SanitizeContent(string(b))) }

// pl13Push pushes one file to pl13RepoID through the sync wire (not the push
// CLI) and returns the path's new sha. A conflict fails the test.
func pl13Push(t *testing.T, srv *testkit.Server, path, content, parent string) string {
	t.Helper()
	resp, err := sync.NewClient(srv.URL, srv.Key).PushPages(pl13RepoID, &sync.PushRequest{
		AuthorName:  "Pl13",
		AuthorEmail: "pl13@example.test",
		Message:     "pl13 push " + path,
		Files:       []sync.PushFile{{Path: path, Content: content, ParentSHA: parent}},
	})
	if err != nil {
		t.Fatalf("push %s: %v", path, err)
	}
	if len(resp.Conflicts) != 0 {
		t.Fatalf("push %s: conflicts %+v", path, resp.Conflicts)
	}
	if len(resp.Pushed) != 1 || resp.Pushed[0].Path != path || resp.Pushed[0].SHA == "" {
		t.Fatalf("push %s: unexpected response %+v", path, resp)
	}
	return resp.Pushed[0].SHA
}

// pl13DecodePull strictly decodes stdout as one ctx.pull.v1 document:
// unknown fields fail, nothing may follow the document, and written,
// adopted, skipped and warnings must be JSON arrays (never null or absent).
func pl13DecodePull(t *testing.T, stdout string) contract.PullDoc {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	var doc contract.PullDoc
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a strict ctx.pull.v1 document: %v\n%s", err, stdout)
	}
	if tok, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("trailing data after the document (token %v, err %v):\n%s", tok, err, stdout)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("stdout is not a JSON object: %v", err)
	}
	for _, k := range []string{"written", "adopted", "skipped", "warnings"} {
		v := bytes.TrimSpace(raw[k])
		if len(v) == 0 || v[0] != '[' {
			t.Errorf("%q must be a JSON array, got %q", k, v)
		}
	}
	return doc
}

// pl13State loads hub's .sync/state.json.
func pl13State(t *testing.T, hub string) *sync.State {
	t.Helper()
	st, err := sync.LoadState(hub)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	return st
}

// pl13PullJSON runs `ctx pull --json [extra...]` in p, requires exit 0 and an
// empty stderr, and returns the strictly decoded document.
func pl13PullJSON(t *testing.T, p *testkit.Project, extra ...string) contract.PullDoc {
	t.Helper()
	args := append([]string{"pull", "--json"}, extra...)
	code, stdout, stderr := pl13Run(t, p.Root, args...)
	if code != 0 {
		t.Fatalf("ctx %s: exit %d, want 0\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("ctx %s: stderr must be empty on success, got %q", strings.Join(args, " "), stderr)
	}
	return pl13DecodePull(t, stdout)
}

// pl13CheckHeader checks the document's schema, server_url and repo_id.
func pl13CheckHeader(t *testing.T, doc contract.PullDoc, srv *testkit.Server) {
	t.Helper()
	if doc.Schema != contract.SchemaPull {
		t.Errorf("schema = %q, want %q", doc.Schema, contract.SchemaPull)
	}
	if doc.ServerURL != srv.URL {
		t.Errorf("server_url = %q, want %q", doc.ServerURL, srv.URL)
	}
	if doc.RepoID != pl13RepoID {
		t.Errorf("repo_id = %q, want %q", doc.RepoID, pl13RepoID)
	}
}

// pl13Equal reports got != want with label.
func pl13Equal(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %+v, want %+v", label, got, want)
	}
}

// pl13HasWarning returns the first warning with code, or nil.
func pl13HasWarning(doc contract.PullDoc, code contract.WarningCode) *contract.Warning {
	for i := range doc.Warnings {
		if doc.Warnings[i].Code == code {
			return &doc.Warnings[i]
		}
	}
	return nil
}

func TestPullJSON_Shape(t *testing.T) {
	srv := pl13Server(t)
	p := pl13Project(t, srv)
	a, b, c, d := pl13Rel("a"), pl13Rel("b"), pl13Rel("c"), pl13Rel("d")
	shaA := pl13Push(t, srv, a, string(pl13Page("a", "alpha v1")), "")
	shaB := pl13Push(t, srv, b, string(pl13Page("b", "bravo v1")), "")
	shaC := pl13Push(t, srv, c, string(pl13Page("c", "charlie v1")), "")

	// Initial pull: every server page is new locally.
	first := pl13PullJSON(t, p)
	pl13CheckHeader(t, first, srv)
	pl13Equal(t, "initial written", first.Written, []contract.PulledPage{
		{Path: a, SHA: shaA, Kind: contract.WriteCreate},
		{Path: b, SHA: shaB, Kind: contract.WriteCreate},
		{Path: c, SHA: shaC, Kind: contract.WriteCreate},
	})
	pl13Equal(t, "initial adopted", first.Adopted, []contract.PulledPage{})
	pl13Equal(t, "initial skipped", first.Skipped, []contract.SkippedPage{})
	if first.Head != shaC {
		t.Errorf("initial head = %q, want %q (the last commit)", first.Head, shaC)
	}

	// A teammate moves b and adds d; locally b and c are edited.
	parentB := pl13State(t, p.HubDir).PageSHAs[b]
	if parentB != shaB {
		t.Fatalf("page_shas[%s] after the initial pull = %q, want %q", b, parentB, shaB)
	}
	shaB2 := pl13Push(t, srv, b, string(pl13Page("b", "bravo v2 from the server")), parentB)
	dBytes := pl13Page("d", "delta, new on the server")
	shaD := pl13Push(t, srv, d, string(dBytes), "")
	localB := pl13Page("b", "bravo edited locally")
	localC := pl13Page("c", "charlie edited locally")
	p.WriteRaw(b, localB)
	p.WriteRaw(c, localC)

	doc := pl13PullJSON(t, p)
	pl13CheckHeader(t, doc, srv)
	if doc.Head != shaD {
		t.Errorf("head = %q, want %q (d's push, the last commit)", doc.Head, shaD)
	}
	pl13Equal(t, "written", doc.Written, []contract.PulledPage{
		{Path: d, SHA: shaD, Kind: contract.WriteCreate},
	})
	pl13Equal(t, "adopted", doc.Adopted, []contract.PulledPage{})
	pl13Equal(t, "skipped", doc.Skipped, []contract.SkippedPage{
		{Path: b, State: contract.StateConflict, Reason: contract.SkipConflict, ServerSHA: shaB2},
		{Path: c, State: contract.StateModified, Reason: contract.SkipModified},
	})
	pl13Equal(t, "warnings", doc.Warnings, []contract.Warning{})
	if !doc.Reindexed {
		t.Error("reindexed = false after writing a page")
	}

	if got := p.Read(b); !bytes.Equal(got, localB) {
		t.Errorf("local edit of %s was overwritten:\n%s", b, got)
	}
	if got := p.Read(c); !bytes.Equal(got, localC) {
		t.Errorf("local edit of %s was overwritten:\n%s", c, got)
	}
	if got := p.Read(d); !bytes.Equal(got, pl13Sanitized(dBytes)) {
		t.Errorf("%s = %q, want the server bytes", d, got)
	}
}

func TestPullJSON_NotAuthenticated(t *testing.T) {
	srv := pl13Server(t)
	p := pl13Project(t, srv, pl13NoCreds)

	code, stdout, stderr := pl13Run(t, p.Root, "pull", "--json")
	if code != contract.ExitAuth {
		t.Errorf("exit = %d, want %d (stderr %q)", code, contract.ExitAuth, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout must be empty on failure, got %q", stdout)
	}
	if n := len(wp01NonEmptyLines(stderr)); n != 1 {
		t.Errorf("stderr must be exactly one envelope line, got %q", stderr)
	}
	if env := wp01Envelope(t, stderr); env.Error.Code != contract.CodeNotAuthenticated {
		t.Errorf("code = %q, want %q (message %q)", env.Error.Code, contract.CodeNotAuthenticated, env.Error.Message)
	}
}

func TestPullJSON_NotInitialized(t *testing.T) {
	wp01IsolateEnv(t)
	root := t.TempDir() // no .contexo

	code, stdout, stderr := pl13Run(t, root, "pull", "--json")
	if code != contract.ExitConfig {
		t.Errorf("exit = %d, want %d (stderr %q)", code, contract.ExitConfig, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout must be empty on failure, got %q", stdout)
	}
	if env := wp01Envelope(t, stderr); env.Error.Code != contract.CodeNotInitialized {
		t.Errorf("code = %q, want %q (message %q)", env.Error.Code, contract.CodeNotInitialized, env.Error.Message)
	}
	if _, err := os.Stat(filepath.Join(root, ".contexo")); !os.IsNotExist(err) {
		t.Errorf("pull in an uninitialised root created .contexo (stat err %v)", err)
	}
}

func TestPullJSON_BusyExit12(t *testing.T) {
	t.Setenv("CONTEXO_LOCK_TIMEOUT", "0")
	srv := pl13Server(t)
	p := pl13Project(t, srv)
	rel := pl13Rel("held")
	pl13Push(t, srv, rel, string(pl13Page("held", "server page")), "")

	unlock, err := syncstate.Lock(p.HubDir, "pl13-holder", 0)
	if err != nil {
		t.Fatalf("hold the lock: %v", err)
	}
	t.Cleanup(unlock)

	code, stdout, stderr := pl13Run(t, p.Root, "pull", "--json")
	if code != contract.ExitBusy {
		t.Errorf("exit = %d, want %d (stderr %q)", code, contract.ExitBusy, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout must be empty on failure, got %q", stdout)
	}
	env := wp01Envelope(t, stderr)
	if env.Error.Code != contract.CodeBusy {
		t.Fatalf("code = %q, want %q (message %q)", env.Error.Code, contract.CodeBusy, env.Error.Message)
	}
	details, ok := env.Error.Details.(map[string]any)
	if !ok || details["command"] != "pl13-holder" {
		t.Errorf("details = %#v, want command %q", env.Error.Details, "pl13-holder")
	}
	if _, err := os.Stat(filepath.Join(p.HubDir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
		t.Errorf("a busy pull wrote %s (stat err %v)", rel, err)
	}
	if _, ok, err := syncstate.TryLock(p.HubDir, "pl13-probe"); err != nil || ok {
		t.Errorf("TryLock after the busy pull = %v, %v; the holder's lock must stay in place", ok, err)
	}
}

func TestPullJSON_FullIsNoop(t *testing.T) {
	srv := pl13Server(t)
	p := pl13Project(t, srv)
	rel := pl13Rel("a")
	pl13Push(t, srv, rel, string(pl13Page("a", "alpha v1")), "")
	if doc := pl13PullJSON(t, p); len(doc.Written) != 1 {
		t.Fatalf("initial pull wrote %+v, want one page", doc.Written)
	}

	edited := pl13Page("a", "alpha edited locally")
	p.WriteRaw(rel, edited)

	code, stdout, stderr := pl13Run(t, p.Root, "pull", "--json", "--full")
	if code != 0 {
		t.Fatalf("pull --json --full: exit %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("pull --json --full: stderr must be empty, got %q", stderr)
	}
	if strings.Contains(stdout, "note:") {
		t.Errorf("pull --json --full printed the human note on stdout:\n%s", stdout)
	}
	full := pl13DecodePull(t, stdout)
	plain := pl13PullJSON(t, p)

	pl13Equal(t, "--full document", full, plain)
	pl13Equal(t, "--full written", full.Written, []contract.PulledPage{})
	pl13Equal(t, "--full adopted", full.Adopted, []contract.PulledPage{})
	pl13Equal(t, "--full skipped", full.Skipped, []contract.SkippedPage{
		{Path: rel, State: contract.StateModified, Reason: contract.SkipModified},
	})
	if got := p.Read(rel); !bytes.Equal(got, edited) {
		t.Errorf("local edit was overwritten:\n%s", got)
	}
}

func TestPullHuman_FullPrintsNote(t *testing.T) {
	srv := pl13Server(t)
	p := pl13Project(t, srv)
	pl13Push(t, srv, pl13Rel("a"), string(pl13Page("a", "alpha v1")), "")

	code, stdout, stderr := pl13Run(t, p.Root, "pull", "--full")
	if code != 0 {
		t.Fatalf("pull --full: exit %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("pull --full: stderr must be empty, got %q", stderr)
	}
	if !strings.Contains(stdout, "always reconciles") {
		t.Errorf("pull --full must note that pull always reconciles; stdout:\n%s", stdout)
	}
}

func TestPullJSON_ReindexFailureIsWarning(t *testing.T) {
	srv := pl13Server(t)
	p := pl13Project(t, srv)
	rel := pl13Rel("fresh")
	sha := pl13Push(t, srv, rel, string(pl13Page("fresh", "new on the server")), "")

	// index.md as a non-empty directory: regenerating it must fail.
	index := filepath.Join(p.HubDir, "index.md")
	if err := os.Remove(index); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(index, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(index, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	doc := pl13PullJSON(t, p)
	pl13Equal(t, "written", doc.Written, []contract.PulledPage{
		{Path: rel, SHA: sha, Kind: contract.WriteCreate},
	})
	if doc.Reindexed {
		t.Error("reindexed = true although index.md could not be regenerated")
	}
	w := pl13HasWarning(doc, contract.WarnReindexFailed)
	if w == nil {
		t.Fatalf("warnings %+v lack %q", doc.Warnings, contract.WarnReindexFailed)
	}
	if w.Message == "" {
		t.Error("reindex_failed warning has an empty message")
	}
}

func TestPullHuman_SummaryAndAdvice(t *testing.T) {
	srv := pl13Server(t)
	p := pl13Project(t, srv)
	a, b, d := pl13Rel("a"), pl13Rel("b"), pl13Rel("d")
	pl13Push(t, srv, a, string(pl13Page("a", "alpha v1")), "")
	pl13Push(t, srv, b, string(pl13Page("b", "bravo v1")), "")

	if code, stdout, stderr := pl13Run(t, p.Root, "pull"); code != 0 {
		t.Fatalf("initial pull: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	// Server: b moves, d is new. Local: a edited only (modified), b edited
	// on both sides (conflict).
	parentB := pl13State(t, p.HubDir).PageSHAs[b]
	pl13Push(t, srv, b, string(pl13Page("b", "bravo v2 from the server")), parentB)
	pl13Push(t, srv, d, string(pl13Page("d", "delta, new on the server")), "")
	localA := pl13Page("a", "alpha edited locally")
	localB := pl13Page("b", "bravo edited locally")
	p.WriteRaw(a, localA)
	p.WriteRaw(b, localB)

	code, stdout, stderr := pl13Run(t, p.Root, "pull")
	if code != 0 {
		t.Fatalf("pull: exit %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("pull: stderr must be empty, got %q", stderr)
	}
	for _, want := range []string{
		"Pulled 1 page(s)",
		"kept your local edits; run ctx push",
		"ctx resolve " + b + " --theirs|--ours",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if got := p.Read(a); !bytes.Equal(got, localA) {
		t.Errorf("local edit of %s was overwritten:\n%s", a, got)
	}
	if got := p.Read(b); !bytes.Equal(got, localB) {
		t.Errorf("local edit of %s was overwritten:\n%s", b, got)
	}
}

func TestPullHuman_AlreadyUpToDate(t *testing.T) {
	srv := pl13Server(t)
	p := pl13Project(t, srv)
	pl13Push(t, srv, pl13Rel("a"), string(pl13Page("a", "alpha v1")), "")

	code, stdout, stderr := pl13Run(t, p.Root, "pull")
	if code != 0 {
		t.Fatalf("first pull: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	// The safe pull's summary line (Task 5) reports adoptions too.
	if !strings.Contains(stdout, "Pulled 1 page(s), adopted 0; HEAD=") {
		t.Errorf("first pull summary = %q, want \"Pulled 1 page(s), adopted 0; HEAD=...\"", stdout)
	}

	code, stdout, stderr = pl13Run(t, p.Root, "pull")
	if code != 0 {
		t.Fatalf("second pull: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("second pull: stderr must be empty, got %q", stderr)
	}
	if !strings.Contains(stdout, "Already up to date") || strings.Contains(stdout, "Pulled") {
		t.Errorf("second pull stdout = %q, want \"Already up to date\"", stdout)
	}
}

func TestPullCapabilityRegistered(t *testing.T) {
	for _, c := range advertisedCapabilities() {
		if c == contract.CapPullJSON {
			return
		}
	}
	t.Errorf("advertisedCapabilities() = %q lacks %q", advertisedCapabilities(), contract.CapPullJSON)
}
