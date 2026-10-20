//go:build !windows

package fsutil

// isRetryableRename reports whether a failed rename is worth retrying. POSIX
// rename replaces open files, so nothing is retried here.
func isRetryableRename(err error) bool {
	return false
}
