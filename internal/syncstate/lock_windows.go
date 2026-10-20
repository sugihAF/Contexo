//go:build windows

package syncstate

import (
	"errors"
	"math"
	"syscall"
)

// Win32 values package syscall does not name.
const (
	processQueryLimitedInformation = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION
	stillActive                    = 259    // STILL_ACTIVE exit code

	errorInvalidParameter syscall.Errno = 87 // ERROR_INVALID_PARAMETER: no such process
	errorSharingViolation syscall.Errno = 32 // ERROR_SHARING_VIOLATION
)

// processAlive reports whether pid names a live process on this host.
// OpenProcess fails with ERROR_INVALID_PARAMETER for a missing process; any
// other failure (access denied for a protected process) counts as alive, so
// an unexpected error never steals a lock. An opened process is alive while
// its exit code is STILL_ACTIVE. pid <= 0 and pids beyond a DWORD are dead.
func processAlive(pid int) bool {
	if pid <= 0 || uint64(pid) > math.MaxUint32 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return !errors.Is(err, errorInvalidParameter)
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}

// isTransientLockErr reports whether a lock-file operation failed only
// because another process had the file open, or it is pending deletion:
// ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION. Such failures are retried.
func isTransientLockErr(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, errorSharingViolation)
}
