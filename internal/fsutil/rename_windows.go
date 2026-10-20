//go:build windows

package fsutil

import (
	"errors"
	"syscall"
)

// errorSharingViolation is ERROR_SHARING_VIOLATION, which package syscall
// does not name.
const errorSharingViolation syscall.Errno = 32

// isRetryableRename reports whether a failed rename is worth retrying:
// ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION from another process that
// has the target open.
func isRetryableRename(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, errorSharingViolation)
}
