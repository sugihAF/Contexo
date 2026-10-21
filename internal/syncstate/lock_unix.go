//go:build !windows

package syncstate

import (
	"errors"
	"math"
	"os"
	"runtime"
	"strings"
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

// osRelease returns the kernel release on linux
// (/proc/sys/kernel/osrelease), or "" elsewhere or when it cannot be read.
func osRelease() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// isTransientLockErr reports whether a lock-file operation failed only
// because another process had the file open. POSIX never fails that way.
func isTransientLockErr(err error) bool {
	return false
}
