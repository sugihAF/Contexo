package cli

// Stub assertions for the hidden wave-0 `ctx show` (WP-0.1). Inherited by
// WP-1.4, which replaces the stub, unhides the command and rewrites this file.

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/ui"
)

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
