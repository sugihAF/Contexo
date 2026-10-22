package cli

// WP-1.1 `ctx status --json [--offline]` CLI tests. They run ctx in-process
// through Execute (always with --root, which NewRootCmd resets) and never
// call t.Parallel: Execute writes the package-level rootDir and the tests
// isolate HOME with t.Setenv.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/syncops/contract"
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
