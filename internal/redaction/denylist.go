package redaction

import (
	"path/filepath"
	"strings"
)

// DefaultDenyPaths returns the default path patterns to deny.
func DefaultDenyPaths() []string {
	return []string{
		".env",
		".env.*",
		"*.pem",
		"*.key",
		"credentials.json",
		"secrets.yaml",
		"secrets.yml",
		"**/id_rsa",
		"**/id_ed25519",
	}
}

// MatchesDenyList checks if a path matches any deny pattern.
func MatchesDenyList(path string, denyPatterns []string) bool {
	base := filepath.Base(path)
	for _, pattern := range denyPatterns {
		// Check against full path
		if matched, _ := filepath.Match(pattern, path); matched {
			return true
		}
		// Check against base name
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
		// Handle ** prefix patterns
		if strings.HasPrefix(pattern, "**/") {
			subPattern := strings.TrimPrefix(pattern, "**/")
			if matched, _ := filepath.Match(subPattern, base); matched {
				return true
			}
		}
	}
	return false
}
