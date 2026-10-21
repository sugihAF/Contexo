// Package fsutil holds crash-safe file-system helpers shared by the sync
// state stores (internal/sync and internal/syncstate). It uses the standard
// library only, so every package can import it without cycles.
package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
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

// WriteFileAtomic writes data to path so that a concurrent reader sees either
// the old content or the new content, never a mix. It creates the parent
// directory, writes a temp file '.<base>.tmp-<rand>' in the same directory
// (the name never ends in .md, so page walks never list it), then Write,
// Sync, Close, Chmod(perm) and Rename over path. On any failure the temp
// file is removed and path is left as it was.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(path, data, perm, os.Rename)
}

// writeFileAtomic is WriteFileAtomic with the rename injected, so tests can
// observe the temp file at the moment it is renamed.
func writeFileAtomic(path string, data []byte, perm os.FileMode, rename func(oldpath, newpath string) error) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file 0o600; Chmod sets perm exactly (no umask).
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	if err := renameRetry(rename, isRetryableRename, time.Sleep, tmp, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir makes a completed rename durable by fsyncing its directory. It is
// best effort: Windows cannot sync a directory handle and some file systems
// refuse it, and the rename itself has already succeeded.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// renameRetry renames oldpath to newpath, retrying while retryable(err)
// holds, at most renameAttempts attempts renameRetryDelay apart. It is the
// seam the retry loop is unit-tested through.
func renameRetry(rename func(oldpath, newpath string) error, retryable func(error) bool, sleep func(time.Duration), oldpath, newpath string) error {
	for attempt := 1; ; attempt++ {
		err := rename(oldpath, newpath)
		if err == nil || attempt >= renameAttempts || !retryable(err) {
			return err
		}
		sleep(renameRetryDelay)
	}
}
