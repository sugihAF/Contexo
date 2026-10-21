package syncstate

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

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
	if reason := relPathProblem(rel); reason != "" {
		return &PathError{Path: rel, Reason: reason}
	}
	return nil
}

// relPathProblem returns why rel is not a page path, or "" when it is one.
// Beyond the documented rules it rejects '\' (rel is '/'-separated), ':'
// (a drive or an NTFS stream on Windows; no slug contains one), invalid
// UTF-8 (it cannot cross the JSON wire intact) and anything this OS does
// not consider a local path (Windows reserved names).
func relPathProblem(rel string) string {
	if rel == "" {
		return "empty path"
	}
	if !utf8.ValidString(rel) {
		return "not valid UTF-8"
	}
	for _, r := range rel {
		switch {
		case r < 0x20 || r == 0x7f:
			return "contains a control character"
		case r == '\\':
			return `contains "\", use "/" separators`
		case r == ':':
			return `contains ":"`
		}
	}
	if strings.HasPrefix(rel, "/") {
		return "absolute path, want a path relative to .contexo"
	}
	for _, seg := range strings.Split(rel, "/") {
		switch {
		case seg == "":
			return "empty path segment"
		case seg == "." || seg == "..":
			return `contains a "` + seg + `" segment`
		case strings.HasPrefix(seg, "-"):
			return `a path segment starts with "-"`
		case strings.HasPrefix(seg, "."):
			return `a path segment starts with "." (dot-files and .sync are not pages)`
		}
	}
	if !strings.HasSuffix(rel, ".md") {
		return "not a .md page"
	}
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "not a valid local file name on this system"
	}
	return ""
}

// NormalizeRelPath turns a user-supplied page argument into a contract path.
// It accepts "wiki/concepts/x.md", ".contexo/wiki/concepts/x.md" (relative to
// root), an absolute path under hubDir, and Windows backslashes, and rejects
// anything escaping the hub. The result passes ValidateRelPath; failures are
// *PathError.
func NormalizeRelPath(root, hubDir, arg string) (string, error) {
	rel, reason := relFromArg(root, hubDir, arg)
	if reason == "" {
		reason = relPathProblem(rel)
	}
	if reason != "" {
		return "", &PathError{Path: arg, Reason: reason}
	}
	return rel, nil
}

// relFromArg maps arg to a '/'-separated candidate path relative to the hub,
// or returns a reason when arg cannot name a path inside it. A relative arg
// is relative to the hub, or to root when it starts with the hub's own
// prefix (".contexo/"); it is never resolved against the working directory.
func relFromArg(root, hubDir, arg string) (string, string) {
	if arg == "" {
		return "", "empty path"
	}
	if filepath.IsAbs(arg) {
		rel, ok := relUnderHub(hubDir, arg)
		switch {
		case !ok:
			return "", "outside the hub " + hubDir
		case rel == ".":
			return "", "names the hub directory, not a page"
		}
		return rel, ""
	}
	s := strings.ReplaceAll(arg, `\`, "/")
	s = strings.TrimPrefix(s, "./")
	if p := hubPrefix(root, hubDir); p != "" {
		s = strings.TrimPrefix(s, p)
	}
	return s, ""
}

// hubPrefix returns the hub's '/'-terminated path relative to root
// (".contexo/" in a standard project), "" when the hub is root itself, and
// ".contexo/" when it cannot be computed.
func hubPrefix(root, hubDir string) string {
	if root != "" && hubDir != "" {
		if rel, err := filepath.Rel(root, hubDir); err == nil && !relEscapes(rel) {
			if rel == "." {
				return ""
			}
			return filepath.ToSlash(rel) + "/"
		}
	}
	return ".contexo/"
}

// relUnderHub returns the '/'-separated path of the absolute path abs
// relative to hubDir when abs lies inside it. A lexical miss is retried with
// symlinks resolved, so /var/x and /private/var/x (or a linked checkout)
// name the same hub; the result is still checked to stay inside the hub.
func relUnderHub(hubDir, abs string) (string, bool) {
	if a, err := filepath.Abs(hubDir); err == nil {
		hubDir = a
	}
	if rel, ok := relInside(hubDir, abs); ok {
		return rel, true
	}
	return relInside(resolveExisting(hubDir), resolveExisting(abs))
}

func relInside(base, target string) (string, bool) {
	rel, err := filepath.Rel(base, target)
	if err != nil || relEscapes(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// relEscapes reports whether a filepath.Rel result leaves its base.
func relEscapes(rel string) bool {
	return filepath.IsAbs(rel) || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExisting resolves symlinks in the longest existing prefix of the
// absolute path p and appends the missing rest unchanged, so a page that
// does not exist (any more) still resolves.
func resolveExisting(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for cur := p; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
