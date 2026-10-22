package e2e

// The shared harness: running ctx with a scratch environment, the stream
// assertions every scenario uses, and the fixture helpers. Its API is frozen
// for the scenario and golden files (helpers there are prefixed per file).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/capture"
	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
)

// clientName is CONTEXO_CLIENT for every ctx run; the server sees it as
// X-Contexo-Client.
const clientName = "contexo-e2e/1"

// defaultRepo is the repo id of every project user unless withRepo says
// otherwise.
const defaultRepo = "e2e"

// runTimeout bounds one ctx run.
const runTimeout = 2 * time.Minute

// ---------------------------------------------------------------- harness tests

func TestHarness_EnvIsolation(t *testing.T) {
	requireCtx(t)
	t.Setenv("CONTEXO_DISTILL_DISABLE", "1")
	t.Setenv("CONTEXO_LOCK_TIMEOUT", "1s")
	t.Setenv("CONTEXO_SERVER", "http://leak.invalid")
	u := newBareUser(t, "env")
	env := ctxEnv(u, nil)
	for _, kv := range env {
		if strings.HasPrefix(kv, "CONTEXO_DISTILL_DISABLE=") {
			t.Fatalf("ctx env inherited %s", kv)
		}
	}

	// A real child: the test binary as a helper process prints its env.
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = ctxEnv(u, []string{envHelper + "=printenv"})
	cmd.Dir = u.Home
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper process: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		got[strings.ToUpper(k)] = v
	}
	want := map[string]string{
		"HOME": u.Home, "USERPROFILE": u.Home,
		"APPDATA":         filepath.Join(u.Home, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(u.Home, "AppData", "Local"),
		"XDG_CONFIG_HOME": filepath.Join(u.Home, ".config"),
		"NO_COLOR":        "1", "CONTEXO_NO_UPDATE_CHECK": "1",
		"CONTEXO_CLIENT": clientName, "TERM": "dumb",
		"PATH":                     os.Getenv("PATH"),
		strings.ToUpper(envHelper): "printenv",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("child %s = %q, want %q", k, got[k], v)
		}
	}
	for k := range got {
		if strings.HasPrefix(k, "CONTEXO_") && want[k] == "" {
			t.Errorf("child inherited %s=%s", k, got[k])
		}
		if _, ok := want[k]; !ok && !harnessAllowedExtra(k) {
			t.Errorf("child env has unexpected %s=%s", k, got[k])
		}
	}
}

// harnessAllowedExtra lists variables the OS or the Go runtime may add to a
// child whose env is set explicitly.
func harnessAllowedExtra(k string) bool {
	switch k {
	case "SYSTEMROOT", "TEMP", "TMP", "__CF_USER_TEXT_ENCODING":
		return true
	}
	return false
}

func TestHarness_ArgvRoot(t *testing.T) {
	u := &e2eUser{Name: "a", Root: "/abs/root"}
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"version", "--json"}, []string{"version", "--json"}},
		{[]string{"status", "--json"}, []string{"status", "--json", "--root", "/abs/root"}},
		{[]string{"push", "--json", "--yes", "--", "wiki/concepts/a.md"},
			[]string{"push", "--json", "--yes", "--root", "/abs/root", "--", "wiki/concepts/a.md"}},
	}
	for _, c := range cases {
		if got := ctxArgv(u, c.args); !equalStrings(got, c.want) {
			t.Errorf("ctxArgv(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestHarness_StreamChecks(t *testing.T) {
	good := []byte("{\n  \"schema\": \"ctx.pull.v1\",\n  \"head\": \"" + strings.Repeat("a", 40) + "\",\n  \"written\": []\n}\n")
	if err := checkDocBytes(good); err != nil {
		t.Fatalf("good doc rejected: %v", err)
	}
	bad := map[string][]byte{
		"compact":        []byte(`{"schema":"ctx.pull.v1"}` + "\n"),
		"no newline":     []byte("{\n  \"schema\": \"x\"\n}"),
		"two docs":       []byte("{\n  \"a\": 1\n}\n{\n  \"a\": 1\n}\n"),
		"ansi":           []byte("{\n  \"a\": \"\x1b[31mx\"\n}\n"),
		"fractional":     []byte("{\n  \"t\": \"2026-01-01T00:00:00.5Z\"\n}\n"),
		"offset":         []byte("{\n  \"t\": \"2026-01-01T07:00:00+07:00\"\n}\n"),
		"short sha":      []byte("{\n  \"sha\": \"abc1234\"\n}\n"),
		"upper sha":      []byte("{\n  \"base_sha\": \"" + strings.Repeat("A", 40) + "\"\n}\n"),
		"short new_head": []byte("{\n  \"new_head\": \"abc\"\n}\n"),
	}
	for name, b := range bad {
		if err := checkDocBytes(b); err == nil {
			t.Errorf("%s: accepted %q", name, b)
		}
	}
	env := `{"schema":"ctx.error.v1","exit_code":12,"error":{"code":"busy","message":"m","advice":[],"retryable":true,"details":{"pid":1,"host":"h","command":"c","started_at":"2026-01-01T00:00:00Z"}}}`
	if _, err := parseEnvelopeLine([]byte(env+"\n"), 12); err != nil {
		t.Fatalf("good envelope rejected: %v", err)
	}
	badEnv := map[string]string{
		"null advice":   strings.Replace(env, `"advice":[]`, `"advice":null`, 1),
		"exit mismatch": strings.Replace(env, `"exit_code":12`, `"exit_code":7`, 1),
		"not retryable": strings.Replace(env, `"retryable":true`, `"retryable":false`, 1),
		"extra detail":  strings.Replace(env, `"command":"c"`, `"command":"c","x":1`, 1),
		"unknown code":  strings.Replace(env, `"busy"`, `"locked"`, 1),
		"two lines":     "noise\n" + env,
	}
	for name, s := range badEnv {
		if _, err := parseEnvelopeLine([]byte(s+"\n"), 12); err == nil {
			t.Errorf("%s: accepted %s", name, s)
		}
	}
}

// ---------------------------------------------------------------- users and runs

// e2eUser is one person running ctx: an isolated HOME and, for project
// users, a testkit project at Root.
type e2eUser struct {
	Name string
	Home string // isolated HOME / USERPROFILE
	Root string // absolute project root (a bare user's has no .contexo)
	Hub  string // Root/.contexo
	P    *testkit.Project
	// Env holds extra KEY=VALUE entries for every run, always explicit.
	Env []string
}

// ctxResult is one ctx process run.
type ctxResult struct {
	Args   []string // argv as run
	Stdout []byte
	Stderr []byte
	Exit   int
}

func (r ctxResult) String() string {
	return fmt.Sprintf("ctx %q: exit %d\nstdout: %s\nstderr: %s", r.Args, r.Exit, r.Stdout, r.Stderr)
}

// ctxArgv returns args with "--root <u.Root>" added: before a "--"
// separator when there is one, otherwise at the end. version gets no
// --root (E25).
func ctxArgv(u *e2eUser, args []string) []string {
	if len(args) > 0 && args[0] == "version" || u == nil || u.Root == "" {
		return append([]string(nil), args...)
	}
	for i, a := range args {
		if a == "--" {
			out := append([]string(nil), args[:i]...)
			out = append(out, "--root", u.Root)
			return append(out, args[i:]...)
		}
	}
	return append(append([]string(nil), args...), "--root", u.Root)
}

// ctxEnv builds a ctx child environment from scratch: never os.Environ, no
// inherited CONTEXO_* variable. extra entries come last.
func ctxEnv(u *e2eUser, extra []string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + u.Home,
		"USERPROFILE=" + u.Home,
		"APPDATA=" + filepath.Join(u.Home, "AppData", "Roaming"),
		"LOCALAPPDATA=" + filepath.Join(u.Home, "AppData", "Local"),
		"XDG_CONFIG_HOME=" + filepath.Join(u.Home, ".config"),
		"NO_COLOR=1",
		"CONTEXO_NO_UPDATE_CHECK=1",
		"CONTEXO_CLIENT=" + clientName,
		"TERM=dumb",
	}
	if runtime.GOOS == "windows" {
		// A Go child without SystemRoot fails network init.
		for _, k := range []string{"SystemRoot", "TEMP", "TMP"} {
			if v, ok := os.LookupEnv(k); ok {
				env = append(env, k+"="+v)
			}
		}
	}
	env = append(env, u.Env...)
	return append(env, extra...)
}

// runCtx runs ctx as u with args (plus --root) and returns the result.
func runCtx(t testing.TB, u *e2eUser, args ...string) ctxResult {
	t.Helper()
	return runCtxEnv(t, u, nil, args...)
}

// runCtxEnv is runCtx with extra env entries for this run only (for
// example CONTEXO_DISTILL_DISABLE=1 for the one E24 case).
func runCtxEnv(t testing.TB, u *e2eUser, extraEnv []string, args ...string) ctxResult {
	t.Helper()
	bin := requireCtx(t)
	r, err := execCtx(bin, u, extraEnv, args...)
	if err != nil {
		t.Fatalf("run ctx %q: %v", r.Args, err)
	}
	return r
}

// execCtx runs ctx without touching a testing.TB, so a proxy hook on
// another goroutine may call it.
func execCtx(bin string, u *e2eUser, extraEnv []string, args ...string) (ctxResult, error) {
	r := ctxResult{Args: ctxArgv(u, args)}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, r.Args...)
	cmd.Env = ctxEnv(u, extraEnv)
	cmd.Dir = u.Home
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r.Stdout, r.Stderr = out.Bytes(), errb.Bytes()
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return r, err
		}
		if ctx.Err() != nil {
			return r, fmt.Errorf("timed out after %s: %w", runTimeout, ctx.Err())
		}
		r.Exit = ee.ExitCode()
	}
	return r, nil
}

// ---------------------------------------------------------------- assertions

var (
	reStrictTime = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)
	reTimeLike   = regexp.MustCompile(`^\d{4}-\d\d-\d\dT`)
	reFullSHA    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// shaKeys are the JSON keys whose value is a commit sha (40-hex).
var shaKeys = map[string]bool{
	"sha": true, "base_sha": true, "server_sha": true, "old_sha": true, "new_sha": true,
	"expected_sha": true, "head": true, "new_head": true, "last_pull_sha": true,
}

// assertOK checks a successful run: exit 0, stdout exactly one JSON
// document (2-space indent, trailing newline, no ANSI, whole-second UTC
// times, 40-hex shas), stderr empty. A non-nil v receives the strictly
// decoded document (DisallowUnknownFields).
func assertOK(t testing.TB, r ctxResult, v any) {
	t.Helper()
	if r.Exit != 0 {
		t.Fatalf("want exit 0\n%s", r)
	}
	if len(r.Stderr) != 0 {
		t.Fatalf("stderr must be empty on success\n%s", r)
	}
	if err := checkDocBytes(r.Stdout); err != nil {
		t.Fatalf("stdout document: %v\n%s", err, r)
	}
	if v != nil {
		if err := strictDecode(r.Stdout, v); err != nil {
			t.Fatalf("strict decode into %T: %v\n%s", v, err, r)
		}
	}
}

// checkDocBytes checks the stdout document format (E16 times and full shas
// are checked on the raw bytes, before any redaction).
func checkDocBytes(b []byte) error {
	if bytes.ContainsRune(b, 0x1b) {
		return errors.New("contains an ANSI escape")
	}
	if !bytes.HasSuffix(b, []byte("\n")) {
		return errors.New("no trailing newline")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		return fmt.Errorf("not one JSON document: %w", err)
	}
	var ind bytes.Buffer
	if err := json.Indent(&ind, compact.Bytes(), "", "  "); err != nil {
		return err
	}
	ind.WriteByte('\n')
	if !bytes.Equal(ind.Bytes(), b) {
		return errors.New("not 2-space indented with one trailing newline")
	}
	return checkValues(b)
}

// checkValues walks a JSON value: every RFC 3339-looking string must be a
// whole-second UTC time and every sha key a full lower-case 40-hex sha.
func checkValues(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	var errs []string
	var walk func(at, key string, v any)
	walk = func(at, key string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				walk(at+"."+k, k, c)
			}
		case []any:
			for i, c := range x {
				walk(fmt.Sprintf("%s[%d]", at, i), key, c)
			}
		case string:
			if reTimeLike.MatchString(x) && !reStrictTime.MatchString(x) {
				errs = append(errs, fmt.Sprintf("%s = %q is not a whole-second UTC time (E16)", at, x))
			}
			if shaKeys[key] && x != "" && !reFullSHA.MatchString(x) {
				errs = append(errs, fmt.Sprintf("%s = %q is not a 40-hex sha", at, x))
			}
		}
	}
	walk("$", "", v)
	if len(errs) > 0 {
		sort.Strings(errs)
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// strictDecode decodes exactly one JSON value into v, rejecting unknown
// fields and trailing data.
func strictDecode(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing data after the document")
	}
	return nil
}

// envelope is a parsed, checked ctx.error.v1 line.
type envelope struct {
	Line       []byte
	ExitCode   int
	Code       contract.ErrorCode
	Message    string
	Advice     []string
	Retryable  bool
	HTTPStatus int
	// Details is the typed pointer from contract.NewDetails(Code), or nil
	// when the envelope has no details.
	Details any
}

type wireEnvelope struct {
	Schema   string `json:"schema"`
	ExitCode int    `json:"exit_code"`
	Error    struct {
		Code       contract.ErrorCode `json:"code"`
		Message    string             `json:"message"`
		Advice     *[]string          `json:"advice"`
		Retryable  bool               `json:"retryable"`
		HTTPStatus int                `json:"http_status,omitempty"`
		Details    json.RawMessage    `json:"details,omitempty"`
	} `json:"error"`
}

// parseEnvelopeLine checks that stderr is exactly one non-empty line
// holding a ctx.error.v1 envelope for exit, and returns it.
func parseEnvelopeLine(stderr []byte, exit int) (*envelope, error) {
	line, ok := bytes.CutSuffix(stderr, []byte("\n"))
	if !ok || len(bytes.TrimSpace(line)) == 0 || bytes.ContainsAny(line, "\r\n") {
		return nil, fmt.Errorf("stderr is not exactly one non-empty line: %q", stderr)
	}
	if bytes.ContainsRune(line, 0x1b) {
		return nil, errors.New("envelope contains an ANSI escape")
	}
	var w wireEnvelope
	if err := strictDecode(line, &w); err != nil {
		return nil, fmt.Errorf("strict decode: %w", err)
	}
	if err := checkValues(line); err != nil {
		return nil, err
	}
	e := &envelope{Line: line, ExitCode: w.ExitCode, Code: w.Error.Code, Message: w.Error.Message,
		Retryable: w.Error.Retryable, HTTPStatus: w.Error.HTTPStatus}
	switch {
	case w.Schema != contract.SchemaError:
		return nil, fmt.Errorf("schema %q", w.Schema)
	case !contract.KnownCode(e.Code):
		return nil, fmt.Errorf("unknown code %q", e.Code)
	case w.ExitCode != exit:
		return nil, fmt.Errorf("exit_code %d != process exit %d", w.ExitCode, exit)
	case w.ExitCode != contract.ExitCodeFor(e.Code):
		return nil, fmt.Errorf("exit_code %d != ExitCodeFor(%s) = %d", w.ExitCode, e.Code, contract.ExitCodeFor(e.Code))
	case e.Retryable != contract.Retryable(e.Code):
		return nil, fmt.Errorf("retryable %v != Retryable(%s)", e.Retryable, e.Code)
	case w.Error.Advice == nil:
		return nil, errors.New("advice is null or missing")
	case e.Message == "":
		return nil, errors.New("empty message")
	}
	e.Advice = *w.Error.Advice
	if len(w.Error.Details) > 0 {
		d := contract.NewDetails(e.Code)
		if d == nil {
			return nil, fmt.Errorf("code %s carries details %s but has no details type", e.Code, w.Error.Details)
		}
		if err := strictDecode(w.Error.Details, d); err != nil {
			return nil, fmt.Errorf("details: %w", err)
		}
		e.Details = d
	}
	return e, nil
}

// assertFail checks a failed run: exit == ExitCodeFor(code), stdout empty,
// stderr exactly one envelope line with that code.
func assertFail(t testing.TB, r ctxResult, code contract.ErrorCode) *envelope {
	t.Helper()
	if want := contract.ExitCodeFor(code); r.Exit != want {
		t.Fatalf("want exit %d (%s)\n%s", want, code, r)
	}
	if len(r.Stdout) != 0 {
		t.Fatalf("stdout must be empty on failure\n%s", r)
	}
	return assertEnvelope(t, r, code)
}

// assertEnvelope checks only stderr (for push exit 6, whose stdout holds
// the document too).
func assertEnvelope(t testing.TB, r ctxResult, code contract.ErrorCode) *envelope {
	t.Helper()
	e, err := parseEnvelopeLine(r.Stderr, r.Exit)
	if err != nil {
		t.Fatalf("envelope: %v\n%s", err, r)
	}
	if e.Code != code {
		t.Fatalf("envelope code %s, want %s\n%s", e.Code, code, r)
	}
	return e
}

// assertPushConflict checks push exit 6: a PushDoc on stdout AND a conflict
// envelope on stderr.
func assertPushConflict(t testing.TB, r ctxResult) (contract.PushDoc, *envelope) {
	t.Helper()
	if r.Exit != contract.ExitConflict {
		t.Fatalf("want exit 6\n%s", r)
	}
	if err := checkDocBytes(r.Stdout); err != nil {
		t.Fatalf("stdout document: %v\n%s", err, r)
	}
	var doc contract.PushDoc
	if err := strictDecode(r.Stdout, &doc); err != nil {
		t.Fatalf("strict decode PushDoc: %v\n%s", err, r)
	}
	return doc, assertEnvelope(t, r, contract.CodeConflict)
}

// assertNoEnvelope checks that neither stream carries a ctx.error.v1 line
// (human mode, E21).
func assertNoEnvelope(t testing.TB, r ctxResult) {
	t.Helper()
	for _, s := range [][]byte{r.Stdout, r.Stderr} {
		if bytes.Contains(s, []byte(contract.SchemaError)) {
			t.Fatalf("human mode printed a %s envelope\n%s", contract.SchemaError, r)
		}
	}
}

// ---------------------------------------------------------------- servers

// e2eServer is an in-process testkit server behind a recording proxy.
// Users talk to URL (the proxy).
type e2eServer struct {
	TK    *testkit.Server
	Proxy *recordingProxy
	URL   string
	Key   string
	Old   bool
}

// startServer starts a current (contract v1) testkit server.
func startServer(t testing.TB) *e2eServer {
	t.Helper()
	tk := testkit.StartServer(t)
	px := startRecordingProxy(t, tk.URL)
	return &e2eServer{TK: tk, Proxy: px, URL: px.URL, Key: tk.Key}
}

// startOldServer starts a pre-v1 server (testkit.StartOldServer).
func startOldServer(t testing.TB) *e2eServer {
	t.Helper()
	tk := testkit.StartOldServer(t)
	px := startRecordingProxy(t, tk.URL)
	return &e2eServer{TK: tk, Proxy: px, URL: px.URL, Key: tk.Key, Old: true}
}

// serverSHA returns the last commit touching rel in repo ("" when the path
// or the whole repo is absent).
func serverSHA(t testing.TB, s *e2eServer, repo, rel string) string {
	t.Helper()
	if !s.TK.Store.Exists(repo) {
		return ""
	}
	pages, err := s.TK.Store.ListPages(repo)
	if err != nil {
		t.Fatalf("server list pages: %v", err)
	}
	for _, p := range pages {
		if p.Path == rel {
			return p.SHA
		}
	}
	return ""
}

// serverContent returns rel's HEAD bytes in repo.
func serverContent(t testing.TB, s *e2eServer, repo, rel string) []byte {
	t.Helper()
	b, _, err := s.TK.Store.Read(repo, rel)
	if err != nil {
		t.Fatalf("server read %s: %v", rel, err)
	}
	return b
}

// serverHead returns repo's HEAD commit.
func serverHead(t testing.TB, s *e2eServer, repo string) string {
	t.Helper()
	h, err := s.TK.Store.HeadSHA(repo)
	if err != nil {
		t.Fatalf("server head: %v", err)
	}
	return h
}

// proxyRecord is one request seen by a recordingProxy.
type proxyRecord struct {
	Method    string
	Path      string
	RawQuery  string
	Client    string   // request X-Contexo-Client
	Caps      string   // response X-Contexo-Capabilities
	Status    int      // response status (502 when the upstream failed)
	PushPaths []string // files[].path of a POST .../sync/push
	PushFiles []pushFile
}

// pushFile is one files[] element of a recorded push body (content left
// out).
type pushFile struct {
	Path         string `json:"path"`
	ParentSHA    string `json:"parent_sha"`
	ExpectAbsent bool   `json:"expect_absent"`
}

// recordingProxy is an httputil.ReverseProxy in front of a server that
// records every request.
type recordingProxy struct {
	URL string

	mu        gosync.Mutex
	recs      []proxyRecord
	afterList func()
}

type pushPathsKey struct{}

func startRecordingProxy(t testing.TB, target string) *recordingProxy {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	px := &recordingProxy{}
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.ModifyResponse = func(resp *http.Response) error {
		r := resp.Request
		rec := proxyRecord{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
			Client: r.Header.Get("X-Contexo-Client"), Caps: resp.Header.Get("X-Contexo-Capabilities"),
			Status: resp.StatusCode}
		if pf, ok := r.Context().Value(pushPathsKey{}).([]pushFile); ok {
			rec.PushFiles = pf
			rec.PushPaths = []string{}
			for _, f := range pf {
				rec.PushPaths = append(rec.PushPaths, f.Path)
			}
		}
		var hook func()
		px.mu.Lock()
		px.recs = append(px.recs, rec)
		if isListing(r) && px.afterList != nil {
			hook, px.afterList = px.afterList, nil
		}
		px.mu.Unlock()
		if hook != nil {
			// The listing is complete upstream; hold it until the hook (a
			// teammate's push) is done, then deliver the stale listing.
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				return err
			}
			resp.Body = io.NopCloser(bytes.NewReader(body))
			hook()
		}
		return nil
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		px.mu.Lock()
		px.recs = append(px.recs, proxyRecord{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
			Client: r.Header.Get("X-Contexo-Client"), Status: http.StatusBadGateway})
		px.mu.Unlock()
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sync/push") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			var req struct {
				Files []pushFile `json:"files"`
			}
			files := []pushFile{}
			if json.Unmarshal(body, &req) == nil {
				files = append(files, req.Files...)
			}
			r = r.WithContext(context.WithValue(r.Context(), pushPathsKey{}, files))
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	px.URL = ts.URL
	return px
}

// isListing reports whether r is GET /v1/repos/<id>/pages (the listing).
func isListing(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return len(parts) == 4 && parts[0] == "v1" && parts[1] == "repos" && parts[3] == "pages"
}

// Records returns a copy of the recorded requests, oldest first.
func (px *recordingProxy) Records() []proxyRecord {
	px.mu.Lock()
	defer px.mu.Unlock()
	return append([]proxyRecord(nil), px.recs...)
}

// Reset forgets the recorded requests and any pending SetAfterList hook.
func (px *recordingProxy) Reset() {
	px.mu.Lock()
	px.recs = nil
	px.afterList = nil
	px.mu.Unlock()
}

// Count returns how many recorded requests had method and a path ending in
// suffix.
func (px *recordingProxy) Count(method, suffix string) int {
	n := 0
	for _, r := range px.Records() {
		if r.Method == method && strings.HasSuffix(r.Path, suffix) {
			n++
		}
	}
	return n
}

// Listings returns how many listing requests were recorded.
func (px *recordingProxy) Listings() int {
	n := 0
	for _, r := range px.Records() {
		if r.Method == http.MethodGet {
			parts := strings.Split(strings.Trim(r.Path, "/"), "/")
			if len(parts) == 4 && parts[3] == "pages" {
				n++
			}
		}
	}
	return n
}

// PushedPaths returns every files[].path the proxy saw pushed, in order.
func (px *recordingProxy) PushedPaths() []string {
	var out []string
	for _, r := range px.Records() {
		out = append(out, r.PushPaths...)
	}
	return out
}

// PushedFiles returns every recorded push files[] element, in order.
func (px *recordingProxy) PushedFiles() []pushFile {
	var out []pushFile
	for _, r := range px.Records() {
		out = append(out, r.PushFiles...)
	}
	return out
}

// SetAfterList installs a one-shot hook that runs after the next listing
// response arrives from the server and before it reaches the client. The
// hook runs on the proxy goroutine: it must not call t, and it must hand
// its result back over a channel (a child process in between is no Go
// happens-before edge, so a shared variable is a data race).
func (px *recordingProxy) SetAfterList(fn func()) {
	px.mu.Lock()
	px.afterList = fn
	px.mu.Unlock()
}

// startUpstream starts a synthetic upstream server with handler h and
// returns its URL.
func startUpstream(t testing.TB, h http.HandlerFunc) string {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL
}

// freeClosedPort returns http://127.0.0.1:<port> for a port nothing listens
// on.
func freeClosedPort(t testing.TB) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}

// ---------------------------------------------------------------- projects

type userCfg struct {
	repo        string
	pages       []testkit.PageSpec
	noCreds     bool
	globalCreds bool
	key         *string
	serverURL   *string
}

// userOpt configures newUser.
type userOpt func(*userCfg)

// withRepo sets the project's repo_id ("" leaves it unset).
func withRepo(id string) userOpt { return func(c *userCfg) { c.repo = id } }

// withPages writes these pages into the new project.
func withPages(specs ...testkit.PageSpec) userOpt {
	return func(c *userCfg) { c.pages = append(c.pages, specs...) }
}

// withNoCreds writes no credentials at all.
func withNoCreds() userOpt { return func(c *userCfg) { c.noCreds = true } }

// withGlobalCreds writes the credentials to the user's HOME instead of the
// project.
func withGlobalCreds() userOpt { return func(c *userCfg) { c.globalCreds = true } }

// withKey sets the credentials token (default: the server's key).
func withKey(k string) userOpt { return func(c *userCfg) { c.key = &k } }

// withServerURL sets the config and credentials server_url (default: the
// server's proxy URL).
func withServerURL(u string) userOpt { return func(c *userCfg) { c.serverURL = &u } }

// newUser creates a project user wired to srv (nil: no server; use
// withServerURL). Credentials are the ones testkit.NewProject writes.
func newUser(t testing.TB, srv *e2eServer, name string, opts ...userOpt) *e2eUser {
	t.Helper()
	c := userCfg{repo: defaultRepo}
	for _, o := range opts {
		o(&c)
	}
	var serverURL, key string
	if srv != nil {
		serverURL, key = srv.URL, srv.Key
	}
	if c.serverURL != nil {
		serverURL = *c.serverURL
	}
	if c.key != nil {
		key = *c.key
	}
	p := testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: serverURL, Key: key, RepoID: c.repo, Pages: c.pages,
		GlobalCreds: c.globalCreds, NoCreds: c.noCreds,
	})
	// NewProject moved HOME (t.Setenv) to this user's fresh home.
	home := os.Getenv("HOME")
	if home == "" {
		t.Fatal("testkit.NewProject left HOME empty")
	}
	return &e2eUser{Name: name, Home: home, Root: p.Root, Hub: p.HubDir, P: p}
}

// newBareUser is a user with an empty HOME and a Root with no .contexo.
func newBareUser(t testing.TB, name string) *e2eUser {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &e2eUser{Name: name, Home: home, Root: root, Hub: config.ContexoDirPath(root)}
}

// writeGlobalCreds writes u's ~/.contexo/credentials.json directly.
func writeGlobalCreds(t testing.TB, u *e2eUser, c config.Credentials) {
	t.Helper()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(u.Home, config.ContexoDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// conceptPage and entityPage build page specs with a fixed body.
func conceptPage(slug, body string) testkit.PageSpec {
	return testkit.PageSpec{Type: schema.TypeConcept, Slug: slug, Title: slug, Body: body}
}

func entityPage(slug, body string) testkit.PageSpec {
	return testkit.PageSpec{Type: schema.TypeEntity, Slug: slug, Title: slug, Body: body}
}

// pageRel is spec's path relative to .contexo.
func pageRel(spec testkit.PageSpec) string {
	typ := spec.Type
	if typ == "" {
		typ = schema.TypeConcept
	}
	fm := schema.PageFrontmatter{Type: typ, Slug: spec.Slug}
	return fm.RelPath()
}

// editPage appends line to rel's body, keeping the page valid.
func editPage(t testing.TB, u *e2eUser, rel, line string) {
	t.Helper()
	b := u.P.Read(rel)
	u.P.WriteRaw(rel, append(b, []byte("\n"+line+"\n")...))
}

// Broken-file kinds for writeBroken.
const (
	brokenParse        = "parse"
	brokenPathMismatch = "path_mismatch"
)

// writeBroken writes an invalid page and returns its path: brokenParse
// fails ParsePage; brokenPathMismatch is a valid page whose frontmatter
// RelPath (wiki/concepts/relocated.md) differs from its file path.
func writeBroken(t testing.TB, u *e2eUser, kind string) string {
	t.Helper()
	switch kind {
	case brokenParse:
		rel := "wiki/concepts/broken-parse.md"
		u.P.WriteRaw(rel, []byte("---\nschema: ctx.page.v1\nslug: [unclosed\n---\n# Broken\n"))
		return rel
	case brokenPathMismatch:
		rel := "wiki/concepts/misplaced.md"
		u.P.WriteRaw(rel, testkit.PageBytes(conceptPage("relocated", "Lives at the wrong path.")))
		return rel
	}
	t.Fatalf("writeBroken: unknown kind %q", kind)
	return ""
}

// captureTime is the fixed turn timestamp of seeded capture buffers.
const captureTime = "2026-01-01T00:00:00Z"

// seedCapture writes a pending capture buffer with turns turns (mtime now,
// so it is fresh).
func seedCapture(t testing.TB, u *e2eUser, sessionID string, turns int) {
	t.Helper()
	b := capture.Open(u.Hub, sessionID)
	for i := 1; i <= turns; i++ {
		err := b.AppendTurn(capture.TurnRecord{Timestamp: captureTime, Turn: i,
			User: fmt.Sprintf("question %d", i), Assistant: fmt.Sprintf("answer %d", i)})
		if err != nil {
			t.Fatalf("seed capture: %v", err)
		}
	}
}

// ---------------------------------------------------------------- sync state

// loadState reads u's state.json.
func loadState(t testing.TB, u *e2eUser) *sync.State {
	t.Helper()
	st, err := sync.LoadState(u.Hub)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	return st
}

// setPageSHA writes page_shas[rel] = sha ("" deletes it), as an old binary
// would, without touching bases.json.
func setPageSHA(t testing.TB, u *e2eUser, rel, sha string) {
	t.Helper()
	st := loadState(t, u)
	if sha == "" {
		delete(st.PageSHAs, rel)
	} else {
		st.PageSHAs[rel] = sha
	}
	if err := sync.SaveState(u.Hub, st); err != nil {
		t.Fatalf("save state: %v", err)
	}
}

// baseRecord is one bases.json entry.
type baseRecord struct {
	SHA      string `json:"sha"`
	Hash     string `json:"hash"`
	SyncedAt string `json:"synced_at"`
}

// readBases strictly reads u's bases.json (empty when absent).
func readBases(t testing.TB, u *e2eUser) map[string]baseRecord {
	t.Helper()
	b, err := os.ReadFile(syncstate.BasesPath(u.Hub))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]baseRecord{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var idx struct {
		Schema string                `json:"schema"`
		Pages  map[string]baseRecord `json:"pages"`
	}
	if err := strictDecode(b, &idx); err != nil {
		t.Fatalf("bases.json: %v\n%s", err, b)
	}
	if idx.Pages == nil {
		idx.Pages = map[string]baseRecord{}
	}
	return idx.Pages
}

// conflictRecord is one conflicts.json entry.
type conflictRecord struct {
	ServerSHA  string `json:"server_sha"`
	BaseSHA    string `json:"base_sha,omitempty"`
	Kind       string `json:"kind"`
	Source     string `json:"source"`
	DetectedAt string `json:"detected_at"`
	HasContent bool   `json:"has_content"`
}

// readConflicts strictly reads u's conflicts.json (empty when absent).
func readConflicts(t testing.TB, u *e2eUser) map[string]conflictRecord {
	t.Helper()
	b, err := os.ReadFile(syncstate.ConflictsPath(u.Hub))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]conflictRecord{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var idx struct {
		Schema    string                    `json:"schema"`
		Conflicts map[string]conflictRecord `json:"conflicts"`
	}
	if err := strictDecode(b, &idx); err != nil {
		t.Fatalf("conflicts.json: %v\n%s", err, b)
	}
	if idx.Conflicts == nil {
		idx.Conflicts = map[string]conflictRecord{}
	}
	return idx.Conflicts
}

// holdLock takes u's .sync/lock in this (live) process through
// syncstate.Lock and releases it at cleanup; the returned func releases it
// early.
func holdLock(t testing.TB, u *e2eUser, command string) func() {
	t.Helper()
	unlock, err := syncstate.Lock(u.Hub, command, 0)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	t.Cleanup(unlock)
	return unlock
}

// ---------------------------------------------------------------- common runs

// mustStatus runs status --json (plus --offline) and decodes it.
func mustStatus(t testing.TB, u *e2eUser, offline bool) contract.StatusDoc {
	t.Helper()
	args := []string{"status", "--json"}
	if offline {
		args = append(args, "--offline")
	}
	var doc contract.StatusDoc
	assertOK(t, runCtx(t, u, args...), &doc)
	return doc
}

// mustPush runs push --json plus args and decodes the document (exit 0).
func mustPush(t testing.TB, u *e2eUser, args ...string) contract.PushDoc {
	t.Helper()
	var doc contract.PushDoc
	assertOK(t, runCtx(t, u, append([]string{"push", "--json"}, args...)...), &doc)
	return doc
}

// mustPull runs pull --json and decodes the document.
func mustPull(t testing.TB, u *e2eUser) contract.PullDoc {
	t.Helper()
	var doc contract.PullDoc
	assertOK(t, runCtx(t, u, "pull", "--json"), &doc)
	return doc
}

// findPage returns doc's entry for rel, or nil.
func findPage(doc contract.StatusDoc, rel string) *contract.PageEntry {
	for i := range doc.Pages {
		if doc.Pages[i].Path == rel {
			return &doc.Pages[i]
		}
	}
	return nil
}

// mustPage is findPage that fails the test when rel is missing.
func mustPage(t testing.TB, doc contract.StatusDoc, rel string) *contract.PageEntry {
	t.Helper()
	p := findPage(doc, rel)
	if p == nil {
		t.Fatalf("status has no entry for %s", rel)
	}
	return p
}

// readHubFile returns the bytes of rel under u's hub (nil when absent).
func readHubFile(t testing.TB, u *e2eUser, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(u.Hub, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---------------------------------------------------------------- small utils

func equalStrings(a, b []string) bool {
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

func sortStrings(s []string) { sort.Strings(s) }

// lines splits b into its non-empty lines.
func lines(b []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if l := strings.TrimRight(sc.Text(), "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}
