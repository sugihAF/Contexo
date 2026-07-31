package cli

import (
	"bytes"
	"strings"
	"testing"
)

// `ctx capture turn` is invoked by six hook events across Claude Code, Codex
// and Cursor. Claude parses a Stop hook's stdout as control JSON, and
// UserPromptSubmit stdout is injected verbatim into the model's context. One
// styled byte there poisons every agent turn, so stdout must stay byte-empty.
//
// The pre-existing capture test only asserts on stderr, so a stray stdout
// write would have passed unnoticed.
func TestCaptureTurnStdoutIsByteEmpty(t *testing.T) {
	dir := t.TempDir()
	var out, errBuf bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(`{"hook_event_name":"Stop"}`))
	root.SetArgs([]string{"--root", dir, "capture", "turn"})
	_ = root.Execute()

	if out.Len() != 0 {
		t.Errorf("capture turn wrote %d bytes to stdout: %q", out.Len(), out.String())
	}
}

// The completion script body must be sourceable -- no ANSI, no chrome.
func TestCompletionOutputIsUnstyled(t *testing.T) {
	var out bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"completion", "bash"})
	if err := root.Execute(); err != nil {
		t.Fatalf("completion: %v", err)
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Error("completion script contains ANSI escapes; it would fail to source")
	}
}

// cobra's __complete RPC is parsed by the shell, including the trailing
// ":<directive>" line.
func TestCompleteRPCIsUnstyled(t *testing.T) {
	var out bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"__complete", ""})
	_ = root.Execute()
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("__complete output contains ANSI: %q", out.String())
	}
}

// version --short is asserted by exact whole-output equality elsewhere; this
// pins that no global chrome leaks into it.
func TestVersionShortHasNoChrome(t *testing.T) {
	var out bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version", "--short"})
	if err := root.Execute(); err != nil {
		t.Fatalf("version --short: %v", err)
	}
	if n := len(strings.Split(strings.TrimSpace(out.String()), "\n")); n != 1 {
		t.Errorf("version --short emitted %d lines: %q", n, out.String())
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("version --short contains ANSI: %q", out.String())
	}
}
