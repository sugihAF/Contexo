package syncstate

// PathError reports a page path that fails the contract path rules. Its
// contract code is invalid_path (exit 3).
type PathError struct {
	Path   string
	Reason string
}

func (e *PathError) Error() string {
	return "invalid page path " + `"` + e.Path + `"` + ": " + e.Reason
}

// ErrorCode returns the contract error code, "invalid_path".
func (e *PathError) ErrorCode() string { return "invalid_path" }

// ErrorAdvice returns no advice (a non-nil empty slice).
func (e *PathError) ErrorAdvice() []string { return []string{} }

// ValidateRelPath checks a page path relative to .contexo: '/'-separated,
// ends in .md, no empty, '.' or '..' segment, no segment starting with '-'
// or '.', no control characters. It mirrors the server's validatePagePath
// plus the .md and no-dot-segment rules, so .sync/*, credentials.json and
// config.json can never be named. Failures are *PathError.
func ValidateRelPath(rel string) error {
	return errNotImplemented
}

// NormalizeRelPath turns a user-supplied page argument into a contract path.
// It accepts "wiki/concepts/x.md", ".contexo/wiki/concepts/x.md" (relative to
// root), an absolute path under hubDir, and Windows backslashes, and rejects
// anything escaping the hub. The result passes ValidateRelPath; failures are
// *PathError.
func NormalizeRelPath(root, hubDir, arg string) (string, error) {
	return "", errNotImplemented
}
