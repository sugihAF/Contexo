// Package version holds the build-time identity of the ctx binary.
//
// The vars below default to a "dev" identity and are overridden at release
// build time via -ldflags, e.g.:
//
//	go build -ldflags "\
//	  -X github.com/sugihAF/contexo/internal/version.Version=1.3.0 \
//	  -X github.com/sugihAF/contexo/internal/version.Commit=abc1234 \
//	  -X github.com/sugihAF/contexo/internal/version.Date=2026-06-02"
package version

import "fmt"

var (
	// Version is the semantic version without a leading "v" (e.g. "1.3.0"),
	// or "dev" for an un-stamped build (`go build`, `go install`).
	Version = "dev"
	// Commit is the short git SHA the binary was built from.
	Commit = "none"
	// Date is the build date (RFC3339 or YYYY-MM-DD).
	Date = "unknown"
)

// IsDevBuild reports whether this binary was built without release stamping.
// Self-update and the update nudge both refuse to act on dev builds.
func IsDevBuild() bool { return Version == "dev" }

// Full returns a human-readable identity line, e.g.
// "ctx 1.3.0 (abc1234, 2026-06-02)".
func Full() string {
	return fmt.Sprintf("ctx %s (%s, %s)", Version, Commit, Date)
}
