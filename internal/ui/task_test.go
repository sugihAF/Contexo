package ui

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
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

// wp03IsolateHome points HOME and USERPROFILE at a temp dir, so nothing a
// test does can read or write the developer's real ~/.contexo.
func wp03IsolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// wp03SingleProc pins GOMAXPROCS to 1. Go queues a goroutine at the go
// statement rather than running it, so with one P the spinner goroutine is
// not scheduled until the caller blocks -- which a caller that finishes its
// work without yielding (ctx status right after login) first does in Done.
func wp03SingleProc(t *testing.T) {
	t.Helper()
	prev := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(prev) })
}

// Done before the spinner goroutine is scheduled: finish nils t.prog, then
// blocks in Quit, and only then does the goroutine run. It must run the
// program it was handed, not re-read the field and panic in Program.Run.
func TestTaskDoneBeforeGoroutineRuns(t *testing.T) {
	wp03IsolateHome(t)
	wp03SingleProc(t)
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: true, Dark: true})
	u.Task("Checking drift").Done("No drift")
	if !strings.Contains(buf.String(), "No drift") {
		t.Errorf("missing completion text: %q", buf.String())
	}
}

func TestTaskFailBeforeGoroutineRuns(t *testing.T) {
	wp03IsolateHome(t)
	wp03SingleProc(t)
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: true, Dark: true})
	u.Task("Pushing").Fail("network unreachable")
	if !strings.Contains(buf.String(), "network unreachable") {
		t.Errorf("missing failure text: %q", buf.String())
	}
}

// Detail before the program runs blocks in Send until the event loop starts;
// it must neither deadlock nor leave Done with a program it cannot stop.
func TestTaskDetailBeforeRun(t *testing.T) {
	wp03IsolateHome(t)
	wp03SingleProc(t)
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: true, Dark: true})
	tk := u.Task("Pulling")
	tk.Detail("3 pages")
	tk.Done("Pulled 3 pages")
	if !strings.Contains(buf.String(), "Pulled 3 pages") {
		t.Errorf("missing completion text: %q", buf.String())
	}
}

func TestTaskQuickSuccession(t *testing.T) {
	wp03IsolateHome(t)
	wp03SingleProc(t)
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: true, Dark: true})
	for i := 0; i < 5; i++ {
		u.Task("Working").Done("Task %d done", i)
	}
	out := buf.String()
	for i := 0; i < 5; i++ {
		if want := fmt.Sprintf("Task %d done", i); !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

// The spinner reads no keys. On bubbletea's default input it would put a
// terminal stdin into raw mode (^C becomes a dropped KeyMsg, not SIGINT), or
// open /dev/tty / CONIN$ when stdin is not a terminal, whose close can block
// behind a pending read. A stdin that never reaches EOF must not hold Done up.
func TestTaskDoesNotReadStdin(t *testing.T) {
	wp03IsolateHome(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		_ = r.Close()
		_ = w.Close()
	})

	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: true, Dark: true})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		u.Task("Waiting").Done("Waited")
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not return within 2s with an open, silent stdin")
	}
	if !strings.Contains(buf.String(), "Waited") {
		t.Errorf("missing completion text: %q", buf.String())
	}
}
