package cli

// Stub assertions for the hidden wave-0 `ctx repair` (WP-0.1). Inherited by
// WP-1.5, which replaces the stub, unhides the command, registers "repair"
// and rewrites this file.

import (
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

func TestRepairStubHiddenAndFlags(t *testing.T) {
	repair := wp01Command(t, "repair")
	if !repair.Hidden {
		t.Error("ctx repair must stay hidden until WP-1.5")
	}
	for _, name := range []string{"dry-run", "json"} {
		f := repair.Flags().Lookup(name)
		if f == nil {
			t.Errorf("--%s is not defined", name)
			continue
		}
		if f.Value.Type() != "bool" || f.DefValue != "false" {
			t.Errorf("--%s is %s=%q, want bool=false", name, f.Value.Type(), f.DefValue)
		}
	}

	// repair takes no positional args; one is usage.
	if c := wp01ArgsCode(t, repair); c != "" {
		t.Errorf("no args rejected: %q", c)
	}
	if c := wp01ArgsCode(t, repair, "extra"); c != contract.CodeUsage {
		t.Errorf("extra arg -> %q, want usage", c)
	}
	code, stdout, stderr := wp01Run(t, "repair", "extra", "--json", "--root", wp01InitRoot(t))
	if code != contract.ExitUsage || stdout != "" {
		t.Errorf("extra arg: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestRepairStubNotImplemented(t *testing.T) {
	root := wp01InitRoot(t)
	for _, args := range [][]string{
		{"repair", "--json", "--root", root},
		{"repair", "--dry-run", "--json", "--root", root},
	} {
		code, stdout, stderr := wp01Run(t, args...)
		if code != contract.ExitUnsupported {
			t.Errorf("%q: exit = %d, want 10 (stderr %q)", args, code, stderr)
		}
		if stdout != "" {
			t.Errorf("%q: stdout = %q", args, stdout)
		}
		if n := len(wp01NonEmptyLines(stderr)); n != 1 {
			t.Errorf("%q: stderr must be one envelope line, got %q", args, stderr)
		}
		if env := wp01Envelope(t, stderr); env.Error.Code != contract.CodeNotImplemented {
			t.Errorf("%q: code = %q", args, env.Error.Code)
		}
	}
}
