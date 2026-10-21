package cli

// Frozen, stub-agnostic envelope tests (WP-0.1). Only the usage,
// unknown-command and not_initialized cases live here, so wave 1 replacing
// the show/resolve/repair stubs never breaks this file. The shared wp01
// helpers below are used by the inherited show/resolve/repair stub tests too.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/ui"
)

// wp01Command returns the named top-level command of a fresh root. It lives
// in this frozen file, not in an inherited stub test, so a wave-1 owner can
// rewrite show_test.go, resolve_test.go or repair_test.go without breaking
// another owner's file.
func wp01Command(t *testing.T, name string) *cobra.Command {
	t.Helper()
	for _, c := range NewRootCmd().Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("ctx %s is not registered", name)
	return nil
}

// wp01ArgsCode runs cmd's Args validator and returns the contract code of its
// error ("" when the args are accepted).
func wp01ArgsCode(t *testing.T, cmd *cobra.Command, args ...string) contract.ErrorCode {
	t.Helper()
	if cmd.Args == nil {
		t.Fatalf("%s has no Args validator", cmd.Name())
	}
	err := cmd.Args(cmd, args)
	if err == nil {
		return ""
	}
	return ui.ToContractError(err).Code
}

// wp01IsolateEnv points HOME (and USERPROFILE) at a fresh temp dir and turns
// the update check off, so no test reads or writes the developer's real
// credentials or update cache.
func wp01IsolateEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CONTEXO_NO_UPDATE_CHECK", "1")
	return home
}

// wp01Run runs ctx in-process through Execute with an isolated HOME and
// returns the exit code and both streams.
func wp01Run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	wp01IsolateEnv(t)
	if args == nil {
		args = []string{}
	}
	var out, errb bytes.Buffer
	code = Execute(args, &out, &errb)
	return code, out.String(), errb.String()
}

// wp01InitRoot returns a temp project root that RequireHub accepts:
// <root>/.contexo/ with a config.json.
func wp01InitRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	hub := filepath.Join(root, ".contexo")
	if err := os.MkdirAll(hub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hub, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// wp01NonEmptyLines splits s into lines, dropping blank ones.
func wp01NonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// wp01Envelope strictly decodes the LAST non-empty stderr line as a
// ctx.error.v1 envelope: unknown fields fail, advice must be [] rather than
// null, and details must decode strictly into contract.NewDetails(code) (and
// be absent for codes without details).
func wp01Envelope(t *testing.T, stderr string) contract.ErrorEnvelope {
	t.Helper()
	lines := wp01NonEmptyLines(stderr)
	if len(lines) == 0 {
		t.Fatalf("no envelope: stderr is empty")
	}
	last := lines[len(lines)-1]

	dec := json.NewDecoder(strings.NewReader(last))
	dec.DisallowUnknownFields()
	var env contract.ErrorEnvelope
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("last stderr line is not a strict envelope: %v\n%q", err, last)
	}
	if dec.More() {
		t.Fatalf("trailing data after the envelope: %q", last)
	}
	if env.Schema != contract.SchemaError {
		t.Errorf("schema = %q, want %q", env.Schema, contract.SchemaError)
	}
	if env.Error.Advice == nil {
		t.Errorf("advice must be [] not null/omitted: %q", last)
	}
	if env.ExitCode != contract.ExitCodeFor(env.Error.Code) {
		t.Errorf("exit_code %d does not match code %q", env.ExitCode, env.Error.Code)
	}
	if env.Error.Retryable != contract.Retryable(env.Error.Code) {
		t.Errorf("retryable %v does not match code %q", env.Error.Retryable, env.Error.Code)
	}
	if strings.ContainsRune(last, '\x1b') {
		t.Errorf("ANSI in envelope: %q", last)
	}

	var raw struct {
		Error struct {
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(last), &raw); err != nil {
		t.Fatal(err)
	}
	want := contract.NewDetails(env.Error.Code)
	switch {
	case raw.Error.Details == nil:
	case want == nil:
		t.Errorf("code %q carries details it does not define: %s", env.Error.Code, raw.Error.Details)
	default:
		dd := json.NewDecoder(bytes.NewReader(raw.Error.Details))
		dd.DisallowUnknownFields()
		if err := dd.Decode(want); err != nil {
			t.Errorf("details do not strictly decode for %q: %v", env.Error.Code, err)
		}
	}
	return env
}

// wp01EnvelopeCase is one row of the frozen envelope table. code == "" means
// human mode: exit 1 and no JSON on stderr.
type wp01EnvelopeCase struct {
	name   string
	args   []string
	exit   int
	code   contract.ErrorCode
	advice []string // checked exactly when non-nil
}

func wp01EnvelopeCases(uninitialised string) []wp01EnvelopeCase {
	return []wp01EnvelopeCase{
		{name: "unknown flag with --json", args: []string{"version", "--bogus", "--json"},
			exit: 3, code: contract.CodeUsage},
		{name: "unknown command with --json", args: []string{"bogus", "--json"},
			exit: 3, code: contract.CodeUsage},
		{name: "show in an uninitialised root", args: []string{"show", "wiki/concepts/a.md", "--at", "base", "--root", uninitialised},
			exit: 5, code: contract.CodeNotInitialized, advice: []string{"ctx init"}},
		{name: "unknown flag without --json stays human", args: []string{"version", "--bogus"},
			exit: 1},
	}
}

// wp01CheckEnvelopeCase asserts the stream rules: stdout empty, exactly one
// non-empty stderr line, which is the envelope in envelope mode.
func wp01CheckEnvelopeCase(t *testing.T, c wp01EnvelopeCase, code int, stdout, stderr string) {
	t.Helper()
	if code != c.exit {
		t.Errorf("exit = %d, want %d (stderr %q)", code, c.exit, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout must be empty, got %q", stdout)
	}
	if n := len(wp01NonEmptyLines(stderr)); n != 1 {
		t.Errorf("stderr must hold exactly one non-empty line, got %d: %q", n, stderr)
	}
	if c.code == "" {
		if strings.Contains(stderr, "{") {
			t.Errorf("human mode printed JSON on stderr: %q", stderr)
		}
		if strings.TrimSpace(stderr) == "" {
			t.Error("human mode swallowed the error")
		}
		return
	}
	env := wp01Envelope(t, stderr)
	if env.ExitCode != code {
		t.Errorf("envelope exit_code %d != process exit %d", env.ExitCode, code)
	}
	if env.Error.Code != c.code {
		t.Errorf("code = %q, want %q (message %q)", env.Error.Code, c.code, env.Error.Message)
	}
	if c.advice != nil && !reflect.DeepEqual(env.Error.Advice, c.advice) {
		t.Errorf("advice = %q, want %q", env.Error.Advice, c.advice)
	}
	if env.Error.Message == "" {
		t.Error("envelope message is empty")
	}
}

// Every non-nil Args validator in the tree yields usage; one that already
// returns a *contract.Error keeps its code; a nil Args stays nil so cobra's
// legacyArgs still reports unknown commands.
func TestArgsValidatorsAreCodedUsage(t *testing.T) {
	root := &cobra.Command{Use: "ctx"}
	exact := &cobra.Command{Use: "exact", Args: cobra.ExactArgs(1), Run: func(*cobra.Command, []string) {}}
	group := &cobra.Command{Use: "group"}
	none := &cobra.Command{Use: "none", Args: cobra.NoArgs, Run: func(*cobra.Command, []string) {}}
	custom := &cobra.Command{Use: "custom", Run: func(*cobra.Command, []string) {},
		Args: func(*cobra.Command, []string) error {
			return contract.New(contract.CodeInvalidPath, "bad path")
		}}
	group.AddCommand(none)
	root.AddCommand(exact, group, custom)

	codeArgsErrorsAsUsage(root)

	if root.Args != nil || group.Args != nil {
		t.Error("a nil Args validator must stay nil")
	}
	for _, c := range []struct {
		cmd  *cobra.Command
		args []string
		want contract.ErrorCode
	}{
		{exact, []string{}, contract.CodeUsage},
		{exact, []string{"a", "b"}, contract.CodeUsage},
		{none, []string{"x"}, contract.CodeUsage},
		{custom, []string{"x"}, contract.CodeInvalidPath},
	} {
		err := c.cmd.Args(c.cmd, c.args)
		if err == nil {
			t.Errorf("%s %q accepted", c.cmd.Name(), c.args)
			continue
		}
		var ce *contract.Error
		if !errors.As(err, &ce) || ce.Code != c.want {
			t.Errorf("%s %q -> %#v, want a *contract.Error with code %q", c.cmd.Name(), c.args, err, c.want)
		}
	}
	if err := exact.Args(exact, []string{"a"}); err != nil {
		t.Errorf("valid args rejected: %v", err)
	}

	// The real tree: every registered validator is wrapped.
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Args != nil {
			if err := c.Args(c, []string{"x", "y", "z", "w", "v", "u", "s", "r", "q", "p", "o"}); err != nil {
				var ce *contract.Error
				if !errors.As(err, &ce) {
					t.Errorf("%s: Args error %q is not coded", c.CommandPath(), err)
				}
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(NewRootCmd())
}

func TestJSONErrorEnvelope(t *testing.T) {
	uninit := t.TempDir()
	for _, c := range wp01EnvelopeCases(uninit) {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := wp01Run(t, c.args...)
			wp01CheckEnvelopeCase(t, c, code, stdout, stderr)
		})
	}
}

// wp01BuildCtx builds the real ctx binary into a temp dir, skipping under
// -short or without a go toolchain. It is built with the caller's
// environment (warm build cache); only the runs get an isolated HOME.
func wp01BuildCtx(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds ctx; skipped under -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	bin := filepath.Join(t.TempDir(), "ctx")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command(goBin, "build", "-o", bin, "github.com/sugihAF/contexo/cmd/ctx")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ctx: %v\n%s", err, out)
	}
	return bin
}

// wp01RunBin runs the built ctx with HOME at home and the update check off,
// and returns the process exit code and both streams.
func wp01RunBin(t *testing.T, bin, home string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"HOME="+home, "USERPROFILE="+home, "CONTEXO_NO_UPDATE_CHECK=1")
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

// TestJSONErrorEnvelopeSubprocess runs the same table against a real ctx
// binary, so the process exit codes (not just Execute's return value) and the
// real os.Stdout/os.Stderr wiring of cmd/ctx/main.go are pinned.
func TestJSONErrorEnvelopeSubprocess(t *testing.T) {
	bin := wp01BuildCtx(t)
	home := t.TempDir()
	uninit := t.TempDir()
	for _, c := range wp01EnvelopeCases(uninit) {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := wp01RunBin(t, bin, home, c.args...)
			wp01CheckEnvelopeCase(t, c, code, stdout, stderr)
		})
	}

	// cobra writes its own diagnostics ("Unknown help topic" plus usage)
	// through OutOrStderr, as push's conflict list and pull's reindex
	// warning do. In the binary that must stay stderr, as on main.
	t.Run("OutOrStderr text stays on stderr", func(t *testing.T) {
		code, stdout, stderr := wp01RunBin(t, bin, home, "help", "nosuchtopic")
		if code != 0 {
			t.Errorf("exit = %d, want 0 (stderr %q)", code, stderr)
		}
		if stdout != "" {
			t.Errorf("stdout must be empty, got %q", stdout)
		}
		if !strings.Contains(stderr, "Unknown help topic") {
			t.Errorf("stderr lacks the help-topic message: %q", stderr)
		}
	})
}

// cobra's OutOrStderr() returns the SetOut writer whenever one is set and
// falls back to os.Stderr only when none is. Execute must therefore leave the
// real streams unset, so the binary routes every OutOrStderr write (push's
// conflict list, pull's reindex warning, cobra's usage and help-topic text)
// to stderr exactly as main did. Substitute writers are still installed, so
// in-process tests capture output.
func TestExecuteKeepsCobraStreamRouting(t *testing.T) {
	sub := func(root *cobra.Command) *cobra.Command {
		c, _, err := root.Find([]string{"push"})
		if err != nil || c == root {
			t.Fatalf("ctx push is not registered: %v", err)
		}
		return c
	}

	std := newExecRoot([]string{}, os.Stdout, os.Stderr)
	for _, c := range []*cobra.Command{std, sub(std)} {
		if w := c.OutOrStderr(); w != os.Stderr {
			t.Errorf("%s: OutOrStderr() = %v, want os.Stderr", c.CommandPath(), w)
		}
		if w := c.OutOrStdout(); w != os.Stdout {
			t.Errorf("%s: OutOrStdout() = %v, want os.Stdout", c.CommandPath(), w)
		}
		if w := c.ErrOrStderr(); w != os.Stderr {
			t.Errorf("%s: ErrOrStderr() = %v, want os.Stderr", c.CommandPath(), w)
		}
	}

	var out, errb bytes.Buffer
	buffered := newExecRoot([]string{}, &out, &errb)
	for _, c := range []*cobra.Command{buffered, sub(buffered)} {
		if c.OutOrStdout() != &out {
			t.Errorf("%s: OutOrStdout() is not the stdout buffer", c.CommandPath())
		}
		if c.ErrOrStderr() != &errb {
			t.Errorf("%s: ErrOrStderr() is not the stderr buffer", c.CommandPath())
		}
	}
}
