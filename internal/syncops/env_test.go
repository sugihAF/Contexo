package syncops

// Frozen after wave 0 (WP-0.1): env.go and errors.go behaviour every wave-1
// engine relies on. Add cases, never weaken them.

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// wp01ContractError asserts err is (or wraps) a *contract.Error and returns it.
func wp01ContractError(t *testing.T, err error) *contract.Error {
	t.Helper()
	var ce *contract.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v (%T) is not a *contract.Error", err, err)
	}
	return ce
}

// wp01StubEnv is an offline Env over an initialised temp root, for the
// wave-0 engine stub tests (status_test.go ... repair_test.go). The helpers
// live here, in a frozen file, so an owner who rewrites its stub test never
// breaks another owner's file.
func wp01StubEnv(t *testing.T) Env {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".contexo"), 0o755); err != nil {
		t.Fatal(err)
	}
	return NewEnv(root, "acme-kb", "https://kb.acme.dev", Author{Name: "Ana", Email: "ana@acme.dev"}, nil)
}

// wp01AssertNotImplemented asserts err is the wave-0 stub error for op:
// code not_implemented (exit 10), advice ["ctx update"], op in the message.
func wp01AssertNotImplemented(t *testing.T, err error, op string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s stub returned a nil error, want not_implemented", op)
	}
	ce := wp01ContractError(t, err)
	if ce.Code != contract.CodeNotImplemented {
		t.Fatalf("%s stub: code = %q, want %q", op, ce.Code, contract.CodeNotImplemented)
	}
	if got := contract.ExitCodeFor(ce.Code); got != 10 {
		t.Fatalf("%s stub: exit = %d, want 10", op, got)
	}
	if !reflect.DeepEqual(ce.Advice, []string{"ctx update"}) {
		t.Fatalf("%s stub: advice = %#v, want [\"ctx update\"]", op, ce.Advice)
	}
	if !strings.Contains(ce.Message, op) {
		t.Fatalf("%s stub: message %q does not name the op", op, ce.Message)
	}
}

func TestNewEnvNormalizesTypedNil(t *testing.T) {
	root := t.TempDir()
	a := Author{Name: "Ana", Email: "ana@acme.dev"}

	var typedNil *sync.Client
	env := NewEnv(root, "acme-kb", "https://kb.acme.dev", a, typedNil)
	if env.Remote != nil {
		t.Fatalf("NewEnv kept a typed-nil *sync.Client: Remote = %#v, want nil interface", env.Remote)
	}
	if env.Online() {
		t.Fatal("Online() = true for a typed-nil client, want false")
	}

	if got := NewEnv(root, "acme-kb", "", a, nil); got.Remote != nil || got.Online() {
		t.Fatalf("nil Remote: Remote = %#v, Online = %v; want nil, false", got.Remote, got.Online())
	}

	c := sync.NewClient("http://127.0.0.1:1", "test-token")
	env = NewEnv(root, "acme-kb", "https://kb.acme.dev", a, c)
	if env.Remote != Remote(c) {
		t.Fatalf("Remote = %#v, want the client passed in", env.Remote)
	}
	if !env.Online() {
		t.Fatal("Online() = false with a real client, want true")
	}
	if env.Root != root || env.HubDir != filepath.Join(root, ".contexo") ||
		env.RepoID != "acme-kb" || env.ServerURL != "https://kb.acme.dev" || env.Author != a {
		t.Fatalf("NewEnv fields = %+v", env)
	}
}

func TestNewEnvDefaultsNow(t *testing.T) {
	env := NewEnv(t.TempDir(), "", "", Author{}, nil)
	if env.Now == nil {
		t.Fatal("NewEnv left Now nil")
	}
	before := time.Now()
	got := env.Now()
	after := time.Now()
	if got.Before(before.Add(-time.Second)) || got.After(after.Add(time.Second)) {
		t.Fatalf("Now() = %v, want the wall clock (between %v and %v)", got, before, after)
	}
}

func TestRequireHub(t *testing.T) {
	t.Run("missing hub", func(t *testing.T) {
		root := t.TempDir()
		hub, err := RequireHub(root)
		wp01AssertNotInitialized(t, hub, err)
	})
	t.Run("missing root", func(t *testing.T) {
		hub, err := RequireHub(filepath.Join(t.TempDir(), "does-not-exist"))
		wp01AssertNotInitialized(t, hub, err)
	})
	t.Run("file named .contexo", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".contexo"), []byte("not a dir\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		hub, err := RequireHub(root)
		wp01AssertNotInitialized(t, hub, err)
	})
	t.Run("ok", func(t *testing.T) {
		root := t.TempDir()
		want := filepath.Join(root, ".contexo")
		if err := os.Mkdir(want, 0o755); err != nil {
			t.Fatal(err)
		}
		hub, err := RequireHub(root)
		if err != nil {
			t.Fatalf("RequireHub: %v", err)
		}
		if hub != want {
			t.Fatalf("hub = %q, want %q", hub, want)
		}
	})
}

func wp01AssertNotInitialized(t *testing.T, hub string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("RequireHub succeeded with hub %q, want not_initialized", hub)
	}
	if hub != "" {
		t.Fatalf("hub = %q on error, want \"\"", hub)
	}
	ce := wp01ContractError(t, err)
	if ce.Code != contract.CodeNotInitialized {
		t.Fatalf("code = %q, want %q", ce.Code, contract.CodeNotInitialized)
	}
	if !reflect.DeepEqual(ce.Advice, []string{"ctx init"}) {
		t.Fatalf("advice = %#v, want [\"ctx init\"]", ce.Advice)
	}
	if got := contract.ExitCodeFor(ce.Code); got != contract.ExitConfig {
		t.Fatalf("exit = %d, want %d", got, contract.ExitConfig)
	}
	if ce.Message == "" {
		t.Fatal("empty message")
	}
}

func TestNotImplementedCode(t *testing.T) {
	err := NotImplemented("status")
	if err == nil {
		t.Fatal("NotImplemented returned nil")
	}
	if err.Code != contract.CodeNotImplemented {
		t.Fatalf("code = %q, want %q", err.Code, contract.CodeNotImplemented)
	}
	if got := contract.ExitCodeFor(err.Code); got != 10 {
		t.Fatalf("exit = %d, want 10", got)
	}
	if contract.Retryable(err.Code) {
		t.Fatal("not_implemented must not be retryable")
	}
	if !reflect.DeepEqual(err.Advice, []string{"ctx update"}) {
		t.Fatalf("advice = %#v, want [\"ctx update\"]", err.Advice)
	}
	if !strings.Contains(err.Message, "status") {
		t.Fatalf("message %q does not name the op", err.Message)
	}
	// It travels through RunE as a plain error and is found again.
	var e error = err
	if ce := wp01ContractError(t, e); ce.Code != contract.CodeNotImplemented {
		t.Fatalf("errors.As code = %q", ce.Code)
	}
}

func TestWrapKeepsErrorsIs(t *testing.T) {
	base := errors.New("disk full")

	w := Wrap(base, contract.CodeInternal, "write base")
	if !errors.Is(w, base) {
		t.Fatal("errors.Is(Wrap(base), base) = false")
	}
	if w.Code != contract.CodeInternal || w.Message != "write base: disk full" {
		t.Fatalf("Wrap = {%q, %q}", w.Code, w.Message)
	}

	if got := Wrap(base, contract.CodeNetwork, "").Message; got != "disk full" {
		t.Fatalf("Wrap with empty msg: message = %q, want %q", got, "disk full")
	}
	if got := Wrap(nil, contract.CodeUsage, "bad flag"); got.Message != "bad flag" || got.Err != nil {
		t.Fatalf("Wrap(nil) = {%q, %v}", got.Message, got.Err)
	}

	// A sentinel deeper in the chain stays reachable.
	w2 := Wrap(w, contract.CodeConflict, "push", "ctx pull")
	if !errors.Is(w2, base) {
		t.Fatal("errors.Is through two Wraps = false")
	}
	if !reflect.DeepEqual(w2.Advice, []string{"ctx pull"}) {
		t.Fatalf("advice = %#v", w2.Advice)
	}

	n := New(contract.CodeStale, "stale", "ctx status")
	if n.Code != contract.CodeStale || n.Message != "stale" || !reflect.DeepEqual(n.Advice, []string{"ctx status"}) {
		t.Fatalf("New = %+v", n)
	}
}

// TestPackageDocScopesWave0Imports pins doc.go (frozen with env.go) to E3.
// The engines sit on internal/syncstate, and WP-1.1's status.go takes a
// *config.Remote and calls ui.ToContractError, so the package doc may keep
// syncops off those packages only in wave 0, never as a lasting rule.
func TestPackageDocScopesWave0Imports(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "doc.go", nil, parser.ParseComments|parser.PackageClauseOnly)
	if err != nil {
		t.Fatal(err)
	}
	if f.Doc == nil {
		t.Fatal("doc.go has no package doc")
	}
	var sentences []string
	for _, para := range strings.Split(f.Doc.Text(), "\n\n") {
		para = strings.Join(strings.Fields(para), " ")
		sentences = append(sentences, strings.Split(para, ". ")...)
	}
	for _, s := range sentences {
		bans := strings.Contains(s, "import") &&
			(strings.Contains(s, "never") || strings.Contains(s, "not "))
		if !bans || strings.Contains(s, "wave 0") {
			continue
		}
		for _, pkg := range []string{"internal/config", "internal/syncstate", "internal/ui"} {
			if strings.Contains(s, pkg) {
				t.Errorf("package doc bans %s for good: %q; E3 and WP-1.1 need it in wave 1, so scope that rule to wave 0", pkg, s)
			}
		}
	}
}
