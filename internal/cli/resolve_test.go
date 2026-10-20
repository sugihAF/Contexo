package cli

// Stub assertions for the hidden wave-0 `ctx resolve` (WP-0.1). Inherited by
// WP-1.4, which replaces the stub, unhides the command and rewrites this file.

import (
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

func TestResolveStubHiddenAndFlags(t *testing.T) {
	resolve := wp01Command(t, "resolve")
	if !resolve.Hidden {
		t.Error("ctx resolve must stay hidden until WP-1.4")
	}
	for name, typ := range map[string]string{
		"ours": "bool", "theirs": "bool", "json": "bool", "if-server-sha": "string",
	} {
		f := resolve.Flags().Lookup(name)
		if f == nil {
			t.Errorf("--%s is not defined", name)
			continue
		}
		if f.Value.Type() != typ {
			t.Errorf("--%s is %s, want %s", name, f.Value.Type(), typ)
		}
		if f.Hidden {
			t.Errorf("--%s must not be hidden", name)
		}
	}
	if d := resolve.Flags().Lookup("json").DefValue; d != "false" {
		t.Errorf("--json defaults to %q, want false (envelope only with --json, E21)", d)
	}

	if c := wp01ArgsCode(t, resolve, "wiki/concepts/a.md"); c != "" {
		t.Errorf("one path rejected: %q", c)
	}
	for _, args := range [][]string{{}, {"a.md", "b.md"}} {
		if c := wp01ArgsCode(t, resolve, args...); c != contract.CodeUsage {
			t.Errorf("args %q -> %q, want usage", args, c)
		}
	}
}

// Exactly one strategy is required. The check runs before RequireHub, so it
// is usage even in an uninitialised root, and it stays human (exit 1)
// without --json (E21).
func TestResolveBothStrategiesIsUsage(t *testing.T) {
	uninit := t.TempDir()
	for _, args := range [][]string{
		{"resolve", "wiki/concepts/a.md", "--ours", "--theirs", "--json"},
		{"resolve", "wiki/concepts/a.md", "--json"},
		{"resolve", "wiki/concepts/a.md", "--ours", "--theirs", "--json", "--root", uninit},
	} {
		code, stdout, stderr := wp01Run(t, args...)
		if code != contract.ExitUsage {
			t.Errorf("%q: exit = %d, want 3 (stderr %q)", args, code, stderr)
		}
		if stdout != "" {
			t.Errorf("%q: stdout = %q", args, stdout)
		}
		if n := len(wp01NonEmptyLines(stderr)); n != 1 {
			t.Errorf("%q: stderr must be one envelope line, got %q", args, stderr)
		}
		if env := wp01Envelope(t, stderr); env.Error.Code != contract.CodeUsage {
			t.Errorf("%q: code = %q, want usage", args, env.Error.Code)
		}
	}

	code, stdout, stderr := wp01Run(t, "resolve", "wiki/concepts/a.md", "--ours", "--theirs")
	if code != 1 || stdout != "" {
		t.Errorf("human mode: exit %d stdout %q", code, stdout)
	}
	if strings.Contains(stderr, "{") || strings.TrimSpace(stderr) == "" {
		t.Errorf("human mode stderr = %q", stderr)
	}
}

func TestResolveStubNotImplemented(t *testing.T) {
	root := wp01InitRoot(t)
	for _, strategy := range []string{"--ours", "--theirs"} {
		code, stdout, stderr := wp01Run(t, "resolve", "wiki/concepts/a.md", strategy, "--json", "--root", root)
		if code != contract.ExitUnsupported {
			t.Errorf("%s: exit = %d, want 10 (stderr %q)", strategy, code, stderr)
		}
		if stdout != "" {
			t.Errorf("%s: stdout = %q", strategy, stdout)
		}
		if n := len(wp01NonEmptyLines(stderr)); n != 1 {
			t.Errorf("%s: stderr must be one envelope line, got %q", strategy, stderr)
		}
		if env := wp01Envelope(t, stderr); env.Error.Code != contract.CodeNotImplemented {
			t.Errorf("%s: code = %q", strategy, env.Error.Code)
		}
	}
}
