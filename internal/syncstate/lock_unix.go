//go:build !windows

package syncstate

import (
	"errors"
	"math"
	"syscall"
)

// processAlive reports whether pid names a live process on this host.
// kill(pid, 0) succeeds or fails with EPERM for a live process and fails
// with ESRCH for a missing one; any other outcome counts as alive, so an
// unexpected error never steals a lock. pid <= 0 would address a process
// group, and a pid beyond pid_t would be truncated by the call, so both are
// treated as dead.
func processAlive(pid int) bool {
	if pid <= 0 || int64(pid) > math.MaxInt32 {
		return false
	}
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// isTransientLockErr reports whether a lock-file operation failed only
// because another process had the file open. POSIX never fails that way.
func isTransientLockErr(err error) bool {
	return false
}
