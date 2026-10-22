package cli

// Owner: WP-1.4. `ctx show <relpath> --at base|server|<sha>`: the order of
// the preflight checks, the raw-bytes output, the always-on envelope
// (E20/E21) and the network variants against in-process servers. Engine
// behaviour is pinned in internal/syncops/show_test.go.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
	"github.com/sugihAF/contexo/internal/ui"
)

// sr14ShowCLIRel is the page the cli show tests read.
const sr14ShowCLIRel = "wiki/concepts/a.md"

// sr14ShowCLISHA is a full sha no server here ever committed.
const sr14ShowCLISHA = "0123456789abcdef0123456789abcdef01234567"

// sr14ShowCanary is planted in files show must never print.
const sr14ShowCanary = "sr14-canary-5f0e2b7c9d1a"

// sr14ShowExec runs ctx in-process through Execute and returns the exit
// code and both streams. Unlike wp01Run it leaves HOME alone, so a
// testkit.NewProject's isolated HOME stays in force; a test that builds no
// project isolates HOME itself.
func sr14ShowExec(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	t.Setenv("CONTEXO_NO_UPDATE_CHECK", "1")
	var out, errb bytes.Buffer
	code = Execute(args, &out, &errb)
	return code, out.String(), errb.String()
}

// sr14ShowFailure asserts a failed show: the exit code, an empty stdout and
// exactly one stderr line, a strict envelope with code want. It returns the
// envelope.
func sr14ShowFailure(t *testing.T, code int, stdout, stderr string, exit int, want contract.ErrorCode) contract.ErrorEnvelope {
	t.Helper()
	if code != exit {
		t.Errorf("exit = %d, want %d (stderr %q)", code, exit, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout must be empty on failure, got %q", stdout)
	}
	if n := len(wp01NonEmptyLines(stderr)); n != 1 {
		t.Errorf("stderr must be exactly one envelope line, got %d: %q", n, stderr)
	}
	env := wp01Envelope(t, stderr)
	if env.Error.Code != want {
		t.Errorf("code = %q, want %q (message %q)", env.Error.Code, want, env.Error.Message)
	}
	if env.ExitCode != code {
		t.Errorf("envelope exit_code %d != process exit %d", env.ExitCode, code)
	}
	return env
}

// sr14ShowSuccess asserts a successful show: exit 0, stdout exactly want
// (no added newline) and nothing at all on stderr.
func sr14ShowSuccess(t *testing.T, code int, stdout, stderr string, want []byte) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr)
	}
	if stdout != string(want) {
		t.Errorf("stdout = %q, want exactly %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr must be empty on success, got %q", stderr)
	}
}

// sr14ShowSeedBase stores content as rel's base for sha and records sha in
// state.json page_shas, as a push or pull leaves a synced page.
func sr14ShowSeedBase(t *testing.T, hub, rel string, content []byte, sha string) {
	t.Helper()
	b, err := syncstate.OpenBases(hub)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Put(rel, content, sha, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	st, _, err := sync.LoadStateOrEmpty(hub)
	if err != nil {
		t.Fatal(err)
	}
	st.PageSHAs[rel] = sha
	if err := sync.SaveState(hub, st); err != nil {
		t.Fatal(err)
	}
}

// sr14ShowPush pushes content at rel to the in-process server s, as a
// teammate's ctx push would, and returns the sha the server reported.
func sr14ShowPush(t *testing.T, s *testkit.Server, repo, rel string, content []byte) string {
	t.Helper()
	resp, err := sync.NewClient(s.URL, s.Key).PushPages(repo, &sync.PushRequest{
		AuthorName:  "Teammate",
		AuthorEmail: "teammate@example.com",
		Message:     "push " + rel,
		Files:       []sync.PushFile{{Path: rel, Content: string(content)}},
	})
	if err != nil {
		t.Fatalf("push %s: %v", rel, err)
	}
	if len(resp.Conflicts) != 0 || len(resp.Pushed) != 1 || resp.Pushed[0].SHA == "" {
		t.Fatalf("push %s: unexpected response %+v", rel, resp)
	}
	return resp.Pushed[0].SHA
}

// sr14ShowPage is a valid concept page with slug "a" and body.
func sr14ShowPage(body string) []byte {
	return testkit.PageBytes(testkit.PageSpec{Slug: "a", Body: body})
}

func TestShowNotHidden(t *testing.T) {
	found, _, err := NewRootCmd().Find([]string{"show"})
	if err != nil || found.Name() != "show" {
		t.Fatalf("Find(show) = %v, %v", found, err)
	}
	if found.Hidden {
		t.Error("ctx show must not be hidden")
	}

	show := wp01Command(t, "show")
	if show.Hidden {
		t.Error("ctx show must not be hidden")
	}
	if show.Use != "show <relpath> --at base|server|<sha>" {
		t.Errorf("Use = %q", show.Use)
	}

	at := show.Flags().Lookup("at")
	if at == nil {
		t.Fatal("--at is not defined")
	}
	if got := at.Annotations[cobra.BashCompOneRequiredFlag]; !reflect.DeepEqual(got, []string{"true"}) {
		t.Errorf("--at must be required, annotations %v", at.Annotations)
	}

	js := show.Flags().Lookup("json")
	if js == nil {
		t.Fatal("hidden --json is not defined")
	}
	if !js.Hidden {
		t.Error("--json must be hidden on show")
	}
	if js.DefValue != "true" || js.Value.String() != "true" || js.Value.Type() != "bool" {
		t.Errorf("--json must be a bool defaulting to true, got %s=%q", js.Value.Type(), js.DefValue)
	}

	// Exactly one positional path; anything else is usage.
	if c := wp01ArgsCode(t, show, "wiki/concepts/a.md"); c != "" {
		t.Errorf("one path rejected: %q", c)
	}
	for _, args := range [][]string{{}, {"a.md", "b.md"}} {
		if c := wp01ArgsCode(t, show, args...); c != contract.CodeUsage {
			t.Errorf("args %q -> %q, want usage", args, c)
		}
	}

	// show is always machine mode, so a missing --at is a usage envelope even
	// without --json.
	code, stdout, stderr := wp01Run(t, "show", "wiki/concepts/a.md", "--root", wp01InitRoot(t))
	if code != contract.ExitUsage || stdout != "" {
		t.Fatalf("missing --at: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if env := wp01Envelope(t, stderr); env.Error.Code != contract.CodeUsage {
		t.Errorf("missing --at code = %q", env.Error.Code)
	}
}

// show is always machine mode (E20/E21): the hidden --json cannot be turned
// off. A bare --json or --json=true is a no-op; --json=false is a usage error
// that is still rendered as an envelope, and the flag keeps reading "true"
// for ui.DetectMode and ui.EnvelopeMode.
func TestShowJSONCannotBeTurnedOff(t *testing.T) {
	show := wp01Command(t, "show")
	js := show.Flags().Lookup("json")
	if js == nil {
		t.Fatal("hidden --json is not defined")
	}
	if js.NoOptDefVal != "true" {
		t.Errorf("bare --json must mean true, NoOptDefVal %q", js.NoOptDefVal)
	}
	for _, v := range []string{"false", "0", "no"} {
		if err := show.Flags().Set("json", v); err == nil {
			t.Errorf("--json=%s accepted", v)
		}
	}
	if err := show.Flags().Set("json", "true"); err != nil {
		t.Errorf("--json=true rejected: %v", err)
	}
	if got := js.Value.String(); got != "true" {
		t.Errorf("--json reads %q after a rejected false, want true", got)
	}
	if got, err := show.Flags().GetBool("json"); err != nil || !got {
		t.Errorf("GetBool(json) = %v, %v; want true", got, err)
	}
	var buf bytes.Buffer
	if !ui.DetectMode(show, &buf).Machine {
		t.Error("show must stay machine mode")
	}

	uninit := t.TempDir()
	for _, c := range []struct {
		flag string
		exit int
		code contract.ErrorCode
	}{
		{"--json=false", contract.ExitUsage, contract.CodeUsage},
		{"--json=true", contract.ExitCodeFor(contract.CodeNotInitialized), contract.CodeNotInitialized},
		{"--json", contract.ExitCodeFor(contract.CodeNotInitialized), contract.CodeNotInitialized},
	} {
		t.Run(c.flag, func(t *testing.T) {
			code, stdout, stderr := wp01Run(t, "show", "wiki/concepts/a.md", "--at", "base", c.flag, "--root", uninit)
			if code != c.exit {
				t.Errorf("exit = %d, want %d (stderr %q)", code, c.exit, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout must be empty, got %q", stdout)
			}
			if n := len(wp01NonEmptyLines(stderr)); n != 1 {
				t.Errorf("stderr must be one envelope line, got %q", stderr)
			}
			if env := wp01Envelope(t, stderr); env.Error.Code != c.code {
				t.Errorf("code = %q, want %q (message %q)", env.Error.Code, c.code, env.Error.Message)
			}
		})
	}
}

func TestShowCapabilities(t *testing.T) {
	got := map[contract.Capability]bool{}
	for _, c := range advertisedCapabilities() {
		got[c] = true
	}
	for _, c := range []contract.Capability{contract.CapShowBase, contract.CapShowServer, contract.CapShowSHA} {
		if !got[c] {
			t.Errorf("capability %q is not advertised (got %v)", c, advertisedCapabilities())
		}
	}
}

// Every path that is not a page is invalid_path (exit 3) before anything is
// read: not the base, not the server, not credentials.json or .sync/*. The
// project has credentials and a repo, so nothing but the path rule stops a
// network variant.
func TestShow_InvalidPath(t *testing.T) {
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: "http://127.0.0.1:9", Key: sr14ShowCanary, RepoID: "acme-kb",
	})
	if creds, err := os.ReadFile(filepath.Join(p.HubDir, "credentials.json")); err != nil || !strings.Contains(string(creds), sr14ShowCanary) {
		t.Fatalf("fixture: credentials.json lacks the canary: %q, %v", creds, err)
	}
	if err := sync.SaveState(p.HubDir, &sync.State{LastPullSHA: sr14ShowCanary, PageSHAs: map[string]string{
		sr14ShowCLIRel: sr14ShowCLISHA,
	}}); err != nil {
		t.Fatal(err)
	}
	p.WriteRaw("wiki/.hidden.md", []byte(sr14ShowCanary))
	p.WriteRaw("wiki/-x.md", []byte(sr14ShowCanary))
	p.WriteRaw("wiki/a.txt", []byte(sr14ShowCanary))

	paths := []string{
		"../x.md", ".sync/state.json", "credentials.json", "wiki/.hidden.md",
		"wiki/-x.md", "wiki/a.txt", "wiki//x.md", "/etc/passwd.md",
		"config.json", ".contexo/credentials.json", ".contexo/.sync/state.json",
		filepath.Join(p.HubDir, "credentials.json"),
		filepath.Join(p.HubDir, ".sync", "state.json"),
		filepath.Join(p.Root, "x.md"),
		"index.md", "wiki/a\u0085b.md", "wiki/a\x1bb.md", "",
	}
	for _, path := range paths {
		for _, at := range []string{"base", "server", sr14ShowCLISHA} {
			code, stdout, stderr := sr14ShowExec(t, "show", path, "--at", at, "--root", p.Root)
			env := sr14ShowFailure(t, code, stdout, stderr, contract.ExitUsage, contract.CodeInvalidPath)
			if len(env.Error.Advice) != 0 {
				t.Errorf("show %q --at %s: advice = %q, want []", path, at, env.Error.Advice)
			}
			if strings.Contains(stdout, sr14ShowCanary) || strings.Contains(stderr, sr14ShowCanary) {
				t.Errorf("show %q --at %s printed the canary: stdout %q stderr %q", path, at, stdout, stderr)
			}
		}
	}
}

// The lead's acceptance command, in process: a root holding only an empty
// .contexo/ (no config.json, no credentials) and HOME at that root. The path
// rule runs before the hub, config or credentials are looked at.
func TestShow_InvalidPathInBareHub(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".contexo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	for _, at := range []string{"base", "server", sr14ShowCLISHA} {
		code, stdout, stderr := sr14ShowExec(t, "show", "../x.md", "--at", at, "--root", root)
		sr14ShowFailure(t, code, stdout, stderr, contract.ExitUsage, contract.CodeInvalidPath)
	}
	// Nothing was created on the way.
	entries, err := os.ReadDir(filepath.Join(root, ".contexo"))
	if err != nil || len(entries) != 0 {
		t.Errorf(".contexo holds %v (%v), want it still empty", entries, err)
	}
}

// stdout is the base file byte for byte: the BOM and CRLFs survive and no
// newline is added, also for a base that does not end in one; stderr stays
// empty. The path may be given as .contexo/<P> or as an absolute path.
func TestShow_RawBytesExact(t *testing.T) {
	p := testkit.NewProject(t, testkit.ProjectOptions{NoCreds: true})
	crlf := append([]byte("\xEF\xBB\xBF"), bytes.ReplaceAll(sr14ShowPage("CRLF body"), []byte("\n"), []byte("\r\n"))...)
	if !bytes.HasPrefix(crlf, []byte("\xEF\xBB\xBF---\r\n")) {
		t.Fatalf("fixture: %q", crlf)
	}
	sr14ShowSeedBase(t, p.HubDir, sr14ShowCLIRel, crlf, sr14ShowCLISHA)
	const bareRel = "wiki/concepts/b.md"
	bare := bytes.TrimRight(testkit.PageBytes(testkit.PageSpec{Slug: "b", Body: "no final newline"}), "\n")
	sr14ShowSeedBase(t, p.HubDir, bareRel, bare, sr14ShowCLISHA)

	for _, c := range []struct {
		arg  string
		want []byte
	}{
		{sr14ShowCLIRel, crlf},
		{".contexo/" + sr14ShowCLIRel, crlf},
		{filepath.Join(p.HubDir, "wiki", "concepts", "a.md"), crlf},
		{bareRel, bare},
	} {
		code, stdout, stderr := sr14ShowExec(t, "show", c.arg, "--at", "base", "--root", p.Root)
		sr14ShowSuccess(t, code, stdout, stderr, c.want)
	}
}

// Without --json every failure is still the one-line envelope with the
// contract exit (E20/E21), never human text and exit 1.
func TestShow_EnvelopeWithoutJSONFlag(t *testing.T) {
	p := testkit.NewProject(t, testkit.ProjectOptions{NoCreds: true, RepoID: "acme-kb", ServerURL: "http://127.0.0.1:9"})
	for _, c := range []struct {
		name   string
		args   []string
		exit   int
		code   contract.ErrorCode
		advice []string
	}{
		{"no base", []string{"show", sr14ShowCLIRel, "--at", "base"},
			contract.ExitNotFound, contract.CodeBaseUnavailable, []string{"ctx repair"}},
		{"bad --at", []string{"show", sr14ShowCLIRel, "--at", "HEAD"},
			contract.ExitUsage, contract.CodeUsage, []string{}},
		{"bad path", []string{"show", "../x.md", "--at", "base"},
			contract.ExitUsage, contract.CodeInvalidPath, []string{}},
		{"no credentials", []string{"show", sr14ShowCLIRel, "--at", "server"},
			contract.ExitAuth, contract.CodeNotAuthenticated, []string{"ctx auth login"}},
		{"unknown flag", []string{"show", sr14ShowCLIRel, "--at", "base", "--bogus"},
			contract.ExitUsage, contract.CodeUsage, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := sr14ShowExec(t, append(c.args, "--root", p.Root)...)
			env := sr14ShowFailure(t, code, stdout, stderr, c.exit, c.code)
			if c.advice != nil && !reflect.DeepEqual(env.Error.Advice, c.advice) {
				t.Errorf("advice = %q, want %q", env.Error.Advice, c.advice)
			}
			if env.Error.Message == "" {
				t.Error("envelope message is empty")
			}
		})
	}
}

func TestShow_MissingAtIsUsageExit3(t *testing.T) {
	p := testkit.NewProject(t, testkit.ProjectOptions{NoCreds: true})
	for _, args := range [][]string{
		{"show", sr14ShowCLIRel},
		{"show", sr14ShowCLIRel, "--at"},
		{"show", sr14ShowCLIRel, "--at="},
		{"show", sr14ShowCLIRel, "--at", ""},
		{"show", "--at", "base"},
		{"show", sr14ShowCLIRel, "wiki/concepts/b.md", "--at", "base"},
	} {
		code, stdout, stderr := sr14ShowExec(t, append(args, "--root", p.Root)...)
		sr14ShowFailure(t, code, stdout, stderr, contract.ExitUsage, contract.CodeUsage)
	}
}

// The network variants need credentials (4) and then a repo id (5); --at
// base reads only the hub and works without either.
func TestShow_NetworkVariantsNeedAuth(t *testing.T) {
	t.Run("no credentials", func(t *testing.T) {
		p := testkit.NewProject(t, testkit.ProjectOptions{NoCreds: true, RepoID: "acme-kb", ServerURL: "http://127.0.0.1:9"})
		for _, at := range []string{"server", sr14ShowCLISHA} {
			code, stdout, stderr := sr14ShowExec(t, "show", sr14ShowCLIRel, "--at", at, "--root", p.Root)
			env := sr14ShowFailure(t, code, stdout, stderr, contract.ExitAuth, contract.CodeNotAuthenticated)
			if !reflect.DeepEqual(env.Error.Advice, []string{"ctx auth login"}) {
				t.Errorf("--at %s: advice = %q", at, env.Error.Advice)
			}
		}

		base := sr14ShowPage("offline base")
		sr14ShowSeedBase(t, p.HubDir, sr14ShowCLIRel, base, sr14ShowCLISHA)
		code, stdout, stderr := sr14ShowExec(t, "show", sr14ShowCLIRel, "--at", "base", "--root", p.Root)
		sr14ShowSuccess(t, code, stdout, stderr, base)
	})
	t.Run("no repo id", func(t *testing.T) {
		p := testkit.NewProject(t, testkit.ProjectOptions{Key: "k", ServerURL: "http://127.0.0.1:9"})
		for _, at := range []string{"server", sr14ShowCLISHA} {
			code, stdout, stderr := sr14ShowExec(t, "show", sr14ShowCLIRel, "--at", at, "--root", p.Root)
			env := sr14ShowFailure(t, code, stdout, stderr, contract.ExitConfig, contract.CodeRepoNotConfigured)
			if !reflect.DeepEqual(env.Error.Advice, []string{"ctx remote set-repo <id>"}) {
				t.Errorf("--at %s: advice = %q", at, env.Error.Advice)
			}
		}
	})
	t.Run("no hub", func(t *testing.T) {
		wp01IsolateEnv(t)
		root := t.TempDir()
		for _, at := range []string{"base", "server", sr14ShowCLISHA} {
			code, stdout, stderr := sr14ShowExec(t, "show", sr14ShowCLIRel, "--at", at, "--root", root)
			env := sr14ShowFailure(t, code, stdout, stderr, contract.ExitConfig, contract.CodeNotInitialized)
			if !reflect.DeepEqual(env.Error.Advice, []string{"ctx init"}) {
				t.Errorf("--at %s: advice = %q", at, env.Error.Advice)
			}
		}
	})
}

// Against the real server: --at server and --at <sha> print exactly what
// pull would write (SanitizeContent of the pushed bytes), history comes back
// as history, and the 404s keep their E14 codes.
func TestShowE2E_Server(t *testing.T) {
	s := testkit.StartServer(t)
	p := testkit.NewProject(t, testkit.ProjectOptions{ServerURL: s.URL, Key: s.Key, RepoID: "sr14-show"})

	v1 := sr14ShowPage("first \x1b[2Jversion")
	s1 := sr14ShowPush(t, s, "sr14-show", sr14ShowCLIRel, v1)
	v2 := sr14ShowPage("second version with a \xe2\x80\xaebidi override")
	s2 := sr14ShowPush(t, s, "sr14-show", sr14ShowCLIRel, v2)
	want1 := []byte(schema.SanitizeContent(string(v1)))
	want2 := []byte(schema.SanitizeContent(string(v2)))
	if bytes.Equal(want1, v1) || bytes.Equal(want2, v2) {
		t.Fatal("fixture: nothing to sanitize")
	}

	for _, c := range []struct {
		at   string
		want []byte
	}{
		{"server", want2},
		{s2, want2},
		{s1, want1},
		{s1[:7], want1},
		{strings.ToUpper(s1), want1},
	} {
		code, stdout, stderr := sr14ShowExec(t, "show", sr14ShowCLIRel, "--at", c.at, "--root", p.Root)
		sr14ShowSuccess(t, code, stdout, stderr, c.want)
	}

	for _, c := range []struct {
		name, rel, at string
		code          contract.ErrorCode
	}{
		{"absent page at HEAD", "wiki/concepts/missing.md", "server", contract.CodePageNotFound},
		{"unknown sha", sr14ShowCLIRel, sr14ShowCLISHA, contract.CodeSHANotFound},
		{"absent page at a sha", "wiki/concepts/missing.md", s2, contract.CodeSHANotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := sr14ShowExec(t, "show", c.rel, "--at", c.at, "--root", p.Root)
			env := sr14ShowFailure(t, code, stdout, stderr, contract.ExitNotFound, c.code)
			if env.Error.HTTPStatus != 404 {
				t.Errorf("http_status = %d, want 404", env.Error.HTTPStatus)
			}
		})
	}

	// A repo the server does not have is repo_not_found for both network
	// variants, never page_not_found or sha_not_found (E14).
	other := testkit.NewProject(t, testkit.ProjectOptions{ServerURL: s.URL, Key: s.Key, RepoID: "sr14-no-such-repo"})
	for _, at := range []string{"server", s1} {
		code, stdout, stderr := sr14ShowExec(t, "show", sr14ShowCLIRel, "--at", at, "--root", other.Root)
		sr14ShowFailure(t, code, stdout, stderr, contract.ExitNotFound, contract.CodeRepoNotFound)
	}
}

// An old server ignores ?at and answers HEAD with 200: show <sha> must fail
// server_unsupported (exit 10, missing [pages.at]) and never print HEAD as
// history. --at server still works there.
func TestShowE2E_OldServer(t *testing.T) {
	s := testkit.StartOldServer(t)
	p := testkit.NewProject(t, testkit.ProjectOptions{ServerURL: s.URL, Key: s.Key, RepoID: "sr14-old"})
	v1 := sr14ShowPage("version one")
	s1 := sr14ShowPush(t, s, "sr14-old", sr14ShowCLIRel, v1)
	v2 := sr14ShowPage("version two at HEAD")
	sr14ShowPush(t, s, "sr14-old", sr14ShowCLIRel, v2)

	code, stdout, stderr := sr14ShowExec(t, "show", sr14ShowCLIRel, "--at", s1, "--root", p.Root)
	env := sr14ShowFailure(t, code, stdout, stderr, contract.ExitUnsupported, contract.CodeServerUnsupported)
	if !strings.Contains(stderr, `"details":{"missing":["pages.at"]}`) {
		t.Errorf("details.missing is not [pages.at]: %q", stderr)
	}
	if env.Error.HTTPStatus != 0 {
		t.Errorf("http_status = %d, want none (the old server answered 200)", env.Error.HTTPStatus)
	}

	code, stdout, stderr = sr14ShowExec(t, "show", sr14ShowCLIRel, "--at", "server", "--root", p.Root)
	sr14ShowSuccess(t, code, stdout, stderr, v2)
}
