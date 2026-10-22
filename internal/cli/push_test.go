package cli

// WP-1.2 `ctx push` CLI tests. Most run the built binary against an
// in-process testkit server, so the process exit codes and the real
// stdout/stderr routing of cmd/ctx/main.go are what is asserted.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
	"github.com/sugihAF/contexo/internal/ui"
)

// p12RepoID is the repo every push test project points at.
const p12RepoID = "p12-repo"

// p12Env prepares a push test: no inherited distill bypass or lock timeout,
// update check off. testkit.NewProject isolates HOME and USERPROFILE, so
// these tests never run in parallel.
func p12Env(t *testing.T) {
	t.Helper()
	t.Setenv("CONTEXO_DISTILL_DISABLE", "")
	t.Setenv("CONTEXO_LOCK_TIMEOUT", "")
	t.Setenv("CONTEXO_NO_UPDATE_CHECK", "1")
}

// p12RunBin runs the built ctx with the test's HOME/USERPROFILE, the update
// check off and extraEnv appended (later entries win), and returns the
// process exit code and both streams. stdin is the null device, never a TTY.
func p12RunBin(t *testing.T, bin string, extraEnv []string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"HOME="+os.Getenv("HOME"), "USERPROFILE="+os.Getenv("USERPROFILE"),
		"CONTEXO_NO_UPDATE_CHECK=1")
	cmd.Env = append(cmd.Env, extraEnv...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run ctx: %v", err)
		}
		code = ee.ExitCode()
	}
	return code, out.String(), errb.String()
}

// p12Proxy is a reverse proxy in front of a testkit server. It records every
// request as "METHOD /path" and can run a hook once, right after the next
// page listing was fetched from the server and before the client gets it.
type p12Proxy struct {
	URL string

	mu        gosync.Mutex
	hits      []string
	afterList func()
}

func p12StartProxy(t *testing.T, target string) *p12Proxy {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	px := &p12Proxy{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		px.mu.Lock()
		px.hits = append(px.hits, r.Method+" "+r.URL.Path)
		var hook func()
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pages") {
			hook, px.afterList = px.afterList, nil
		}
		px.mu.Unlock()
		if hook == nil {
			rp.ServeHTTP(w, r)
			return
		}
		// Buffer the listing so the hook lands before the client sees it.
		rec := httptest.NewRecorder()
		rp.ServeHTTP(rec, r)
		hook()
		for k, vs := range rec.Header() {
			w.Header()[k] = vs
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	t.Cleanup(ts.Close)
	px.URL = ts.URL
	return px
}

// AfterList arms the one-shot listing hook.
func (px *p12Proxy) AfterList(hook func()) {
	px.mu.Lock()
	px.afterList = hook
	px.mu.Unlock()
}

// Hits returns the recorded requests.
func (px *p12Proxy) Hits() []string {
	px.mu.Lock()
	defer px.mu.Unlock()
	return append([]string{}, px.hits...)
}

// Reset forgets the recorded requests.
func (px *p12Proxy) Reset() {
	px.mu.Lock()
	px.hits = nil
	px.mu.Unlock()
}

// p12Project builds a project on p12RepoID whose project credentials point
// at serverURL.
func p12Project(t *testing.T, serverURL, key string, pages ...testkit.PageSpec) *testkit.Project {
	t.Helper()
	return testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: serverURL, Key: key, RepoID: p12RepoID, Pages: pages,
	})
}

// p12Entity is an entity page spec (never a knowledge page for the guard).
func p12Entity(slug, body string) testkit.PageSpec {
	return testkit.PageSpec{Type: schema.TypeEntity, Slug: slug, Body: body}
}

// p12DecodePushDoc strictly decodes stdout as exactly one ctx.push.v1 doc.
func p12DecodePushDoc(t *testing.T, stdout string) contract.PushDoc {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	var doc contract.PushDoc
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a strict PushDoc: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one document: %q", stdout)
	}
	if doc.Schema != contract.SchemaPush {
		t.Errorf("schema = %q, want %q", doc.Schema, contract.SchemaPush)
	}
	if doc.Pushed == nil || doc.Conflicts == nil || doc.Skipped == nil || doc.Warnings == nil {
		t.Errorf("doc arrays must be [] not null: %s", stdout)
	}
	return doc
}

// p12MustPush runs `push --json --root <root>` plus extra and returns the
// doc of a successful (exit 0) push.
func p12MustPush(t *testing.T, bin string, p *testkit.Project, extra ...string) contract.PushDoc {
	t.Helper()
	args := append([]string{"push", "--json", "--root", p.Root}, extra...)
	code, stdout, stderr := p12RunBin(t, bin, nil, args...)
	if code != 0 {
		t.Fatalf("push %q: exit %d\nstdout: %s\nstderr: %s", extra, code, stdout, stderr)
	}
	return p12DecodePushDoc(t, stdout)
}

// p12PushedSHA returns the sha doc reports for path, failing when absent.
func p12PushedSHA(t *testing.T, doc contract.PushDoc, path string) string {
	t.Helper()
	for _, pp := range doc.Pushed {
		if pp.Path == path {
			if pp.SHA == "" {
				t.Fatalf("pushed %s without a sha: %+v", path, doc.Pushed)
			}
			return pp.SHA
		}
	}
	t.Fatalf("%s not in pushed %+v", path, doc.Pushed)
	return ""
}

// p12EnvelopeDetails strictly decodes the last stderr line's error.details
// into v.
func p12EnvelopeDetails(t *testing.T, stderr string, v any) {
	t.Helper()
	lines := wp01NonEmptyLines(stderr)
	if len(lines) == 0 {
		t.Fatal("stderr is empty")
	}
	var raw struct {
		Error struct {
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &raw); err != nil {
		t.Fatalf("last stderr line is not JSON: %v", err)
	}
	if raw.Error.Details == nil {
		t.Fatalf("envelope has no details: %q", lines[len(lines)-1])
	}
	dec := json.NewDecoder(bytes.NewReader(raw.Error.Details))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("details: %v (%s)", err, raw.Error.Details)
	}
}

// p12AssertNoEnvelope fails when any stderr line is a JSON document.
func p12AssertNoEnvelope(t *testing.T, stderr string) {
	t.Helper()
	for _, l := range wp01NonEmptyLines(stderr) {
		var v map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(l)), &v) == nil {
			t.Errorf("human mode printed a JSON line on stderr: %q", l)
		}
	}
}

// p12WriteBuffer writes a pending capture buffer with lines turns and the
// given mtime.
func p12WriteBuffer(t *testing.T, hub, sid string, lines int, mtime time.Time) {
	t.Helper()
	dir := filepath.Join(hub, "raw", "sessions", "_pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < lines; i++ {
		b.WriteString(`{"role":"user","text":"turn"}` + "\n")
	}
	path := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// p12HashTree maps every file under hub ('/'-separated, relative) to its
// sha256, leaving out the lock file and .sync/.gitignore, which a dry run
// may create.
func p12HashTree(t *testing.T, hub string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(hub, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(hub, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".sync/lock" || rel == ".sync/.gitignore" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// p12Head returns the repo HEAD, or "" when the repo does not exist.
func p12Head(t *testing.T, srv *testkit.Server) string {
	t.Helper()
	if !srv.Store.Exists(p12RepoID) {
		return ""
	}
	head, err := srv.Store.HeadSHA(p12RepoID)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// p12ConflictSetup pushes two entity pages, edits both locally and arms the
// proxy so a teammate commit to x lands right after the next listing: the
// next push sends x with a parent the server has moved past (a 409) and y
// cleanly. It returns x, y, x's first-push sha and a getter for the
// teammate's sha.
func p12ConflictSetup(t *testing.T, bin string, srv *testkit.Server, px *p12Proxy, p *testkit.Project) (x, y, baseX string, teammateSHA func() string) {
	t.Helper()
	x = p.WritePage(p12Entity("p12-x", "v1 of x"))
	y = p.WritePage(p12Entity("p12-y", "v1 of y"))
	first := p12MustPush(t, bin, p)
	baseX = p12PushedSHA(t, first, x)
	p12PushedSHA(t, first, y)

	p.WritePage(p12Entity("p12-x", "alice's x"))
	p.WritePage(p12Entity("p12-y", "alice's y"))

	var mu gosync.Mutex
	var sha string
	px.AfterList(func() {
		s, conflict, err := srv.Store.Write(p12RepoID, x, testkit.PageBytes(p12Entity("p12-x", "bob's x")),
			"bob", "bob@example.test", "teammate edit", "")
		if err != nil || conflict != nil {
			t.Errorf("teammate write: %v %+v", err, conflict)
		}
		mu.Lock()
		sha = s
		mu.Unlock()
	})
	return x, y, baseX, func() string {
		mu.Lock()
		defer mu.Unlock()
		return sha
	}
}

// p12Cmd returns a bare command whose streams are buffers. cobra's
// OutOrStderr is the out buffer once SetOut was called.
func p12Cmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	cmd := &cobra.Command{Use: "push"}
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	return cmd, &out, &errb
}

// p12PreviewServer answers every page read with an old version, so an
// update file previews as EDIT, and counts the reads.
func p12PreviewServer(t *testing.T) (*sync.Client, func() int) {
	t.Helper()
	var mu gosync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.Header().Set("X-Page-SHA", "abc1234")
		_, _ = w.Write([]byte("---\nslug: x\ntype: entity\n---\n## Notes\nold\n"))
	}))
	t.Cleanup(srv.Close)
	return sync.NewClient(srv.URL, "tok"), func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

func TestPushConfirm_PreviewAndPrompt(t *testing.T) {
	update := syncops.PushPlanFile{Kind: contract.WriteUpdate, File: sync.PushFile{
		Path: "wiki/entities/x.md", Content: "---\nslug: x\ntype: entity\n---\n## Notes\nnew\n", ParentSHA: "abc1234"}}
	create := syncops.PushPlanFile{Kind: contract.WriteCreate, File: sync.PushFile{
		Path: "wiki/entities/z.md", Content: "---\nslug: z\ntype: entity\n---\nz\n", ExpectAbsent: true}}
	noTTY := func() bool { return false }
	tty := func() bool { return true }

	cases := []struct {
		name   string
		plan   []syncops.PushPlanFile
		fl     pushFlags
		isTTY  func() bool
		in     string
		ok     bool
		refuse bool
		reads  int
	}{
		{name: "edit without --yes off a TTY is refused", plan: []syncops.PushPlanFile{update}, isTTY: noTTY, refuse: true, reads: 1},
		{name: "--yes skips the prompt", plan: []syncops.PushPlanFile{update}, fl: pushFlags{yes: true}, isTTY: noTTY, ok: true, reads: 1},
		{name: "a dry run never prompts", plan: []syncops.PushPlanFile{update}, fl: pushFlags{dryRun: true}, isTTY: noTTY, ok: true, reads: 1},
		{name: "creates only need no confirmation", plan: []syncops.PushPlanFile{create}, isTTY: noTTY, ok: true, reads: 0},
		{name: "TTY yes", plan: []syncops.PushPlanFile{update, create}, isTTY: tty, in: "y\n", ok: true, reads: 1},
		{name: "TTY no", plan: []syncops.PushPlanFile{update}, isTTY: tty, in: "n\n", ok: false, reads: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, reads := p12PreviewServer(t)
			var out bytes.Buffer
			ok, err := pushConfirm(&out, strings.NewReader(c.in), c.isTTY, client, "repo", c.fl)(c.plan)
			if c.refuse {
				if err == nil || !strings.Contains(err.Error(), "pass --yes") {
					t.Fatalf("err = %v, want the non-interactive refusal", err)
				}
				if adv := ui.ToContractError(err).Advice; !reflect.DeepEqual(adv, []string{"ctx push --yes"}) {
					t.Errorf("advice = %q", adv)
				}
			} else if err != nil {
				t.Fatalf("err = %v", err)
			}
			if ok != c.ok {
				t.Errorf("ok = %v, want %v", ok, c.ok)
			}
			if got := reads(); got != c.reads {
				t.Errorf("server reads = %d, want %d", got, c.reads)
			}
			if !strings.Contains(out.String(), "About to push ") {
				t.Errorf("no preview printed:\n%s", out.String())
			}
		})
	}
}

func TestPushFinishHuman_Outcomes(t *testing.T) {
	t.Run("declined prints Aborted and succeeds", func(t *testing.T) {
		cmd, out, _ := p12Cmd()
		if err := pushFinishHuman(cmd, nil, syncops.ErrPushDeclined, pushFlags{}); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if out.String() != "Aborted.\n" {
			t.Errorf("out = %q", out.String())
		}
	})

	t.Run("conflict lists pages and carries resolve advice", func(t *testing.T) {
		cmd, out, _ := p12Cmd()
		doc := &contract.PushDoc{
			NewHead: "abcdef0123456789",
			Pushed:  []contract.PushedPage{{Path: "wiki/entities/y.md", Kind: contract.WriteUpdate, SHA: "1111111122"}},
			Conflicts: []contract.PushConflict{
				{Path: "wiki/entities/x.md", Kind: contract.ConflictBothModified, BaseSHA: "2222222233", ServerSHA: "3333333344"},
				{Path: "wiki/entities/z.md", Kind: contract.ConflictBothAdded, ServerSHA: "4444444455"},
			},
			Skipped: []contract.SkippedPage{}, Warnings: []contract.Warning{},
		}
		advice := []string{
			"ctx resolve wiki/entities/x.md --ours", "ctx resolve wiki/entities/x.md --theirs",
			"ctx resolve wiki/entities/z.md --ours", "ctx resolve wiki/entities/z.md --theirs",
		}
		engineErr := &contract.Error{Code: contract.CodeConflict, HTTPStatus: 409,
			Message: "push: 2 conflict(s) remain; other pages were pushed", Advice: advice,
			Details: contract.ConflictDetails{Paths: []string{"wiki/entities/x.md", "wiki/entities/z.md"}}}
		err := pushFinishHuman(cmd, doc, engineErr, pushFlags{})
		var ce *ui.CLIError
		if !errors.As(err, &ce) || !reflect.DeepEqual(ce.Advice, advice) {
			t.Fatalf("err = %#v, want a *ui.CLIError carrying the resolve advice", err)
		}
		if c := ui.ToContractError(err); c.Code != contract.CodeConflict || !reflect.DeepEqual(c.Advice, advice) {
			t.Errorf("contract error = %q %q", c.Code, c.Advice)
		}
		for _, want := range []string{
			"Pushed 1 page(s); HEAD=abcdef01\n",
			"2 conflict(s):\n",
			"  wiki/entities/x.md: server=33333333 base=22222222\n",
			"  wiki/entities/z.md: server=44444444 base=none\n",
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output lacks %q:\n%s", want, out.String())
			}
		}
	})

	t.Run("distill_required prints the agent prompt and the bypass note", func(t *testing.T) {
		cmd, _, errb := p12Cmd()
		engineErr := &contract.Error{Code: contract.CodeDistillRequired, Message: "push: 1 un-distilled agent session is pending",
			Details: &contract.DistillRequiredDetails{Pages: []string{"wiki/concepts/a.md"}, Sessions: []contract.CaptureSession{},
				AgentPrompt: "Push my pending Contexo changes with the ctx_push tool (wiki/concepts/a.md)."}}
		err := pushFinishHuman(cmd, nil, engineErr, pushFlags{})
		if err != error(engineErr) {
			t.Fatalf("err = %#v, want the engine error unchanged", err)
		}
		if adv := ui.ToContractError(err).Advice; len(adv) != 0 {
			t.Errorf("advice = %q, want []", adv)
		}
		want := "Share via your agent: Push my pending Contexo changes with the ctx_push tool (wiki/concepts/a.md).\n" +
			"ctx push --no-distill bypasses this guard; use it only deliberately.\n"
		if errb.String() != want {
			t.Errorf("stderr = %q, want %q", errb.String(), want)
		}
	})

	reports := []struct {
		name string
		doc  contract.PushDoc
		fl   pushFlags
		out  string
	}{
		{name: "nothing to push", out: "Nothing to push.\n"},
		{name: "dry run without preview lists the batch",
			doc: contract.PushDoc{DryRun: true, Pushed: []contract.PushedPage{
				{Path: "wiki/entities/x.md", Kind: contract.WriteUpdate}, {Path: "wiki/entities/z.md", Kind: contract.WriteCreate}}},
			fl:  pushFlags{dryRun: true, noPreview: true},
			out: "Would push 2 page(s):\n  wiki/entities/x.md  (update)\n  wiki/entities/z.md  (create)\n"},
		{name: "dry run after the preview",
			doc: contract.PushDoc{DryRun: true, Pushed: []contract.PushedPage{{Path: "wiki/entities/x.md", Kind: contract.WriteUpdate}}},
			fl:  pushFlags{dryRun: true},
			out: "(dry-run; no changes sent)\n"},
		{name: "pushed with skips",
			doc: contract.PushDoc{NewHead: "0123456789ab",
				Pushed:  []contract.PushedPage{{Path: "wiki/entities/x.md", Kind: contract.WriteCreate, SHA: "0123456789ab"}},
				Skipped: []contract.SkippedPage{{Path: "wiki/entities/c.md", State: contract.StateConflict, Reason: contract.SkipConflict}}},
			out: "Pushed 1 page(s); HEAD=01234567\nSkipped 1 page(s):\n  wiki/entities/c.md  (conflict)\n"},
	}
	for _, c := range reports {
		t.Run(c.name, func(t *testing.T) {
			cmd, out, errb := p12Cmd()
			doc := c.doc
			doc.Warnings = []contract.Warning{{Code: "base_repair_skipped", Message: "could not verify 1 base", Paths: []string{}}}
			if err := pushFinishHuman(cmd, &doc, nil, c.fl); err != nil {
				t.Fatalf("err = %v", err)
			}
			if out.String() != c.out {
				t.Errorf("out = %q, want %q", out.String(), c.out)
			}
			if errb.String() != "warning: could not verify 1 base\n" {
				t.Errorf("stderr = %q", errb.String())
			}
		})
	}
}

// The preflight runs before any request: an uninitialised root is
// not_initialized (5), a hub without credentials not_authenticated (4).
func TestPushJSON_PreflightErrors(t *testing.T) {
	t.Setenv("CONTEXO_DISTILL_DISABLE", "")
	cases := []struct {
		name   string
		root   string
		exit   int
		code   contract.ErrorCode
		advice []string
	}{
		{name: "no hub", root: t.TempDir(), exit: 5, code: contract.CodeNotInitialized, advice: []string{"ctx init"}},
		{name: "no credentials", root: wp01InitRoot(t), exit: 4, code: contract.CodeNotAuthenticated, advice: []string{"ctx auth login"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := wp01Run(t, "push", "--json", "--root", c.root, "--", "wiki/entities/x.md")
			wp01CheckEnvelopeCase(t, wp01EnvelopeCase{name: c.name, exit: c.exit, code: c.code, advice: c.advice},
				code, stdout, stderr)
		})
	}
}

func TestPushCapabilitiesRegistered(t *testing.T) {
	got := map[contract.Capability]bool{}
	for _, c := range advertisedCapabilities() {
		got[c] = true
	}
	for _, c := range []contract.Capability{contract.CapPushJSON, contract.CapPushPaths, contract.CapPushDistillGuard} {
		if !got[c] {
			t.Errorf("capability %q is not advertised: %q", c, advertisedCapabilities())
		}
	}
}

func TestPushJSON_DistillRequiredExit9(t *testing.T) {
	p12Env(t)
	bin := wp01BuildCtx(t)
	srv := testkit.StartServer(t)
	p := p12Project(t, srv.URL, srv.Key)
	rel := p.WritePage(testkit.PageSpec{Type: schema.TypeConcept, Slug: "p12-decision", Body: "## Decision\nUse X.\n"})
	mtime := time.Now().Add(-time.Minute)
	p12WriteBuffer(t, p.HubDir, "p12-session", 3, mtime)
	before := p12HashTree(t, p.HubDir)

	code, stdout, stderr := p12RunBin(t, bin, nil, "push", "--json", "--root", p.Root)
	if code != contract.ExitCodeFor(contract.CodeDistillRequired) || code != 9 {
		t.Fatalf("exit = %d, want 9\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout must be empty on a refusal, got %q", stdout)
	}
	env := wp01Envelope(t, stderr)
	if env.Error.Code != contract.CodeDistillRequired {
		t.Fatalf("code = %q, want distill_required", env.Error.Code)
	}
	if len(env.Error.Advice) != 0 {
		t.Errorf("advice = %q, want []", env.Error.Advice)
	}
	var d contract.DistillRequiredDetails
	p12EnvelopeDetails(t, stderr, &d)
	if !reflect.DeepEqual(d.Pages, []string{rel}) {
		t.Errorf("details.pages = %q, want [%s]", d.Pages, rel)
	}
	wantSession := contract.CaptureSession{SessionID: "p12-session", Turns: 3, UpdatedAt: contract.TimeOf(mtime), Fresh: true}
	if len(d.Sessions) != 1 || !d.Sessions[0].UpdatedAt.Equal(wantSession.UpdatedAt) ||
		d.Sessions[0].SessionID != wantSession.SessionID || d.Sessions[0].Turns != 3 || !d.Sessions[0].Fresh {
		t.Errorf("details.sessions = %+v, want [%+v]", d.Sessions, wantSession)
	}
	wantPrompt := "Push my pending Contexo changes with the ctx_push tool (" + rel +
		"). Distill the pending session into a source page first when ctx_push asks you to."
	if d.AgentPrompt != wantPrompt {
		t.Errorf("details.agent_prompt = %q, want %q", d.AgentPrompt, wantPrompt)
	}
	if srv.Store.Exists(p12RepoID) {
		t.Error("the refused push reached the server: the repo now exists")
	}
	if after := p12HashTree(t, p.HubDir); !reflect.DeepEqual(before, after) {
		t.Errorf("the refused push wrote under .contexo:\nbefore %v\nafter  %v", before, after)
	}

	// --no-distill is the deliberate bypass: same batch, pushed, and the doc
	// says the guard was required and bypassed.
	doc := p12MustPush(t, bin, p, "--no-distill")
	if doc.Distill != (contract.DistillCheck{Required: true, Bypassed: true}) {
		t.Errorf("distill = %+v, want {required true, bypassed true}", doc.Distill)
	}
	p12PushedSHA(t, doc, rel)
}

func TestPushJSON_ConflictExit6DocAndEnvelope(t *testing.T) {
	p12Env(t)
	bin := wp01BuildCtx(t)
	srv := testkit.StartServer(t)
	px := p12StartProxy(t, srv.URL)
	p := p12Project(t, px.URL, srv.Key)
	x, y, baseX, teammateSHA := p12ConflictSetup(t, bin, srv, px, p)

	code, stdout, stderr := p12RunBin(t, bin, nil, "push", "--json", "--root", p.Root)
	if code != 6 {
		t.Fatalf("exit = %d, want 6\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	doc := p12DecodePushDoc(t, stdout)
	want := []contract.PushConflict{{Path: x, Kind: contract.ConflictBothModified, BaseSHA: baseX, ServerSHA: teammateSHA()}}
	if !reflect.DeepEqual(doc.Conflicts, want) {
		t.Errorf("conflicts = %+v, want %+v", doc.Conflicts, want)
	}
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != y || doc.Pushed[0].Kind != contract.WriteUpdate || doc.Pushed[0].SHA == "" {
		t.Errorf("pushed = %+v, want only %s (update, with a sha)", doc.Pushed, y)
	}
	if doc.NewHead == "" || doc.DryRun {
		t.Errorf("new_head = %q, dry_run = %v", doc.NewHead, doc.DryRun)
	}

	env := wp01Envelope(t, stderr)
	if env.Error.Code != contract.CodeConflict || env.ExitCode != 6 {
		t.Fatalf("envelope code = %q exit_code = %d, want conflict / 6", env.Error.Code, env.ExitCode)
	}
	var d contract.ConflictDetails
	p12EnvelopeDetails(t, stderr, &d)
	if !reflect.DeepEqual(d.Paths, []string{x}) {
		t.Errorf("details.paths = %q, want [%s]", d.Paths, x)
	}
	wantAdvice := []string{"ctx resolve " + x + " --ours", "ctx resolve " + x + " --theirs"}
	if !reflect.DeepEqual(env.Error.Advice, wantAdvice) {
		t.Errorf("advice = %q, want %q", env.Error.Advice, wantAdvice)
	}

	// The teammate's commit survives on the server.
	got, _, err := srv.Store.Read(p12RepoID, x)
	if err != nil || !bytes.Equal(got, testkit.PageBytes(p12Entity("p12-x", "bob's x"))) {
		t.Errorf("server x = %q, %v; want bob's content", got, err)
	}
}

func TestPushHuman_ConflictExit1NoEnvelope(t *testing.T) {
	p12Env(t)
	bin := wp01BuildCtx(t)
	srv := testkit.StartServer(t)
	px := p12StartProxy(t, srv.URL)
	p := p12Project(t, px.URL, srv.Key)
	x, _, _, _ := p12ConflictSetup(t, bin, srv, px, p)

	code, stdout, stderr := p12RunBin(t, bin, nil, "push", "--yes", "--root", p.Root)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (human mode)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	p12AssertNoEnvelope(t, stderr)
	for _, want := range []string{"1 conflict(s):", "ctx resolve " + x + " --ours", "ctx resolve " + x + " --theirs"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stdout, "Pushed 1 page(s)") {
		t.Errorf("stdout lacks the pushed summary:\n%s", stdout)
	}
}

func TestPushJSON_ArgvWithYesAndSeparator(t *testing.T) {
	p12Env(t)
	bin := wp01BuildCtx(t)
	srv := testkit.StartServer(t)
	p := p12Project(t, srv.URL, srv.Key)
	x := p.WritePage(p12Entity("p12-x", "x"))
	y := p.WritePage(p12Entity("p12-y", "y"))

	// The extension's exact argv (E25).
	doc := p12MustPush(t, bin, p, "--yes", "-m", "msg", "--", x)
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != x || doc.Pushed[0].Kind != contract.WriteCreate || doc.Pushed[0].SHA == "" {
		t.Errorf("pushed = %+v, want only %s (create, with a sha)", doc.Pushed, x)
	}
	if len(doc.Conflicts) != 0 || doc.Message != "msg" {
		t.Errorf("conflicts = %+v, message = %q", doc.Conflicts, doc.Message)
	}
	if _, _, err := srv.Store.Read(p12RepoID, x); err != nil {
		t.Errorf("server lacks %s: %v", x, err)
	}
	if _, _, err := srv.Store.Read(p12RepoID, y); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("server read %s = %v, want not exist (it was not named)", y, err)
	}
	log, err := srv.Store.Log(p12RepoID, 10)
	if err != nil || len(log) != 1 || log[0].Message != "msg" {
		t.Errorf("server log = %+v, %v; want one commit with message %q", log, err, "msg")
	}
}

func TestPushJSON_NoPromptWithoutTTY(t *testing.T) {
	p12Env(t)
	bin := wp01BuildCtx(t)
	srv := testkit.StartServer(t)
	p := p12Project(t, srv.URL, srv.Key)
	x := p.WritePage(p12Entity("p12-x", "v1"))
	first := p12PushedSHA(t, p12MustPush(t, bin, p), x)

	p.WritePage(p12Entity("p12-x", "v2"))
	// No --yes, stdin is not a TTY: --json never prompts and never refuses.
	code, stdout, stderr := p12RunBin(t, bin, nil, "push", "--json", "--root", p.Root)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr must be empty in JSON mode, got %q", stderr)
	}
	doc := p12DecodePushDoc(t, stdout)
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != x || doc.Pushed[0].Kind != contract.WriteUpdate {
		t.Fatalf("pushed = %+v, want only %s (update)", doc.Pushed, x)
	}
	if doc.Pushed[0].SHA == "" || doc.Pushed[0].SHA == first {
		t.Errorf("pushed sha = %q, want a new commit after %q", doc.Pushed[0].SHA, first)
	}
	want, err := syncstate.Normalize(p.Read(x))
	if err != nil {
		t.Fatal(err)
	}
	if got, _, err := srv.Store.Read(p12RepoID, x); err != nil || !bytes.Equal(got, want) {
		t.Errorf("server x = %q, %v; want the local edit", got, err)
	}
}

// Human mode keeps its back-compat gate: editing an existing page from a
// non-TTY without --yes is refused (exit 1, nothing sent); --yes pushes.
func TestPushHuman_NonTTYRefusesEditsWithoutYes(t *testing.T) {
	p12Env(t)
	bin := wp01BuildCtx(t)
	srv := testkit.StartServer(t)
	p := p12Project(t, srv.URL, srv.Key)
	x := p.WritePage(p12Entity("p12-x", "v1"))
	p12MustPush(t, bin, p)
	head := p12Head(t, srv)

	p.WritePage(p12Entity("p12-x", "v2"))
	code, stdout, stderr := p12RunBin(t, bin, nil, "push", "--root", p.Root)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	p12AssertNoEnvelope(t, stderr)
	if !strings.Contains(stderr, "pass --yes") || !strings.Contains(stderr, "ctx push --yes") {
		t.Errorf("stderr lacks the --yes refusal:\n%s", stderr)
	}
	if !strings.Contains(stdout, "[EDIT]  "+x) {
		t.Errorf("stdout lacks the preview row for %s:\n%s", x, stdout)
	}
	if got := p12Head(t, srv); got != head {
		t.Errorf("the refused push committed: HEAD %s -> %s", head, got)
	}

	code, stdout, stderr = p12RunBin(t, bin, nil, "push", "--yes", "--root", p.Root)
	if code != 0 {
		t.Fatalf("push --yes: exit = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Pushed 1 page(s); HEAD=") {
		t.Errorf("stdout lacks the pushed summary:\n%s", stdout)
	}
	if got := p12Head(t, srv); got == head {
		t.Error("push --yes did not commit")
	}
}

func TestPush_BusyExit12(t *testing.T) {
	p12Env(t)
	bin := wp01BuildCtx(t)
	srv := testkit.StartServer(t)
	p := p12Project(t, srv.URL, srv.Key, p12Entity("p12-x", "x"))

	unlock, err := syncstate.Lock(p.HubDir, "test", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	code, stdout, stderr := p12RunBin(t, bin, []string{"CONTEXO_LOCK_TIMEOUT=300ms"}, "push", "--json", "--root", p.Root)
	if code != 12 {
		t.Fatalf("exit = %d, want 12\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout must be empty, got %q", stdout)
	}
	env := wp01Envelope(t, stderr)
	if env.Error.Code != contract.CodeBusy || !env.Error.Retryable {
		t.Errorf("code = %q retryable = %v, want busy / true", env.Error.Code, env.Error.Retryable)
	}
	var d contract.BusyDetails
	p12EnvelopeDetails(t, stderr, &d)
	if d.PID != os.Getpid() || d.Command != "test" {
		t.Errorf("details = %+v, want pid %d command test", d, os.Getpid())
	}
	if srv.Store.Exists(p12RepoID) {
		t.Error("a busy push reached the server")
	}
}

func TestPushJSON_DryRunWritesNothing(t *testing.T) {
	p12Env(t)
	bin := wp01BuildCtx(t)
	srv := testkit.StartServer(t)
	px := p12StartProxy(t, srv.URL)
	p := p12Project(t, px.URL, srv.Key)
	x := p.WritePage(p12Entity("p12-x", "v1"))
	p.WritePage(p12Entity("p12-y", "v1"))
	p12MustPush(t, bin, p)

	p.WritePage(p12Entity("p12-x", "v2"))
	z := p.WritePage(p12Entity("p12-z", "new"))
	head := p12Head(t, srv)
	before := p12HashTree(t, p.HubDir)
	px.Reset()

	doc := p12MustPush(t, bin, p, "--dry-run")
	if !doc.DryRun || doc.NewHead != "" {
		t.Errorf("dry_run = %v new_head = %q, want true and absent", doc.DryRun, doc.NewHead)
	}
	want := []contract.PushedPage{{Path: x, Kind: contract.WriteUpdate}, {Path: z, Kind: contract.WriteCreate}}
	if !reflect.DeepEqual(doc.Pushed, want) {
		t.Errorf("pushed = %+v, want %+v (no sha)", doc.Pushed, want)
	}
	if len(doc.Conflicts) != 0 {
		t.Errorf("conflicts = %+v, want []", doc.Conflicts)
	}
	if after := p12HashTree(t, p.HubDir); !reflect.DeepEqual(before, after) {
		t.Errorf("dry run wrote under .contexo:\nbefore %v\nafter  %v", before, after)
	}
	if hits, want := px.Hits(), []string{"GET /v1/repos/" + p12RepoID + "/pages"}; !reflect.DeepEqual(hits, want) {
		t.Errorf("dry run requests = %q, want only %q", hits, want)
	}
	if got := p12Head(t, srv); got != head {
		t.Errorf("dry run committed: HEAD %s -> %s", head, got)
	}
}
