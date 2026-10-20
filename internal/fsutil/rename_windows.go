//go:build windows

package fsutil

// isRetryableRename reports whether a failed rename is worth retrying:
// ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION from another process that
// has the target open.
func isRetryableRename(err error) bool {
	return false
}
