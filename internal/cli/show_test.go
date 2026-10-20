package cli

// Stub assertions for the hidden wave-0 `ctx show` (WP-0.1). Inherited by
// WP-1.4, which replaces the stub, unhides the command and rewrites this file.

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/ui"
)

// wp01Command returns the named top-level command of a fresh root.
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

func TestShowStubHiddenAndFlags(t *testing.T) {
	show := wp01Command(t, "show")
	if !show.Hidden {
		t.Error("ctx show must stay hidden until WP-1.4")
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

func TestShowStubNotImplemented(t *testing.T) {
	root := wp01InitRoot(t)
	for _, at := range []string{"base", "server", "0123456789abcdef0123456789abcdef01234567"} {
		code, stdout, stderr := wp01Run(t, "show", "wiki/concepts/a.md", "--at", at, "--root", root)
		if code != contract.ExitUnsupported {
			t.Errorf("--at %s: exit = %d, want 10 (stderr %q)", at, code, stderr)
		}
		if stdout != "" {
			t.Errorf("--at %s: stdout must be empty, got %q", at, stdout)
		}
		if n := len(wp01NonEmptyLines(stderr)); n != 1 {
			t.Errorf("--at %s: stderr must be one envelope line, got %q", at, stderr)
		}
		env := wp01Envelope(t, stderr)
		if env.Error.Code != contract.CodeNotImplemented || env.ExitCode != 10 {
			t.Errorf("--at %s: envelope %+v", at, env)
		}
	}
}
