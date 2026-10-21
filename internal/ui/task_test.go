package ui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
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

// Detail issued before the program runs, racing Done: Detail either blocks in
// Send until the event loop starts or finds t.prog already cleared. Neither
// may deadlock, and the spinner goroutine, scheduled only after both, must
// still run the program it was handed (the old code re-read the cleared
// field and panicked in Program.Run).
func TestTaskDetailBeforeRun(t *testing.T) {
	wp03IsolateHome(t)
	wp03SingleProc(t)
	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: true, Dark: true})
	tk := u.Task("Pulling")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tk.Detail("3 pages")
	}()
	tk.Done("Pulled 3 pages")
	wg.Wait()
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
// behind a pending read. A stdin that never reaches EOF must not hold Done up,
// and the spinner must still draw: with default input and no openable
// /dev/tty (CI, sandboxes) Run fails before rendering anything. On a real
// terminal only a pty-driven test could see the raw-mode half.
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
	if !strings.Contains(buf.String(), "Waiting") {
		t.Errorf("spinner never rendered (bubbletea fell back to default input?): %q", buf.String())
	}
}

// ^C during a live spinner: with input disabled the terminal stays cooked, so
// ^C raises a real SIGINT. Exiting there directly would skip bubbletea's
// shutdown and leave the cursor hidden and bracketed paste on. The spinner
// must restore the terminal first, then end the process (interruptExit).
func TestTaskInterruptRestoresTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal(os.Interrupt) is not supported on Windows")
	}
	wp03IsolateHome(t)
	exited := make(chan struct{})
	prev := interruptExit
	interruptExit = func() { close(exited) }
	t.Cleanup(func() { interruptExit = prev })

	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: true, Dark: true})
	tk := u.Task("Pushing")
	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := self.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("^C during the spinner did not reach interruptExit within 5s")
	}
	out := buf.String()
	for _, seq := range []string{"\x1b[?25h", "\x1b[?2004l"} {
		if !strings.Contains(out, seq) {
			t.Errorf("terminal not restored before exit: missing %q in %q", seq, out)
		}
	}
	tk.Done("unreachable in production") // must not hang on a killed program
}

// A finished Task must have stopped watching for ^C before Done returns: a
// watcher still registered when a later ^C arrives would exit the process
// itself, without the live spinner restoring the terminal.
func TestTaskInterruptAfterFinishedTask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal(os.Interrupt) is not supported on Windows")
	}
	wp03IsolateHome(t)
	wp03SingleProc(t)
	var mu sync.Mutex
	calls := 0
	first := make(chan struct{}, 1)
	prev := interruptExit
	interruptExit = func() {
		mu.Lock()
		calls++
		mu.Unlock()
		select {
		case first <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { interruptExit = prev })

	var buf bytes.Buffer
	u := New(&buf, Mode{TTY: true, Dark: true})
	u.Task("First").Done("first done")
	tk := u.Task("Second")
	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := self.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("^C during the second spinner did not reach interruptExit within 5s")
	}
	tk.Done("second done")
	time.Sleep(50 * time.Millisecond) // let any stale watcher run
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("interruptExit ran %d times, want once (a finished Task was still watching ^C)", calls)
	}
}

// A ^C that lands as the spinner finishes -- Run has returned but the watcher
// has not unregistered yet -- must still interrupt, never be swallowed. With
// both the signal and done ready the watcher's select picks either branch, so
// every iteration must end in exactly one interruptExit.
func TestTaskInterruptWhileFinishingIsHonoured(t *testing.T) {
	calls := 0
	prev := interruptExit
	interruptExit = func() { calls++ }
	t.Cleanup(func() { interruptExit = prev })

	const rounds = 200
	for i := 0; i < rounds; i++ {
		sig := make(chan os.Signal, 1)
		sig <- os.Interrupt
		done := make(chan struct{})
		close(done)
		watchInterrupt(sig, done, func() {})
	}
	if calls != rounds {
		t.Errorf("interruptExit ran %d times in %d rounds; a ^C racing the end of the spinner was dropped", calls, rounds)
	}
}

// wp03InterruptHelperEnv selects TestWp03InterruptHelper's mode: "spin" runs
// a spinner and ^Cs itself; "ignored" does the same with SIGINT ignored, the
// way a shell's empty INT trap or a background job of a non-interactive shell
// starts it.
const wp03InterruptHelperEnv = "WP03_INTERRUPT_HELPER"

// TestWp03InterruptHelper is not a test on its own: the interrupt tests
// re-run the test binary with only it selected, so a real ^C can end a real
// process.
func TestWp03InterruptHelper(t *testing.T) {
	mode := os.Getenv(wp03InterruptHelperEnv)
	if mode == "" {
		t.Skip("helper process for the interrupt tests")
	}
	if mode == "ignored" {
		signal.Ignore(os.Interrupt)
	}
	u := New(os.Stdout, Mode{TTY: true, Dark: true})
	tk := u.Task("Spinning")
	if self, err := os.FindProcess(os.Getpid()); err == nil {
		_ = self.Signal(os.Interrupt)
	}
	time.Sleep(500 * time.Millisecond)
	tk.Done("spun")
	fmt.Println("HELPER-FINISHED")
}

func wp03RunInterruptHelper(t *testing.T, mode string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal(os.Interrupt) is not supported on Windows")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestWp03InterruptHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), wp03InterruptHelperEnv+"="+mode)
	out, err := cmd.Output()
	return string(out), err
}

// ^C during a spinner must end ctx the way SIGINT does by default, after the
// terminal is restored: a calling shell then sees the signal and stops its
// script. A plain exit status 130 lets `ctx pull; ctx push` carry on.
func TestTaskInterruptDiesFromSIGINT(t *testing.T) {
	out, err := wp03RunInterruptHelper(t, "spin")
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ProcessState.String() != "signal: interrupt" {
		t.Fatalf("helper ended with %v, want death by SIGINT; stdout %q", err, out)
	}
	for _, seq := range []string{"\x1b[?25h", "\x1b[?2004l"} {
		if !strings.Contains(out, seq) {
			t.Errorf("terminal not restored before dying: missing %q in %q", seq, out)
		}
	}
	if strings.Contains(out, "HELPER-FINISHED") {
		t.Errorf("the helper ran on after ^C: %q", out)
	}
}

// A ctx started with SIGINT ignored must keep ignoring it while a spinner is
// up; registering for the signal would un-ignore it.
func TestTaskInterruptKeepsIgnoredSIGINT(t *testing.T) {
	out, err := wp03RunInterruptHelper(t, "ignored")
	if err != nil || !strings.Contains(out, "HELPER-FINISHED") {
		t.Fatalf("helper with SIGINT ignored ended with %v, want a normal finish; stdout %q", err, out)
	}
}
