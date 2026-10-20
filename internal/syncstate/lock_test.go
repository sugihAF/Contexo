package syncstate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var wp04lockStartedAtRE = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)

// wp04lockHost is the host name Lock records for this process.
func wp04lockHost() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// wp04lockHub returns a hub path under a fresh temp dir. The hub itself does
// not exist yet.
func wp04lockHub(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".contexo")
}

// wp04lockWriteRaw plants data as the lock file, as another process would.
func wp04lockWriteRaw(t *testing.T, hub string, data []byte) {
	t.Helper()
	if err := EnsureSyncDir(hub); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LockPath(hub), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// wp04lockWrite plants info as the lock file and returns its bytes.
func wp04lockWrite(t *testing.T, hub string, info LockInfo) []byte {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	wp04lockWriteRaw(t, hub, data)
	return data
}

// wp04lockReadFile returns the lock file bytes, failing if it is missing.
func wp04lockReadFile(t *testing.T, hub string) []byte {
	t.Helper()
	data, err := os.ReadFile(LockPath(hub))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	return data
}

// wp04lockAssertMine checks the lock file names this process and command.
func wp04lockAssertMine(t *testing.T, hub, command string) {
	t.Helper()
	data := wp04lockReadFile(t, hub)
	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("lock file %q: %v", data, err)
	}
	if info.PID != os.Getpid() || info.Command != command || info.Host != wp04lockHost() {
		t.Fatalf("lock file = %+v, want pid %d, command %q, host %q", info, os.Getpid(), command, wp04lockHost())
	}
}

// wp04lockAssertAbsent checks that no lock file exists.
func wp04lockAssertAbsent(t *testing.T, hub string) {
	t.Helper()
	if _, err := os.Lstat(LockPath(hub)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lock file still present (lstat err %v)", err)
	}
}

// wp04lockBusy asserts err is a *BusyError and returns it.
func wp04lockBusy(t *testing.T, err error) *BusyError {
	t.Helper()
	var be *BusyError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v (%T), want *BusyError", err, err)
	}
	return be
}

// wp04lockSyncNames lists <hub>/.sync, sorted.
func wp04lockSyncNames(t *testing.T, hub string) []string {
	t.Helper()
	entries, err := os.ReadDir(SyncDir(hub))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// wp04lockDeadPID returns the pid of a child process that has exited and
// been reaped.
func wp04lockDeadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run short-lived child: %v\n%s", err, out)
	}
	return cmd.ProcessState.Pid()
}

// wp04lockNow is the current time as Lock records it.
func wp04lockNow() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

// wp04lockHelperProc is a child test binary running TestWP04LockHelper.
type wp04lockHelperProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	locked chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	out    []string
}

func (h *wp04lockHelperProc) output() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.out, "\n")
}

// wp04lockStartHelper re-execs the test binary to hold hub's lock in another
// process and returns once the child has printed "locked".
func wp04lockStartHelper(t *testing.T, hub string) *wp04lockHelperProc {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWP04LockHelper$")
	cmd.Env = append(os.Environ(), "WP04_LOCK_HELPER=1", "WP04_LOCK_HUB="+hub)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &wp04lockHelperProc{cmd: cmd, stdin: stdin, locked: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(h.done)
		sc := bufio.NewScanner(stdout)
		seen := false
		for sc.Scan() {
			line := sc.Text()
			h.mu.Lock()
			h.out = append(h.out, line)
			h.mu.Unlock()
			if line == "locked" && !seen {
				seen = true
				close(h.locked)
			}
		}
	}()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			<-h.done
			_ = cmd.Wait()
		}
	})
	select {
	case <-h.locked:
	case <-h.done:
		_ = cmd.Wait()
		t.Fatalf("helper exited before locking:\n%s", h.output())
	case <-time.After(60 * time.Second):
		t.Fatalf("helper did not lock within 60s:\n%s", h.output())
	}
	return h
}

// stop closes the helper's stdin so it unlocks and exits, then reaps it.
func (h *wp04lockHelperProc) stop(t *testing.T) {
	t.Helper()
	_ = h.stdin.Close()
	<-h.done
	if err := h.cmd.Wait(); err != nil {
		t.Fatalf("helper: %v\n%s", err, h.output())
	}
}

// TestWP04LockHelper is not a test: the cross-process tests re-exec the test
// binary to run it in a child process, which takes the lock, prints
// "locked" and holds the lock until its stdin closes.
func TestWP04LockHelper(t *testing.T) {
	if os.Getenv("WP04_LOCK_HELPER") == "" {
		t.Skip("child-process helper for the cross-process lock tests")
	}
	unlock, err := Lock(os.Getenv("WP04_LOCK_HUB"), "helper", 0)
	if err != nil {
		t.Fatalf("helper lock: %v", err)
	}
	defer unlock()
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestLock_ExclusiveAcrossProcesses(t *testing.T) {
	hub := wp04lockHub(t)
	h := wp04lockStartHelper(t, hub)

	_, err := Lock(hub, "push", 200*time.Millisecond)
	be := wp04lockBusy(t, err)
	if be.Holder.PID != h.cmd.Process.Pid || be.Holder.Command != "helper" || be.Holder.Host != wp04lockHost() {
		t.Fatalf("holder = %+v, want helper pid %d on %q", be.Holder, h.cmd.Process.Pid, wp04lockHost())
	}
	if _, ok, err := TryLock(hub, "status"); ok || err != nil {
		t.Fatalf("TryLock while the helper holds the lock: ok=%v err=%v", ok, err)
	}

	h.stop(t)
	wp04lockAssertAbsent(t, hub)
	unlock, err := Lock(hub, "push", 0)
	if err != nil {
		t.Fatalf("Lock after the helper released: %v", err)
	}
	wp04lockAssertMine(t, hub, "push")
	unlock()
	wp04lockAssertAbsent(t, hub)
}

func TestWP04LockKilledHolderIsReclaimed(t *testing.T) {
	hub := wp04lockHub(t)
	h := wp04lockStartHelper(t, hub)
	if err := h.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-h.done
	_ = h.cmd.Wait() // killed: a non-nil error is expected

	var info LockInfo
	if err := json.Unmarshal(wp04lockReadFile(t, hub), &info); err != nil || info.PID != h.cmd.Process.Pid {
		t.Fatalf("killed helper's lock = %+v (err %v), want pid %d", info, err, h.cmd.Process.Pid)
	}
	unlock, err := Lock(hub, "pull", 0)
	if err != nil {
		t.Fatalf("Lock over a killed holder's lock: %v", err)
	}
	wp04lockAssertMine(t, hub, "pull")
	unlock()
	wp04lockAssertAbsent(t, hub)
}

func TestLock_TimeoutHasBusyCode(t *testing.T) {
	hub := wp04lockHub(t)
	unlock, err := Lock(hub, "holder", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	start := time.Now()
	u, err := Lock(hub, "push", 100*time.Millisecond)
	elapsed := time.Since(start)
	if u != nil {
		t.Fatal("Lock returned an unlock func on timeout")
	}
	be := wp04lockBusy(t, err)
	if elapsed < 100*time.Millisecond {
		t.Errorf("Lock gave up after %v, before its 100ms wait", elapsed)
	}
	if got := be.ErrorCode(); got != "busy" {
		t.Errorf("ErrorCode() = %q, want busy", got)
	}
	if adv := be.ErrorAdvice(); adv == nil || len(adv) != 0 {
		t.Errorf("ErrorAdvice() = %#v, want a non-nil empty slice", adv)
	}
	if be.Error() == "" || !strings.Contains(be.Error(), "holder") {
		t.Errorf("Error() = %q, want it to name the holder's command", be.Error())
	}

	raw, err := json.Marshal(be.ErrorDetails())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if got := strings.Join(keys, ","); got != "command,host,pid,started_at" {
		t.Errorf("details keys = %s, want exactly command,host,pid,started_at (%s)", got, raw)
	}
	if s, _ := m["started_at"].(string); !wp04lockStartedAtRE.MatchString(s) {
		t.Errorf("started_at = %q, want UTC whole seconds", m["started_at"])
	}
	if m["pid"] != float64(os.Getpid()) || m["command"] != "holder" || m["host"] != wp04lockHost() {
		t.Errorf("details = %s, want pid %d, command holder, host %q", raw, os.Getpid(), wp04lockHost())
	}

	if got := strings.Join(wp04lockSyncNames(t, hub), ","); got != ".gitignore,lock" {
		t.Errorf(".sync holds %s, want only .gitignore,lock", got)
	}
}

func TestWP04LockFileContent(t *testing.T) {
	hub := wp04lockHub(t)
	before := time.Now().UTC().Truncate(time.Second)
	unlock, err := Lock(hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	after := time.Now().UTC()

	data := wp04lockReadFile(t, hub)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("lock file %q: %v", data, err)
	}
	if len(m) != 4 {
		t.Errorf("lock file %s has %d keys, want pid, host, command, started_at", data, len(m))
	}
	s, _ := m["started_at"].(string)
	if !wp04lockStartedAtRE.MatchString(s) {
		t.Fatalf("started_at = %q, want UTC whole seconds", s)
	}
	at, err := time.Parse(time.RFC3339, s)
	if err != nil || at.Before(before) || at.After(after) {
		t.Errorf("started_at = %v (err %v), want within [%v, %v]", at, err, before, after)
	}
	wp04lockAssertMine(t, hub, "push")
}

func TestLock_StaleRecovery(t *testing.T) {
	host := wp04lockHost()
	cases := []struct {
		name  string
		plant func(t *testing.T, hub string)
		stale bool
	}{
		{"dead_pid", func(t *testing.T, hub string) {
			wp04lockWrite(t, hub, LockInfo{PID: wp04lockDeadPID(t), Host: host, Command: "push", StartedAt: wp04lockNow()})
		}, true},
		{"older_than_10min", func(t *testing.T, hub string) {
			wp04lockWrite(t, hub, LockInfo{PID: os.Getpid(), Host: host, Command: "push", StartedAt: wp04lockNow().Add(-11 * time.Minute)})
		}, true},
		{"older_than_10min_other_host", func(t *testing.T, hub string) {
			wp04lockWrite(t, hub, LockInfo{PID: os.Getpid(), Host: "wp04-other-host", Command: "push", StartedAt: wp04lockNow().Add(-11 * time.Minute)})
		}, true},
		{"unparseable_old_mtime", func(t *testing.T, hub string) {
			wp04lockWriteRaw(t, hub, []byte(`{"pid": 12`))
			old := time.Now().Add(-31 * time.Second)
			if err := os.Chtimes(LockPath(hub), old, old); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"empty_old_mtime", func(t *testing.T, hub string) {
			wp04lockWriteRaw(t, hub, nil)
			old := time.Now().Add(-31 * time.Second)
			if err := os.Chtimes(LockPath(hub), old, old); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"unparseable_fresh", func(t *testing.T, hub string) {
			wp04lockWriteRaw(t, hub, []byte(`{"pid": 12`))
		}, false},
		{"empty_fresh", func(t *testing.T, hub string) {
			wp04lockWriteRaw(t, hub, nil)
		}, false},
		{"live_pid_fresh", func(t *testing.T, hub string) {
			wp04lockWrite(t, hub, LockInfo{PID: os.Getpid(), Host: host, Command: "push", StartedAt: wp04lockNow().Add(-9 * time.Minute)})
		}, false},
		{"other_host_dead_pid", func(t *testing.T, hub string) {
			wp04lockWrite(t, hub, LockInfo{PID: wp04lockDeadPID(t), Host: "wp04-other-host", Command: "push", StartedAt: wp04lockNow()})
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := wp04lockHub(t)
			tc.plant(t, hub)
			planted := wp04lockReadFile(t, hub)
			unlock, err := Lock(hub, "pull", 0)
			if !tc.stale {
				wp04lockBusy(t, err)
				if got := wp04lockReadFile(t, hub); !bytes.Equal(got, planted) {
					t.Fatalf("a live lock was changed: %q -> %q", planted, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Lock over a stale lock: %v", err)
			}
			wp04lockAssertMine(t, hub, "pull")
			unlock()
			wp04lockAssertAbsent(t, hub)
			if got := strings.Join(wp04lockSyncNames(t, hub), ","); got != ".gitignore" {
				t.Errorf(".sync holds %s after unlock, want only .gitignore", got)
			}
		})
	}
}

func TestWP04LockNonPositivePIDIsStale(t *testing.T) {
	host := wp04lockHost()
	now := wp04lockNow().Format(time.RFC3339)
	cases := map[string]string{
		"pid_0":            fmt.Sprintf(`{"pid":0,"host":%q,"command":"push","started_at":%q}`, host, now),
		"pid_negative":     fmt.Sprintf(`{"pid":-1,"host":%q,"command":"push","started_at":%q}`, host, now),
		"pid_0_other_host": fmt.Sprintf(`{"pid":0,"host":"wp04-other-host","command":"push","started_at":%q}`, now),
		"pid_missing":      fmt.Sprintf(`{"host":%q,"command":"push","started_at":%q}`, host, now),
		"pid_string":       fmt.Sprintf(`{"pid":"abc","host":%q,"command":"push","started_at":%q}`, host, now),
		"pid_fraction":     fmt.Sprintf(`{"pid":1.5,"host":%q,"command":"push","started_at":%q}`, host, now),
		"pid_out_of_range": fmt.Sprintf(`{"pid":4294967297,"host":%q,"command":"push","started_at":%q}`, host, now),
		"json_null":        `null`,
		"json_array":       `[1,2]`,
		"bad_started_at":   fmt.Sprintf(`{"pid":%d,"host":%q,"command":"push","started_at":"yesterday"}`, os.Getpid(), host),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			hub := wp04lockHub(t)
			wp04lockWriteRaw(t, hub, []byte(content)) // fresh mtime
			unlock, err := Lock(hub, "pull", 0)
			if err != nil {
				t.Fatalf("Lock over %s: %v", content, err)
			}
			wp04lockAssertMine(t, hub, "pull")
			unlock()
		})
	}
}

func TestWP04ProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("processAlive(own pid) = false")
	}
	if pid := wp04lockDeadPID(t); processAlive(pid) {
		t.Errorf("processAlive(%d) = true for a reaped child", pid)
	}
	for _, pid := range []int{0, -1, math.MinInt32} {
		if processAlive(pid) {
			t.Errorf("processAlive(%d) = true, want false", pid)
		}
	}
	big := int64(1) << 40
	if int64(int(big)) == big && processAlive(int(big)) {
		t.Errorf("processAlive(%d) = true for a pid beyond the OS range", big)
	}
	switch runtime.GOOS {
	case "windows":
		if !processAlive(4) { // System: protected, still alive
			t.Error("processAlive(4) = false for the System process")
		}
	default:
		if !processAlive(1) { // init/launchd: EPERM means alive
			t.Error("processAlive(1) = false for init")
		}
	}
}

func TestTryLockDoesNotWait(t *testing.T) {
	t.Setenv(lockTimeoutEnv, "30s")
	hub := wp04lockHub(t)
	held, err := Lock(hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	best := time.Hour
	for i := 0; i < 5; i++ {
		start := time.Now()
		u, ok, err := TryLock(hub, "status")
		d := time.Since(start)
		if err != nil || ok || u != nil {
			t.Fatalf("TryLock on a held lock: ok=%v err=%v unlock=%v", ok, err, u != nil)
		}
		best = min(best, d)
	}
	if best >= 50*time.Millisecond {
		t.Errorf("TryLock took %v at best on a held lock, want < 50ms", best)
	}
	wp04lockAssertMine(t, hub, "push")

	held()
	u, ok, err := TryLock(hub, "status")
	if err != nil || !ok || u == nil {
		t.Fatalf("TryLock on a free lock: ok=%v err=%v", ok, err)
	}
	wp04lockAssertMine(t, hub, "status")
	u()
	wp04lockAssertAbsent(t, hub)
}

func TestUnlockIdempotent(t *testing.T) {
	hub := wp04lockHub(t)
	u1, err := Lock(hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	u1()
	u1()
	wp04lockAssertAbsent(t, hub)

	// Same process, same command, usually the same second: the new lock's
	// bytes can equal the old one's, so only idempotence protects it.
	u2, err := Lock(hub, "push", 0)
	if err != nil {
		t.Fatalf("Lock after unlock: %v", err)
	}
	u1()
	wp04lockAssertMine(t, hub, "push")
	u2()
	wp04lockAssertAbsent(t, hub)
}

func TestUnlockKeepsForeignLock(t *testing.T) {
	hub := wp04lockHub(t)
	unlock, err := Lock(hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Another acquisition replaced ours (e.g. it judged ours stale).
	foreign := wp04lockWrite(t, hub, LockInfo{PID: os.Getpid() + 1, Host: wp04lockHost(), Command: "pull", StartedAt: wp04lockNow().Add(time.Second)})
	unlock()
	if got := wp04lockReadFile(t, hub); !bytes.Equal(got, foreign) {
		t.Fatalf("unlock changed a foreign lock: %q -> %q", foreign, got)
	}
}

func TestDefaultLockWaitEnv(t *testing.T) {
	cases := []struct {
		env  string
		want time.Duration
	}{
		{"3s", 3 * time.Second},
		{"7", 7 * time.Second},
		{"500ms", 500 * time.Millisecond},
		{" 5 ", 5 * time.Second},
		{"0", 0},
		{"0s", 0},
		{"", 10 * time.Second},
		{"bogus", 10 * time.Second},
		{"1.5", 10 * time.Second},
		{"-1", 10 * time.Second},
		{"-2s", 10 * time.Second},
		{"99999999999999999999", 10 * time.Second},
		{"9223372036854775807", 10 * time.Second},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.env), func(t *testing.T) {
			t.Setenv(lockTimeoutEnv, tc.env)
			if got := DefaultLockWait(); got != tc.want {
				t.Errorf("DefaultLockWait() with %s=%q = %v, want %v", lockTimeoutEnv, tc.env, got, tc.want)
			}
		})
	}
	t.Run("unset", func(t *testing.T) {
		t.Setenv(lockTimeoutEnv, "x")
		if err := os.Unsetenv(lockTimeoutEnv); err != nil {
			t.Fatal(err)
		}
		if got := DefaultLockWait(); got != 10*time.Second {
			t.Errorf("DefaultLockWait() unset = %v, want 10s", got)
		}
	})
}

func TestWP04LockStaleReclaimSingleWinner(t *testing.T) {
	dead := wp04lockDeadPID(t)
	const workers = 8
	for round := 0; round < 20; round++ {
		hub := wp04lockHub(t)
		wp04lockWrite(t, hub, LockInfo{PID: dead, Host: wp04lockHost(), Command: "push", StartedAt: wp04lockNow()})

		start := make(chan struct{})
		var wg sync.WaitGroup
		unlocks := make([]func(), workers)
		oks := make([]bool, workers)
		errs := make([]error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				unlocks[i], oks[i], errs[i] = TryLock(hub, fmt.Sprintf("g%d", i))
			}()
		}
		close(start)
		wg.Wait()

		winner := -1
		for i := 0; i < workers; i++ {
			if errs[i] != nil {
				t.Fatalf("round %d: TryLock g%d: %v", round, i, errs[i])
			}
			if oks[i] {
				if winner >= 0 {
					t.Fatalf("round %d: both g%d and g%d acquired the reclaimed lock", round, winner, i)
				}
				winner = i
			}
		}
		if winner < 0 {
			t.Fatalf("round %d: no TryLock reclaimed the stale lock", round)
		}
		wp04lockAssertMine(t, hub, fmt.Sprintf("g%d", winner))
		unlocks[winner]()
		wp04lockAssertAbsent(t, hub)
		if got := strings.Join(wp04lockSyncNames(t, hub), ","); got != ".gitignore" {
			t.Fatalf("round %d: .sync holds %s, want only .gitignore", round, got)
		}
	}
}

func TestWP04LockMutualExclusion(t *testing.T) {
	hub := wp04lockHub(t)
	var inside, entries atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				unlock, err := Lock(hub, fmt.Sprintf("g%d", g), 30*time.Second)
				if err != nil {
					errs <- err
					return
				}
				if !inside.CompareAndSwap(0, 1) {
					errs <- errors.New("two goroutines inside the lock")
				}
				entries.Add(1)
				inside.Store(0)
				unlock()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if entries.Load() != 20 {
		t.Fatalf("entries = %d, want 20", entries.Load())
	}
	wp04lockAssertAbsent(t, hub)
}

func TestWP04LockWaitsForRelease(t *testing.T) {
	hub := wp04lockHub(t)
	held, err := Lock(hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	time.AfterFunc(150*time.Millisecond, held)
	unlock, err := Lock(hub, "pull", 10*time.Second)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Lock while the holder releases: %v", err)
	}
	if elapsed < 150*time.Millisecond || elapsed > 5*time.Second {
		t.Errorf("Lock returned after %v, want just after the 150ms release", elapsed)
	}
	wp04lockAssertMine(t, hub, "pull")
	unlock()
}

func TestWP04LockNegativeWaitIsZero(t *testing.T) {
	hub := wp04lockHub(t)
	held, err := Lock(hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	start := time.Now()
	_, err = Lock(hub, "pull", -time.Second)
	wp04lockBusy(t, err)
	if d := time.Since(start); d > time.Second {
		t.Errorf("Lock with a negative wait took %v", d)
	}
}

func TestWP04LockCreatesSyncGitignore(t *testing.T) {
	hub := wp04lockHub(t)
	unlock, err := Lock(hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	got, err := os.ReadFile(filepath.Join(SyncDir(hub), ".gitignore"))
	if err != nil || string(got) != "*\n" {
		t.Fatalf(".sync/.gitignore = %q (err %v), want \"*\\n\"", got, err)
	}
}

func TestWP04LockIOError(t *testing.T) {
	t.Run("hub_is_a_file", func(t *testing.T) {
		hub := filepath.Join(t.TempDir(), "hub")
		if err := os.WriteFile(hub, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		u, err := Lock(hub, "push", 0)
		var be *BusyError
		if err == nil || errors.As(err, &be) || u != nil {
			t.Fatalf("Lock with a file as hub: unlock=%v err=%v, want an I/O error", u != nil, err)
		}
		u, ok, err := TryLock(hub, "status")
		if err == nil || ok || u != nil {
			t.Fatalf("TryLock with a file as hub: ok=%v err=%v, want an I/O error", ok, err)
		}
	})
	t.Run("lock_is_a_directory", func(t *testing.T) {
		hub := wp04lockHub(t)
		if err := os.MkdirAll(LockPath(hub), 0o755); err != nil {
			t.Fatal(err)
		}
		u, err := Lock(hub, "push", 0)
		var be *BusyError
		if err == nil || errors.As(err, &be) || u != nil {
			t.Fatalf("Lock with a directory at the lock path: err=%v, want an I/O error", err)
		}
	})
}

func TestWP04LockFreshGuardBlocksReclaim(t *testing.T) {
	hub := wp04lockHub(t)
	planted := wp04lockWrite(t, hub, LockInfo{PID: wp04lockDeadPID(t), Host: wp04lockHost(), Command: "push", StartedAt: wp04lockNow()})
	// Another process is in the middle of reclaiming it.
	if err := os.WriteFile(lockGuardPath(hub), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	u, ok, err := TryLock(hub, "status")
	if err != nil || ok || u != nil {
		t.Fatalf("TryLock during another reclaim: ok=%v err=%v", ok, err)
	}
	if got := wp04lockReadFile(t, hub); !bytes.Equal(got, planted) {
		t.Fatalf("lock changed without the guard: %q -> %q", planted, got)
	}
}

func TestWP04LockStaleGuardIsCleared(t *testing.T) {
	hub := wp04lockHub(t)
	wp04lockWrite(t, hub, LockInfo{PID: wp04lockDeadPID(t), Host: wp04lockHost(), Command: "push", StartedAt: wp04lockNow()})
	// A reclaimer died holding the guard a minute ago.
	guard := lockGuardPath(hub)
	if err := os.WriteFile(guard, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(guard, old, old); err != nil {
		t.Fatal(err)
	}
	u, ok, err := TryLock(hub, "status")
	if err != nil || !ok {
		t.Fatalf("TryLock past a dead reclaimer's guard: ok=%v err=%v", ok, err)
	}
	wp04lockAssertMine(t, hub, "status")
	u()
	if got := strings.Join(wp04lockSyncNames(t, hub), ","); got != ".gitignore" {
		t.Errorf(".sync holds %s, want only .gitignore", got)
	}
}

func TestWP04UnlockWaitsForGuard(t *testing.T) {
	hub := wp04lockHub(t)
	unlock, err := Lock(hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	guard := lockGuardPath(hub)
	if err := os.WriteFile(guard, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	time.AfterFunc(100*time.Millisecond, func() { _ = os.Remove(guard) })
	unlock()
	if d := time.Since(start); d < 100*time.Millisecond {
		t.Errorf("unlock returned after %v, while another process held the guard", d)
	}
	wp04lockAssertAbsent(t, hub)
	if _, err := os.Lstat(guard); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("unlock left the guard behind (lstat err %v)", err)
	}
}

func TestWP04UnlockStealsStaleGuard(t *testing.T) {
	hub := wp04lockHub(t)
	unlock, err := Lock(hub, "push", 0)
	if err != nil {
		t.Fatal(err)
	}
	// A reclaimer died holding the guard, which goes stale in ~200ms.
	guard := lockGuardPath(hub)
	if err := os.WriteFile(guard, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-lockGuardStaleAge + 200*time.Millisecond)
	if err := os.Chtimes(guard, old, old); err != nil {
		t.Fatal(err)
	}
	unlock()
	wp04lockAssertAbsent(t, hub)
	if got := strings.Join(wp04lockSyncNames(t, hub), ","); got != ".gitignore" {
		t.Errorf(".sync holds %s, want only .gitignore", got)
	}
}

func TestWP04ReleaseLockFallsBackWithoutGuard(t *testing.T) {
	hub := wp04lockHub(t)
	if err := EnsureSyncDir(hub); err != nil {
		t.Fatal(err)
	}
	// A guard that can never be taken or cleared: a non-empty directory.
	guard := lockGuardPath(hub)
	if err := os.MkdirAll(filepath.Join(guard, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	ours := wp04lockWrite(t, hub, LockInfo{PID: os.Getpid(), Host: wp04lockHost(), Command: "push", StartedAt: wp04lockNow()})
	releaseLock(hub, ours, 50*time.Millisecond)
	wp04lockAssertAbsent(t, hub)

	foreign := wp04lockWrite(t, hub, LockInfo{PID: os.Getpid() + 1, Host: wp04lockHost(), Command: "pull", StartedAt: wp04lockNow()})
	releaseLock(hub, ours, 50*time.Millisecond)
	if got := wp04lockReadFile(t, hub); !bytes.Equal(got, foreign) {
		t.Fatalf("fallback release removed a foreign lock: %q -> %q", foreign, got)
	}
}
