package redaction

import "regexp"

// Pattern defines a named regex pattern for secret detection.
type Pattern struct {
	Name    string
	Regex   *regexp.Regexp
	Replace string
}

// DefaultPatterns returns the built-in secret detection patterns.
func DefaultPatterns() []Pattern {
	return []Pattern{
		{
			Name:    "aws_access_key",
			Regex:   regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
			Replace: "[REDACTED:aws_key]",
		},
		{
			Name:    "aws_secret_key",
			Regex:   regexp.MustCompile(`(?i)(?:aws_secret_access_key|secret_key|secret)\s*[=:]\s*[A-Za-z0-9/+=]{40}`),
			Replace: "[REDACTED:aws_secret]",
		},
		{
			Name:    "generic_api_token",
			Regex:   regexp.MustCompile(`(?i)(?:api[_-]?key|api[_-]?token|access[_-]?token|auth[_-]?token)\s*[=:]\s*["']?[A-Za-z0-9_\-\.]{20,}["']?`),
			Replace: "[REDACTED:api_token]",
		},
		{
			Name:    "bearer_token",
			Regex:   regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9_\-\.]{20,}`),
			Replace: "[REDACTED:bearer_token]",
		},
		{
			Name:    "private_key",
			Regex:   regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA )?PRIVATE KEY-----`),
			Replace: "[REDACTED:private_key]",
		},
		{
			Name:    "github_token",
			Regex:   regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{36,}`),
			Replace: "[REDACTED:github_token]",
		},
	}
}
