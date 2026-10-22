package cli

// WP-1.1 `ctx status --json [--offline]` CLI tests. They run ctx in-process
// through Execute (always with --root, which NewRootCmd resets) and never
// call t.Parallel: Execute writes the package-level rootDir and the tests
// isolate HOME with t.Setenv.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
	ctxsync "github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
)

// st11RealHome and st11RealProfile are HOME and USERPROFILE as the test
// binary started; st11Run refuses to run while either still points there.
var (
	st11RealHome    = os.Getenv("HOME")
	st11RealProfile = os.Getenv("USERPROFILE")
)

// st11Home points HOME and USERPROFILE at a fresh temp dir, so no test reads
// the developer's real global credentials. testkit.NewProject does the same.
func st11Home(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// st11Run runs ctx in-process through Execute with the update check off and
// returns the exit code and both streams. It keeps the caller's HOME, so a
// test can seed global credentials there first, and fails when HOME was
// never isolated (st11Home or testkit.NewProject).
func st11Run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	if os.Getenv("HOME") == st11RealHome || os.Getenv("USERPROFILE") == st11RealProfile {
		t.Fatal("st11Run: HOME/USERPROFILE not isolated; call st11Home or testkit.NewProject first")
	}
	t.Setenv("CONTEXO_NO_UPDATE_CHECK", "1")
	var out, errb bytes.Buffer
	code = Execute(args, &out, &errb)
	return code, out.String(), errb.String()
}

// st11Decode strictly decodes stdout as exactly one ctx.status.v1 document.
func st11Decode(t *testing.T, stdout string) *contract.StatusDoc {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	var doc contract.StatusDoc
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a strict StatusDoc: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one document:\n%s", stdout)
	}
	if doc.Schema != contract.SchemaStatus {
		t.Errorf("schema = %q, want %q", doc.Schema, contract.SchemaStatus)
	}
	return &doc
}

// st11StatusJSON runs `status --json --root root` plus extra args and returns
// the decoded document, failing unless it exits 0 with an empty stderr.
func st11StatusJSON(t *testing.T, root string, extra ...string) (*contract.StatusDoc, string) {
	t.Helper()
	args := append([]string{"status", "--json", "--root", root}, extra...)
	code, stdout, stderr := st11Run(t, args...)
	if code != 0 {
		t.Fatalf("%q: exit %d, stderr %q", args, code, stderr)
	}
	if stderr != "" {
		t.Errorf("%q: stderr must be empty on success, got %q", args, stderr)
	}
	return st11Decode(t, stdout), stdout
}

// st11Trap is a server that fails the test on any request: status must not
// contact it.
type st11Trap struct {
	*httptest.Server
	hits atomic.Int32
}

func st11TrapServer(t *testing.T) *st11Trap {
	t.Helper()
	tr := &st11Trap{}
	tr.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr.hits.Add(1)
		t.Errorf("trap server received %s %s; status must send nothing", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(tr.Close)
	return tr
}

// st11Pages are two valid pages of different types.
var st11Pages = []testkit.PageSpec{
	{Slug: "alpha", Title: "Alpha", Tags: []string{"st11"}},
	{Type: schema.TypeEntity, Slug: "beta"},
}

func TestStatusJSON_NotInitialized(t *testing.T) {
	st11Home(t)
	uninit := t.TempDir()
	for _, extra := range [][]string{nil, {"--offline"}} {
		args := append([]string{"status", "--json", "--root", uninit}, extra...)
		code, stdout, stderr := st11Run(t, args...)
		if code != contract.ExitConfig {
			t.Errorf("%q: exit = %d, want %d (stderr %q)", args, code, contract.ExitConfig, stderr)
		}
		if stdout != "" {
			t.Errorf("%q: stdout must be empty, got %q", args, stdout)
		}
		if n := len(wp01NonEmptyLines(stderr)); n != 1 {
			t.Errorf("%q: stderr must hold exactly one line, got %d: %q", args, n, stderr)
		}
		env := wp01Envelope(t, stderr)
		if env.Error.Code != contract.CodeNotInitialized || env.ExitCode != contract.ExitConfig {
			t.Errorf("%q: envelope = %+v, want not_initialized / exit 5", args, env)
		}
		if !reflect.DeepEqual(env.Error.Advice, []string{"ctx init"}) {
			t.Errorf("%q: advice = %q, want [ctx init]", args, env.Error.Advice)
		}
		if !strings.HasPrefix(env.Error.Message, "status: ") || !strings.Contains(env.Error.Message, uninit) {
			t.Errorf("%q: message %q must start with 'status: ' and name %s", args, env.Error.Message, uninit)
		}
	}
}

func TestStatusHuman_NotInitializedExit0(t *testing.T) {
	st11Home(t)
	code, stdout, stderr := st11Run(t, "status", "--root", t.TempDir())
	if code != 0 {
		t.Errorf("exit = %d, want 0 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stdout, "Not initialized") || !strings.Contains(stdout, "ctx init") {
		t.Errorf("stdout lacks the not-initialized notice:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestStatusJSON_NoDriftAlias(t *testing.T) {
	f := newStatusCmd().Flags().Lookup("no-drift")
	if f == nil {
		t.Fatal("ctx status has no --no-drift flag")
	}
	if !f.Hidden {
		t.Error("--no-drift must be hidden")
	}
	if o := newStatusCmd().Flags().Lookup("offline"); o == nil || o.Hidden {
		t.Errorf("--offline must exist and be visible: %+v", o)
	}

	trap := st11TrapServer(t)
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: trap.URL, Key: "st11-key", RepoID: "st11-repo", Pages: st11Pages,
	})
	docs := map[string]*contract.StatusDoc{}
	for _, flag := range []string{"--no-drift", "--offline"} {
		doc, _ := st11StatusJSON(t, p.Root, flag)
		if !doc.Offline {
			t.Errorf("%s: offline = false", flag)
		}
		if doc.Remote.Source != contract.RemoteNone || doc.Remote.Error != nil {
			t.Errorf("%s: remote = %+v, want source none and no error", flag, doc.Remote)
		}
		if len(doc.Pages) != len(st11Pages) {
			t.Errorf("%s: %d pages, want %d", flag, len(doc.Pages), len(st11Pages))
		}
		docs[flag] = doc
	}
	if !reflect.DeepEqual(docs["--no-drift"].Pages, docs["--offline"].Pages) {
		t.Errorf("--no-drift pages differ from --offline:\n%+v\n%+v", docs["--no-drift"].Pages, docs["--offline"].Pages)
	}

	// The human form of the alias sends nothing either.
	if code, _, stderr := st11Run(t, "status", "--no-drift", "--root", p.Root); code != 0 {
		t.Errorf("status --no-drift: exit %d, stderr %q", code, stderr)
	}
	if n := trap.hits.Load(); n != 0 {
		t.Errorf("--no-drift/--offline sent %d request(s)", n)
	}
}

func TestStatusJSON_Usage(t *testing.T) {
	st11Home(t)
	code, stdout, stderr := st11Run(t, "status", "--json", "extra", "--root", t.TempDir())
	if code != contract.ExitUsage {
		t.Errorf("exit = %d, want %d (stderr %q)", code, contract.ExitUsage, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout must be empty, got %q", stdout)
	}
	if env := wp01Envelope(t, stderr); env.Error.Code != contract.CodeUsage {
		t.Errorf("code = %q, want usage", env.Error.Code)
	}
}

func TestStatusCapabilitiesRegistered(t *testing.T) {
	have := map[contract.Capability]bool{}
	for _, c := range advertisedCapabilities() {
		have[c] = true
	}
	for _, c := range []contract.Capability{contract.CapStatusJSON, contract.CapStatusOffline} {
		if !have[c] {
			t.Errorf("capability %q is not registered: %q", c, advertisedCapabilities())
		}
	}

	code, stdout, stderr := wp01Run(t, "version", "--json")
	if code != 0 {
		t.Fatalf("version --json: exit %d, stderr %q", code, stderr)
	}
	doc := wp01DecodeVersionDoc(t, stdout)
	wp01CheckCapabilities(t, doc.Capabilities)
	adv := map[contract.Capability]bool{}
	for _, c := range doc.Capabilities {
		adv[c] = true
	}
	if !adv[contract.CapStatusJSON] || !adv[contract.CapStatusOffline] {
		t.Errorf("version --json does not advertise status.json and status.offline: %q", doc.Capabilities)
	}
}

// st11ArrayKeys are the document keys that must always be arrays, never
// null (contract "never null" rule; ErrorBody.advice too).
var st11ArrayKeys = map[string]bool{
	"pages": true, "tags": true, "notes": true, "warnings": true,
	"sessions": true, "capabilities": true, "paths": true, "advice": true,
}

// st11TimeKeys are the document keys that hold contract times.
var st11TimeKeys = map[string]bool{
	"generated_at": true, "fetched_at": true, "synced_at": true,
	"time": true, "updated": true, "updated_at": true,
}

// st11TimeRE is the only contract time form (E16).
var st11TimeRE = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)

// st11CheckRaw walks the raw JSON: no value is null, every st11ArrayKeys
// key holds an array, every time is whole-second UTC, and the always-present
// arrays (pages, warnings, captures.sessions, server.capabilities, each
// page's tags and notes) are there.
func st11CheckRaw(t *testing.T, raw string) {
	t.Helper()
	var top map[string]any
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		t.Fatalf("decode raw document: %v", err)
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case nil:
			t.Errorf("%s is null", path)
		case map[string]any:
			for k, e := range x {
				if _, ok := e.([]any); st11ArrayKeys[k] && !ok {
					t.Errorf("%s.%s is not an array: %v", path, k, e)
				}
				if s, ok := e.(string); st11TimeKeys[k] && (!ok || !st11TimeRE.MatchString(s)) {
					t.Errorf("%s.%s = %v, want YYYY-MM-DDTHH:MM:SSZ", path, k, e)
				}
				walk(path+"."+k, e)
			}
		case []any:
			for i, e := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), e)
			}
		}
	}
	walk("$", top)

	need := func(m map[string]any, path string, keys ...string) {
		for _, k := range keys {
			if _, ok := m[k]; !ok {
				t.Errorf("%s.%s is missing", path, k)
			}
		}
	}
	need(top, "$", "pages", "warnings")
	if s, ok := top["server"].(map[string]any); ok {
		need(s, "$.server", "capabilities")
	}
	if c, ok := top["captures"].(map[string]any); ok {
		need(c, "$.captures", "sessions")
	}
	pages, _ := top["pages"].([]any)
	for i, p := range pages {
		if m, ok := p.(map[string]any); ok {
			need(m, fmt.Sprintf("$.pages[%d]", i), "tags", "notes")
		}
	}
}

// st11AssertNoSecret fails when any output carries the token or a PAT
// prefix.
func st11AssertNoSecret(t *testing.T, label, token string, outputs ...string) {
	t.Helper()
	for _, o := range outputs {
		if token != "" && strings.Contains(o, token) {
			t.Errorf("%s: output carries the token %q:\n%s", label, token, o)
		}
		if strings.Contains(o, "ctxp_") {
			t.Errorf("%s: output carries a 'ctxp_' token:\n%s", label, o)
		}
	}
}

// st11Push pushes files (path -> page bytes, sent normalized) to srv's repo
// in one request through the real client and returns each path's reported
// sha. A path without a parent is sent with expect_absent.
func st11Push(t *testing.T, srv *testkit.Server, repo string, files map[string][]byte, parents map[string]string) map[string]string {
	t.Helper()
	req := &ctxsync.PushRequest{AuthorName: "Seed", AuthorEmail: "seed@st11.test", Message: "st11 push"}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		norm, err := syncstate.Normalize(files[p])
		if err != nil {
			t.Fatalf("normalize %s: %v", p, err)
		}
		f := ctxsync.PushFile{Path: p, Content: string(norm), ParentSHA: parents[p]}
		f.ExpectAbsent = f.ParentSHA == ""
		req.Files = append(req.Files, f)
	}
	resp, err := ctxsync.NewClient(srv.URL, srv.Key).PushPages(repo, req)
	if err != nil {
		t.Fatalf("push %q: %v", paths, err)
	}
	if len(resp.Conflicts) != 0 {
		t.Fatalf("push %q: conflicts %+v", paths, resp.Conflicts)
	}
	shas := map[string]string{}
	for _, pf := range resp.Pushed {
		shas[pf.Path] = pf.SHA
	}
	for _, p := range paths {
		if shas[p] == "" {
			t.Fatalf("push %q: no sha for %s in %+v", paths, p, resp.Pushed)
		}
	}
	return shas
}

// st11RecordBases stores Normalize(files[p]) as p's base at shas[p] and
// writes page_shas, as a successful push or pull does.
func st11RecordBases(t *testing.T, hub string, files map[string][]byte, shas map[string]string) {
	t.Helper()
	bases, err := syncstate.OpenBases(hub)
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := ctxsync.LoadStateOrEmpty(hub)
	if err != nil {
		t.Fatal(err)
	}
	for p, content := range files {
		norm, err := syncstate.Normalize(content)
		if err != nil {
			t.Fatalf("normalize %s: %v", p, err)
		}
		if err := bases.Put(p, norm, shas[p], time.Now()); err != nil {
			t.Fatal(err)
		}
		st.PageSHAs[p] = shas[p]
	}
	if err := bases.Save(); err != nil {
		t.Fatal(err)
	}
	if err := ctxsync.SaveState(hub, st); err != nil {
		t.Fatal(err)
	}
}

func TestStatusJSON_ShapeMatchesContract(t *testing.T) {
	srv := testkit.StartServer(t)
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: srv.URL, Key: srv.Key, RepoID: "st11-shape",
		Pages: []testkit.PageSpec{
			{Slug: "a", Title: "A"},
			{Slug: "b", Tags: []string{"x", "y"}},
			{Type: schema.TypeEntity, Slug: "c"},
		},
	})
	// a is synced, z exists only on the server, b and c are new locally.
	a := "wiki/concepts/a.md"
	z := "wiki/concepts/z.md"
	files := map[string][]byte{a: p.Read(a), z: testkit.PageBytes(testkit.PageSpec{Slug: "z"})}
	shas := st11Push(t, srv, "st11-shape", files, nil)
	st11RecordBases(t, p.HubDir, map[string][]byte{a: files[a]}, shas)

	code, stdout, stderr := st11Run(t, "status", "--json", "--root", p.Root)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	doc := st11Decode(t, stdout)
	st11CheckRaw(t, stdout)
	var want bytes.Buffer
	if err := contract.WriteDoc(&want, doc); err != nil {
		t.Fatal(err)
	}
	if stdout != want.String() {
		t.Errorf("stdout is not WriteDoc output:\n got %s\nwant %s", stdout, want.String())
	}
	if doc.Remote.Source != contract.RemoteLive || doc.Auth.State != contract.AuthAuthenticated {
		t.Errorf("remote/auth = %+v / %+v, want live / authenticated", doc.Remote, doc.Auth)
	}
	if len(doc.Pages) != 4 {
		t.Errorf("pages = %+v, want a, b, c, z", doc.Pages)
	}
	if !filepath.IsAbs(doc.Root) || doc.Root != p.Root || doc.ContexoDir != p.HubDir {
		t.Errorf("root/contexo_dir = %q/%q, want %q/%q", doc.Root, doc.ContexoDir, p.Root, p.HubDir)
	}
	if doc.Server.URL != srv.URL || doc.Project.RepoID != "st11-shape" {
		t.Errorf("server.url/repo_id = %q/%q", doc.Server.URL, doc.Project.RepoID)
	}
	st11AssertNoSecret(t, "server project", srv.Key, stdout, stderr)
	_, human, herr := st11Run(t, "status", "--root", p.Root)
	st11AssertNoSecret(t, "server project (human)", srv.Key, human, herr)

	// Token hygiene with a PAT: offline, human, and online against a server
	// that rejects it (remote.error must not echo the token either).
	const pat = "ctxp_secret_wp11"
	reject := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid or expired token"}`))
	}))
	t.Cleanup(reject.Close)
	q := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: reject.URL, Key: pat, RepoID: "st11-hygiene", Pages: st11Pages,
	})
	for _, args := range [][]string{
		{"status", "--json", "--offline", "--root", q.Root},
		{"status", "--offline", "--root", q.Root},
		{"status", "--json", "--root", q.Root},
		{"status", "--root", q.Root},
	} {
		code, stdout, stderr := st11Run(t, args...)
		if code != 0 {
			t.Errorf("%q: exit %d, stderr %q", args, code, stderr)
		}
		st11AssertNoSecret(t, strings.Join(args, " "), pat, stdout, stderr)
	}
	doc, _ = st11StatusJSON(t, q.Root, "--offline")
	if doc.Auth.TokenKind != "pat" {
		t.Errorf("auth.token_kind = %q, want pat", doc.Auth.TokenKind)
	}
}

func TestStatusJSON_RemoteUnusablePrecedence(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, trapURL string) *testkit.Project
		code   contract.ErrorCode
		advice []string
		auth   contract.AuthState
	}{
		{
			name: "no credentials",
			setup: func(t *testing.T, trapURL string) *testkit.Project {
				return testkit.NewProject(t, testkit.ProjectOptions{
					ServerURL: trapURL, RepoID: "st11-repo", NoCreds: true, Pages: st11Pages,
				})
			},
			code: contract.CodeNotAuthenticated, advice: []string{"ctx auth login"},
			auth: contract.AuthNotAuthenticated,
		},
		{
			name: "global credentials without server_url",
			setup: func(t *testing.T, trapURL string) *testkit.Project {
				p := testkit.NewProject(t, testkit.ProjectOptions{
					ServerURL: trapURL, RepoID: "st11-repo", NoCreds: true, Pages: st11Pages,
				})
				if err := config.SaveGlobalCredentials(&config.Credentials{Token: "legacy-k"}); err != nil {
					t.Fatal(err)
				}
				return p
			},
			code: contract.CodeServerUnresolved, advice: []string{"ctx auth login --server <url>"},
			auth: contract.AuthUnchecked,
		},
		{
			name: "no repo_id",
			setup: func(t *testing.T, trapURL string) *testkit.Project {
				return testkit.NewProject(t, testkit.ProjectOptions{
					ServerURL: trapURL, Key: "st11-key", Pages: st11Pages,
				})
			},
			code: contract.CodeRepoNotConfigured, advice: []string{"ctx remote set-repo <id>"},
			auth: contract.AuthUnchecked,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			trap := st11TrapServer(t)
			p := c.setup(t, trap.URL)
			doc, raw := st11StatusJSON(t, p.Root)
			st11CheckRaw(t, raw)
			e := doc.Remote.Error
			if e == nil {
				t.Fatalf("remote.error missing; remote = %+v", doc.Remote)
			}
			if e.Code != c.code || !reflect.DeepEqual(e.Advice, c.advice) {
				t.Errorf("remote.error = %s %q, want %s %q", e.Code, e.Advice, c.code, c.advice)
			}
			if e.Retryable || e.Message == "" {
				t.Errorf("remote.error = %+v, want retryable false and a message", e)
			}
			if doc.Remote.Source != contract.RemoteNone || doc.Offline {
				t.Errorf("remote.source = %q offline = %v, want none / false", doc.Remote.Source, doc.Offline)
			}
			if doc.Auth.State != c.auth {
				t.Errorf("auth.state = %q, want %q", doc.Auth.State, c.auth)
			}
			if len(doc.Pages) != len(st11Pages) {
				t.Errorf("%d pages, want %d (status behaves like --offline)", len(doc.Pages), len(st11Pages))
			}
		})
	}
}

func TestStatusJSON_DashboardEmptyOmitsTemplate(t *testing.T) {
	srv := testkit.StartServer(t)
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: srv.URL, Key: srv.Key, RepoID: "st11-dash", Pages: st11Pages,
	})
	doc, raw := st11StatusJSON(t, p.Root)
	if !strings.Contains(raw, `"dashboard_url": ""`) {
		t.Errorf(`raw JSON lacks "dashboard_url": "":\n%s`, raw)
	}
	if strings.Contains(raw, "dashboard_page_url_template") {
		t.Errorf("a self-hosted server without dashboard_url must omit the template:\n%s", raw)
	}
	if doc.Project.DashboardURL != "" {
		t.Errorf("dashboard_url = %q, want empty", doc.Project.DashboardURL)
	}

	// With a configured dashboard the template is there (identity is filled
	// offline too).
	cfg, err := config.Load(p.Root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DashboardURL = "https://dash.st11.test/"
	if err := config.Save(p.Root, cfg); err != nil {
		t.Fatal(err)
	}
	doc, _ = st11StatusJSON(t, p.Root, "--offline")
	want := "https://dash.st11.test/repos/st11-dash/page/{path}"
	if doc.Project.DashboardURL != "https://dash.st11.test" || doc.Project.DashboardPageURLTemplate != want {
		t.Errorf("dashboard = %q / %q, want https://dash.st11.test / %q",
			doc.Project.DashboardURL, doc.Project.DashboardPageURLTemplate, want)
	}
}

func TestStatusJSON_OfflineHasNoRemoteError(t *testing.T) {
	trap := st11TrapServer(t)
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: trap.URL, RepoID: "st11-repo", NoCreds: true, Pages: st11Pages,
	})
	doc, raw := st11StatusJSON(t, p.Root, "--offline")
	st11CheckRaw(t, raw)
	if doc.Remote.Error != nil || strings.Contains(raw, `"error"`) {
		t.Errorf("--offline must carry no remote.error: %+v", doc.Remote.Error)
	}
	if doc.Auth.State != contract.AuthNotAuthenticated {
		t.Errorf("auth.state = %q, want not_authenticated", doc.Auth.State)
	}
	if !doc.Offline || doc.Remote.Source != contract.RemoteNone || doc.Server.CapabilitiesKnown {
		t.Errorf("offline doc: offline=%v source=%q capabilities_known=%v", doc.Offline, doc.Remote.Source, doc.Server.CapabilitiesKnown)
	}
}

func TestStatusJSON_RelativeRootAbsolute(t *testing.T) {
	p := testkit.NewProject(t, testkit.ProjectOptions{NoCreds: true, Pages: st11Pages})
	t.Chdir(filepath.Dir(p.Root))
	doc, _ := st11StatusJSON(t, filepath.Base(p.Root), "--offline")
	if !filepath.IsAbs(doc.Root) || !filepath.IsAbs(doc.ContexoDir) {
		t.Errorf("root %q / contexo_dir %q must be absolute", doc.Root, doc.ContexoDir)
	}
	if doc.Root != p.Root || doc.ContexoDir != p.HubDir {
		t.Errorf("root/contexo_dir = %q/%q, want %q/%q", doc.Root, doc.ContexoDir, p.Root, p.HubDir)
	}
}

func TestStatusBinary_ExitCodes(t *testing.T) {
	bin := wp01BuildCtx(t)
	home := st11Home(t)

	uninit := t.TempDir()
	code, stdout, stderr := wp01RunBin(t, bin, home, "status", "--json", "--root", uninit)
	if code != contract.ExitConfig || stdout != "" {
		t.Errorf("uninitialized: exit %d stdout %q, want 5 and empty", code, stdout)
	}
	if env := wp01Envelope(t, stderr); env.Error.Code != contract.CodeNotInitialized {
		t.Errorf("uninitialized: code %q, want not_initialized", env.Error.Code)
	}

	code, _, stderr = wp01RunBin(t, bin, home, "status", "--json", "extra", "--root", uninit)
	if code != contract.ExitUsage {
		t.Errorf("positional arg: exit %d, want 3 (stderr %q)", code, stderr)
	}

	trap := st11TrapServer(t)
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: trap.URL, Key: "st11-key", RepoID: "st11-repo", Pages: st11Pages,
	})
	code, stdout, stderr = wp01RunBin(t, bin, os.Getenv("HOME"), "status", "--json", "--offline", "--root", p.Root)
	if code != 0 || stderr != "" {
		t.Fatalf("offline: exit %d stderr %q, want 0 and empty", code, stderr)
	}
	if doc := st11Decode(t, stdout); !doc.Offline || len(doc.Pages) != len(st11Pages) {
		t.Errorf("offline doc: offline=%v pages=%d", doc.Offline, len(doc.Pages))
	}
}
