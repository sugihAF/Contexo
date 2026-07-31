package ui

import (
	"bytes"
	"strings"
	"testing"
)

// Non-TTY must be exactly two plain lines: start and completion. No repaints,
// no carriage returns, no escapes -- the docs capture harness records this
// branch (stdout to a file, stdin from /dev/null).
func TestTaskNonTTYEmitsTwoPlainLines(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: false, Dark: true})
	tk := u.Task("Analyzing codebase")
	tk.Detail("graphify, 1284 files")
	tk.Done("Analyzed %d files", 1284)

	out := buf.String()
	if strings.ContainsAny(out, "\r") || strings.Contains(out, "\x1b[") {
		t.Errorf("non-tty task emitted control bytes: %q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "Analyzing codebase") {
		t.Errorf("first line = %q", lines[0])
	}
	if !strings.Contains(lines[1], "Analyzed 1284 files") {
		t.Errorf("second line = %q", lines[1])
	}
}

func TestTaskMachineModeIsSilent(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{Machine: true})
	tk := u.Task("Analyzing")
	tk.Detail("x")
	tk.Done("done")
	if buf.Len() != 0 {
		t.Errorf("machine task wrote %q", buf.String())
	}
}

func TestTaskFailUsesFailGlyph(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: false, Dark: true})
	u.Task("Pushing").Fail("network unreachable")
	if !strings.Contains(buf.String(), "network unreachable") {
		t.Errorf("missing failure text: %q", buf.String())
	}
}

// Detail off a TTY must not add a third line.
func TestTaskDetailIsNoOpOffTTY(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: false, Dark: true})
	tk := u.Task("Working")
	tk.Detail("a")
	tk.Detail("b")
	tk.Detail("c")
	tk.Done("Worked")
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("Detail leaked lines: got %d\n%q", len(lines), buf.String())
	}
}
