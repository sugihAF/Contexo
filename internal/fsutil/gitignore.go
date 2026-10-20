package fsutil

// EnsureSelfIgnoringDir creates dir (MkdirAll) and, only when dir/.gitignore
// is absent, atomically writes it with the content "*\n", so nothing under
// dir is ever committed (E41). An existing .gitignore is left byte-identical.
func EnsureSelfIgnoringDir(dir string) error {
	return errNotImplemented
}
