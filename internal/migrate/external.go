package migrate

import (
	"crypto/sha1"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Cloner fetches a git URL into dest. Seam for testing.
type Cloner func(url, dest string) error

// IsGitURL reports whether from looks like a git remote (vs a local path).
func IsGitURL(s string) bool {
	return strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "git@") ||
		strings.HasSuffix(s, ".git")
}

// DefaultCloner shells out to git with an argument slice (never a shell string).
func DefaultCloner(url, dest string) error {
	cmd := exec.Command("git", "clone", "--depth", "1", "--", url, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ResolveExternal turns a --from value into a local directory to scan. For a
// URL it clones into .contexo/.migrate-cache/<hash> and returns that as both
// scanDir and cacheDir (so the caller records it for later cleanup).
func ResolveExternal(from, contexoDir string, clone Cloner) (scanDir, cacheDir string, err error) {
	if from == "" {
		return "", "", fmt.Errorf("migrate: empty --from")
	}
	if IsGitURL(from) {
		if clone == nil {
			clone = DefaultCloner
		}
		sum := sha1.Sum([]byte(from))
		dest := filepath.Join(contexoDir, ".migrate-cache", fmt.Sprintf("%x", sum)[:12])
		_ = os.RemoveAll(dest) // fresh clone
		if err := clone(from, dest); err != nil {
			return "", "", err
		}
		return dest, dest, nil
	}
	info, err := os.Stat(from)
	if err != nil {
		return "", "", fmt.Errorf("migrate: --from path %q: %w", from, err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("migrate: --from %q is not a directory", from)
	}
	return from, "", nil
}
