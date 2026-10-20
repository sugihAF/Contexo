// Package fsutil holds crash-safe file-system helpers shared by the sync
// state stores (internal/sync and internal/syncstate). It uses the standard
// library only, so every package can import it without cycles.
package fsutil

import (
	"errors"
	"os"
	"time"
)

// Rename retry policy for WriteFileAtomic (contract §4): on Windows a rename
// over a file another process has open fails with access-denied or a sharing
// violation, so the rename is attempted at most renameAttempts times,
// renameRetryDelay apart. Other platforms never retry.
const (
	renameAttempts   = 5
	renameRetryDelay = 50 * time.Millisecond
)

var errNotImplemented = errors.New("fsutil: not implemented")

// WriteFileAtomic writes data to path so that a concurrent reader sees either
// the old content or the new content, never a mix. It creates the parent
// directory, writes a temp file '.<base>.tmp-<rand>' in the same directory
// (the name never ends in .md, so page walks never list it), then Write,
// Sync, Close, Chmod(perm) and Rename over path.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return errNotImplemented
}

// renameRetry renames oldpath to newpath, retrying while retryable(err)
// holds, at most renameAttempts attempts renameRetryDelay apart. It is the
// seam the retry loop is unit-tested through.
func renameRetry(rename func(oldpath, newpath string) error, retryable func(error) bool, sleep func(time.Duration), oldpath, newpath string) error {
	return errNotImplemented
}
