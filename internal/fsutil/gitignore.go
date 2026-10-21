package fsutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// EnsureSelfIgnoringDir creates dir (MkdirAll) and, only when dir/.gitignore
// is absent, atomically writes it with the content "*\n", so nothing under
// dir is ever committed (E41). An existing .gitignore is left byte-identical.
func EnsureSelfIgnoringDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, ".gitignore")
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// Two processes racing here both write the same bytes, so whichever
	// rename lands last changes nothing.
	return WriteFileAtomic(path, []byte("*\n"), 0o644)
}
