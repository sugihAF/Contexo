package syncstate

import (
	"fmt"
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

// DefaultLockWait is how long push, pull, resolve and repair wait for the
// lock: env CONTEXO_LOCK_TIMEOUT as a Go duration ("3s") or integer seconds
// ("7"), otherwise 10s.
func DefaultLockWait() time.Duration {
	return defaultLockTimeout
}

// Lock takes the cross-process .sync/lock for command, waiting up to wait
// for a live holder to release it. Stale locks (same host and dead PID,
// older than 10 min, or unparseable with an mtime older than 30 s) are
// removed and acquisition retried. On timeout it returns *BusyError. The
// lock is not reentrant. unlock is idempotent and removes the file only
// while it still holds this acquisition's pid, host and started_at.
func Lock(hubDir, command string, wait time.Duration) (unlock func(), err error) {
	return nil, errNotImplemented
}

// TryLock is Lock without waiting: ok is false (with a nil error) when a
// live holder has the lock. err reports only I/O failures.
func TryLock(hubDir, command string) (unlock func(), ok bool, err error) {
	return nil, false, errNotImplemented
}
