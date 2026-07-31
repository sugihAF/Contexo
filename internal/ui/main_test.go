package ui

import (
	"os"
	"testing"
)

// termenv honours CLICOLOR_FORCE even on a non-TTY, so a developer or CI
// machine with it set would flip every renderer in this package to colour and
// produce environment-dependent failures. Pin the environment.
func TestMain(m *testing.M) {
	os.Unsetenv("CLICOLOR_FORCE")
	os.Unsetenv("NO_COLOR")
	os.Unsetenv("CTX_THEME")
	os.Unsetenv("CTX_ASCII")
	os.Setenv("TERM", "dumb")
	os.Exit(m.Run())
}
