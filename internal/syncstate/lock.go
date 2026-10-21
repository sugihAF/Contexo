package syncstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LockInfo is the JSON content of .sync/lock. It marshals to exactly the
// contract's busy details: {"pid","host","command","started_at"}, with
// StartedAt in UTC whole seconds.
type LockInfo struct {
	PID       int       `json:"pid"`
	Host      string    `json:"host"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"started_at"`
}

// BusyError is returned by Lock when the wait expires while another process
// holds the lock. Its contract code is busy (exit 12, retryable).
type BusyError struct {
	Holder LockInfo
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("another ctx process is syncing (%s, pid %d on %s, since %s)",
		e.Holder.Command, e.Holder.PID, e.Holder.Host, e.Holder.StartedAt.UTC().Format(time.RFC3339))
}

// ErrorCode returns the contract error code, "busy".
func (e *BusyError) ErrorCode() string { return "busy" }

// ErrorAdvice returns no advice (a non-nil empty slice).
func (e *BusyError) ErrorAdvice() []string { return []string{} }

// ErrorDetails returns the holder, which marshals to the busy details.
func (e *BusyError) ErrorDetails() any { return e.Holder }

// Stale-lock thresholds (contract §4).
const (
	lockStaleAge       = 10 * time.Minute // any lock older than this is reclaimed
	lockUnparsableAge  = 30 * time.Second // an unparseable lock older than this (mtime) is reclaimed
	defaultLockTimeout = 10 * time.Second
	lockTimeoutEnv     = "CONTEXO_LOCK_TIMEOUT"
)

// Polling and removal policy. A waiting Lock polls every lockPollMin,
// doubling up to lockPollMax. Removing the lock or its guard is retried like
// fsutil's rename (contract §4): on Windows a reader that has the file open
// makes the delete fail with access-denied or a sharing violation.
const (
	lockPollMin          = 25 * time.Millisecond
	lockPollMax          = 250 * time.Millisecond
	lockRoundAttempts    = 4 // create/inspect/reclaim passes per poll
	lockRemoveAttempts   = 5
	lockRemoveRetryDelay = 50 * time.Millisecond
	lockMaxFileSize      = 64 << 10 // a larger lock file is garbage; read only this much
)

// The removal guard. Every removal of .sync/lock (a stale reclaim or an
// unlock) happens while holding .sync/lock.guard, a file created with
// O_CREATE|O_EXCL and removed right after. Creating the lock needs no
// guard: O_EXCL already lets only one process create it. With removals
// serialized, the guard holder's re-read and remove act as one
// compare-and-delete, so two waiters reclaiming the same stale lock cannot
// both win, and an unlock never deletes a lock another acquisition wrote.
// A guard is held for well under a second; one older than lockGuardStaleAge
// belongs to a process that died inside the critical section and is
// cleared. Clearing is itself a compare-and-delete (clearStaleGuard), and a
// holder releases its guard only while the path still names the file it
// created (releaseLockGuard), so no process deletes another's fresh guard.
const (
	lockGuardStaleAge = 5 * time.Second
	lockGuardPoll     = 10 * time.Millisecond
	lockGuardWait     = lockGuardStaleAge + time.Second // how long unlock waits for the guard
)

// lockGuardPath returns <hub>/.sync/lock.guard.
func lockGuardPath(hub string) string { return LockPath(hub) + ".guard" }

// lockRetryError is a transient failure (see isTransientLockErr) that blocked
// one acquisition round. Lock keeps polling through it and returns it only if
// it persists until the deadline.
type lockRetryError struct{ err error }

func (e *lockRetryError) Error() string { return e.err.Error() }
func (e *lockRetryError) Unwrap() error { return e.err }

// DefaultLockWait is how long push, pull, resolve and repair wait for the
// lock: env CONTEXO_LOCK_TIMEOUT as a Go duration ("3s") or integer seconds
// ("7"), otherwise 10s.
func DefaultLockWait() time.Duration {
	v := strings.TrimSpace(os.Getenv(lockTimeoutEnv))
	if v == "" {
		return defaultLockTimeout
	}
	if d, err := time.ParseDuration(v); err == nil {
		if d < 0 {
			return defaultLockTimeout
		}
		return d
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 || n > math.MaxInt64/int64(time.Second) {
		return defaultLockTimeout
	}
	return time.Duration(n) * time.Second
}

// Lock takes the cross-process .sync/lock for command, waiting up to wait
// for a live holder to release it. Stale locks (same host and dead PID,
// older than 10 min, or unparseable with an mtime older than 30 s) are
// removed and acquisition retried. On timeout it returns *BusyError. The
// lock is not reentrant. unlock is idempotent and removes the file only
// while it still holds this acquisition's pid, host and started_at.
func Lock(hubDir, command string, wait time.Duration) (unlock func(), err error) {
	deadline := time.Now().Add(max(wait, 0))
	host := lockHost()
	poll := lockPollMin
	var holder LockInfo
	for {
		unlock, h, err := acquireOnce(hubDir, command, host)
		if unlock != nil {
			return unlock, nil
		}
		var retry *lockRetryError
		switch {
		case errors.As(err, &retry):
		case err != nil:
			return nil, err
		default:
			holder = h
		}
		now := time.Now()
		if !now.Before(deadline) {
			if retry != nil {
				return nil, fmt.Errorf("syncstate: lock: %w", retry.err)
			}
			holder.StartedAt = holder.StartedAt.UTC().Truncate(time.Second)
			return nil, &BusyError{Holder: holder}
		}
		time.Sleep(min(poll, deadline.Sub(now)))
		poll = min(2*poll, lockPollMax)
	}
}

// TryLock is Lock without waiting: ok is false (with a nil error) when a
// live holder has the lock. err reports only I/O failures.
func TryLock(hubDir, command string) (unlock func(), ok bool, err error) {
	unlock, _, err = acquireOnce(hubDir, command, lockHost())
	if unlock != nil {
		return unlock, true, nil
	}
	var retry *lockRetryError
	if err != nil && !errors.As(err, &retry) {
		return nil, false, err
	}
	return nil, false, nil
}

// Seams for lockHost: the kernel release string ("" off linux) and the
// environment.
var (
	lockOSRelease = osRelease
	lockGetenv    = os.Getenv
)

// lockHost is the host name recorded in, and compared against, lock files.
// It names the pid namespace a lock's pid belongs to, not just the machine:
// WSL inherits the Windows computer name but has its own pids, so inside
// WSL (a linux kernel release containing "microsoft" or "wsl") the hostname
// gets " (wsl:<$WSL_DISTRO_NAME>)", or " (wsl)" without a distro name. A
// lock from the other side then falls under the cross-host rule.
func lockHost() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		h = "unknown"
	}
	rel := strings.ToLower(lockOSRelease())
	if !strings.Contains(rel, "microsoft") && !strings.Contains(rel, "wsl") {
		return h
	}
	if distro := lockGetenv("WSL_DISTRO_NAME"); distro != "" {
		return h + " (wsl:" + distro + ")"
	}
	return h + " (wsl)"
}

// acquireOnce makes one acquisition round: create the lock, or inspect the
// existing one and reclaim it when stale. It returns a non-nil unlock on
// success. Otherwise holder is the live holder (zero when its file is not
// parseable yet) and err is nil, or err is an I/O failure, wrapped in
// *lockRetryError when transient.
func acquireOnce(hub, command, host string) (unlock func(), holder LockInfo, err error) {
	if err := EnsureSyncDir(hub); err != nil {
		return nil, LockInfo{}, lockIOError(err)
	}
	path := LockPath(hub)
	for attempt := 0; attempt < lockRoundAttempts; attempt++ {
		ours, err := createLockFile(path, command, host)
		if err == nil {
			return newUnlock(hub, ours), LockInfo{}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, LockInfo{}, lockIOError(err)
		}
		cur, curFI, err := readLockFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue // released since the create failed
		}
		if err != nil {
			return nil, LockInfo{}, lockIOError(err)
		}
		info, stale := judgeLock(cur, curFI.ModTime(), host, time.Now())
		if !stale && info.Host == host && info.PID == os.Getpid() && !heldHere(cur) {
			// Our pid, but no acquisition of this process wrote it: a dead
			// predecessor that had the same pid (a restarted container's
			// pid 1), which processAlive cannot tell from us.
			stale = true
		}
		holder = info
		if !stale {
			return nil, holder, nil
		}
		reclaimed, err := reclaimLock(hub, cur, curFI)
		if err != nil {
			return nil, holder, err
		}
		if !reclaimed {
			return nil, holder, nil // another process holds the guard
		}
	}
	return nil, holder, nil
}

// lockIOError wraps an acquisition I/O failure, marking transient ones.
func lockIOError(err error) error {
	if isTransientLockErr(err) {
		return &lockRetryError{err: err}
	}
	return fmt.Errorf("syncstate: lock: %w", err)
}

// heldLocks counts the lock contents that acquisitions of this process
// currently hold (on any hub). A lock file naming this process's pid whose
// content is not counted here was left by a dead predecessor with the same
// pid. A content is counted from before its file is created until after
// unlock removed it, so another goroutine never mistakes a live acquisition
// of this process for such an orphan.
var heldLocks = struct {
	sync.Mutex
	n map[string]int
}{n: map[string]int{}}

// markHeld adds delta to the held count of lock content data.
func markHeld(data []byte, delta int) {
	heldLocks.Lock()
	defer heldLocks.Unlock()
	if heldLocks.n[string(data)] += delta; heldLocks.n[string(data)] <= 0 {
		delete(heldLocks.n, string(data))
	}
}

// heldHere reports whether an acquisition of this process holds a lock with
// content data.
func heldHere(data []byte) bool {
	heldLocks.Lock()
	defer heldLocks.Unlock()
	return heldLocks.n[string(data)] > 0
}

// createLockFile creates path with O_CREATE|O_EXCL and writes this
// acquisition's LockInfo to it. It returns the bytes written, which unlock
// compares against later. On success the content is counted in heldLocks
// until unlock releases it.
func createLockFile(path, command, host string) ([]byte, error) {
	data, err := json.Marshal(LockInfo{
		PID:       os.Getpid(),
		Host:      host,
		Command:   command,
		StartedAt: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	markHeld(data, 1)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		markHeld(data, -1)
		return nil, err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		// The file is ours and too young for anyone to judge it stale, so
		// it is removed without the guard.
		_ = removeLockFile(path)
		markHeld(data, -1)
		return nil, err
	}
	return data, nil
}

// readLockFile returns the lock file's content (at most lockMaxFileSize
// bytes) and its identity and mtime, both from the same open file (see
// statGuard). Anything but a regular file at the lock path is an error,
// never a holder.
func readLockFile(path string) ([]byte, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s: not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, lockMaxFileSize))
	if err != nil {
		return nil, nil, err
	}
	return data, fi, nil
}

// judgeLock decides whether lock content data, last modified at mtime, is
// stale for a process on host at now. It returns the parsed holder (zero
// when the content does not decode).
//
//   - Not valid JSON (empty or torn): possibly still being written, so stale
//     only once its mtime is older than lockUnparsableAge.
//   - Complete JSON that does not decode to a LockInfo (pid not an integer,
//     started_at not a time, not an object), or a pid <= 0: no live ctx
//     wrote it, so stale on any host. kill(0, 0) would signal the whole
//     process group and report it alive.
//   - started_at older than lockStaleAge: stale on any host.
//   - Same host and the pid is not a live process: stale.
//
// A lock from another host with a live-looking pid is never stale before
// lockStaleAge, because its pid cannot be checked here.
func judgeLock(data []byte, mtime time.Time, host string, now time.Time) (LockInfo, bool) {
	if !json.Valid(data) {
		return LockInfo{}, now.Sub(mtime) > lockUnparsableAge
	}
	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return LockInfo{}, true
	}
	if info.PID <= 0 || now.Sub(info.StartedAt) > lockStaleAge {
		return info, true
	}
	return info, info.Host == host && !processAlive(info.PID)
}

// reclaimLock removes the stale lock judged under the removal guard, only
// while the lock path still names that same file (judgedFI) with exactly
// the judged content: comparing bytes alone could delete a lock created
// since the judgement that has the same bytes (an empty one, not written
// yet). reclaimed is true when the caller should retry creating the lock
// now (the stale lock is gone, or it changed and must be judged again);
// false means another process holds the guard.
func reclaimLock(hub string, judged []byte, judgedFI os.FileInfo) (reclaimed bool, err error) {
	guard := lockGuardPath(hub)
	held, err := takeLockGuard(guard)
	if err != nil {
		return false, lockIOError(err)
	}
	if held == nil {
		return false, nil
	}
	defer releaseLockGuard(guard, held)
	if _, err := removeLockIfUnchanged(LockPath(hub), judged, judgedFI); err != nil {
		return false, lockIOError(err)
	}
	return true, nil
}

// takeLockGuard tries once to create the removal guard, first clearing a
// guard older than lockGuardStaleAge. held identifies the guard file this
// process created, for releaseLockGuard; it is nil when another process
// holds a fresh guard (or a transient error got in the way).
func takeLockGuard(guard string) (held os.FileInfo, err error) {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(guard, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			// Only the file's existence and identity matter; a failed close
			// leaves it.
			held, err := f.Stat()
			_ = f.Close()
			if err != nil {
				// Unidentifiable, so it could never be released safely. It
				// is too young for anyone else to clear.
				_ = removeLockFile(guard)
				return nil, err
			}
			return held, nil
		}
		if isTransientLockErr(err) {
			return nil, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		fi, err := statGuard(guard)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			if isTransientLockErr(err) {
				return nil, nil
			}
			return nil, err
		}
		if time.Since(fi.ModTime()) <= lockGuardStaleAge {
			return nil, nil
		}
		// Its holder died inside the critical section.
		if !clearStaleGuard(guard, fi) {
			return nil, nil
		}
	}
	return nil, nil
}

// statGuard returns the identity and mtime of the file at guard, both from
// one open handle. (os.Lstat would not do: on Windows its result loads the
// file id lazily, from whatever the path names when os.SameFile runs.)
func statGuard(guard string) (os.FileInfo, error) {
	f, err := os.Open(guard)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

// sameGuard reports whether a and b are the same guard (or lock) file,
// unchanged: the same file (a reused inode number shows up as a moved
// mtime) with the same mtime.
func sameGuard(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.ModTime().Equal(b.ModTime())
}

// clearStaleGuard removes the guard judged stale, as a compare-and-delete:
// it renames whatever is at guard to a unique name next to it and checks
// that this is still the judged file. If it is not (another process cleared
// the stale guard and took a fresh one since the judgement), the file is
// put back with a hard link and the clear fails. It reports whether the
// caller should retry creating the guard: the judged guard is gone, by this
// call or another process.
func clearStaleGuard(guard string, judged os.FileInfo) bool {
	if !judged.Mode().IsRegular() {
		return false // not a guard this package wrote; never moved
	}
	aside := guard + ".stale-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatUint(rand.Uint64(), 36)
	if err := os.Rename(guard, aside); err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	moved, err := statGuard(aside)
	if err == nil && sameGuard(moved, judged) {
		_ = removeLockFile(aside)
		return true
	}
	// Someone else's guard: put it back. A guard created at the path in
	// the meantime wins (os.Link fails with ErrExist). Without hard links
	// (FAT, some network shares) rename it back instead.
	if err := os.Link(aside, guard); err != nil && !errors.Is(err, fs.ErrExist) {
		if os.Rename(aside, guard) == nil {
			return false
		}
	}
	_ = removeLockFile(aside)
	return false
}

// releaseLockGuard removes the guard this process created (held), only
// while the guard path still names that file: a holder paused past
// lockGuardStaleAge may have had it cleared and replaced by another
// process's guard, which must stay.
func releaseLockGuard(guard string, held os.FileInfo) {
	cur, err := statGuard(guard)
	if err != nil || !sameGuard(cur, held) {
		return
	}
	_ = removeLockFile(guard)
}

// newUnlock returns the idempotent unlock for an acquisition that wrote
// ours to the lock file.
func newUnlock(hub string, ours []byte) func() {
	var once sync.Once
	return func() {
		once.Do(func() { releaseLock(hub, ours, lockGuardWait) })
	}
}

// releaseLock removes the lock file if it still holds exactly ours, under
// the removal guard. It waits up to guardWait for the guard; past that (a
// guard that can be neither taken nor cleared) it falls back to an
// unguarded re-read and remove, since leaving a lock behind would block
// every other ctx until it goes stale. Errors are dropped: unlock has no
// way to report them, and a lock left behind is reclaimed once stale.
func releaseLock(hub string, ours []byte, guardWait time.Duration) {
	path, guard := LockPath(hub), lockGuardPath(hub)
	deadline := time.Now().Add(guardWait)
	for {
		held, err := takeLockGuard(guard)
		if held != nil {
			_, _ = removeLockIfUnchanged(path, ours, nil)
			releaseLockGuard(guard, held)
			markHeld(ours, -1)
			return
		}
		if err != nil || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(lockGuardPoll)
	}
	_, _ = removeLockIfUnchanged(path, ours, nil)
	markHeld(ours, -1)
}

// removeLockIfUnchanged re-reads path and removes it only if it holds
// exactly want and, when wantFI is non-nil, is still that file (see
// sameGuard). A missing file is not an error. unlock passes no identity:
// its bytes (pid, host, command and second) are already unique to it.
func removeLockIfUnchanged(path string, want []byte, wantFI os.FileInfo) (removed bool, err error) {
	cur, curFI, err := readLockFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !bytes.Equal(cur, want) || (wantFI != nil && !sameGuard(curFI, wantFI)) {
		return false, nil
	}
	if err := removeLockFile(path); err != nil {
		return false, err
	}
	return true, nil
}

// removeLockFile removes path, retrying transient failures (see
// removeRetry). A missing file is not an error.
func removeLockFile(path string) error {
	return removeRetry(os.Remove, isTransientLockErr, path)
}

// removeRetry calls remove(path), retrying failures that transient accepts
// at most lockRemoveAttempts times lockRemoveRetryDelay apart. A missing
// file is not an error. The lock files and the base and conflict stores
// delete through it.
func removeRetry(remove func(string) error, transient func(error) bool, path string) error {
	for attempt := 1; ; attempt++ {
		err := remove(path)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if attempt >= lockRemoveAttempts || !transient(err) {
			return err
		}
		time.Sleep(lockRemoveRetryDelay)
	}
}
