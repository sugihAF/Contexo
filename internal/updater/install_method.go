package updater

import (
	"os"
	"strings"
)

// InstallMethod is how the running ctx binary was put on disk. Only
// InstallManaged binaries (downloaded directly or via the install script) are
// safe to self-replace; package-manager installs should be updated through
// their manager so its bookkeeping stays consistent.
type InstallMethod int

const (
	InstallManaged InstallMethod = iota
	InstallHomebrew
	InstallScoop
)

// CurrentInstallMethod inspects the running executable's path. Errors resolving
// the path fall back to InstallManaged (assume self-update is fine).
func CurrentInstallMethod() InstallMethod {
	exe, err := os.Executable()
	if err != nil {
		return InstallManaged
	}
	return detectInstallMethod(exe)
}

// detectInstallMethod classifies an executable path by well-known
// package-manager directory markers. Pure for testability.
func detectInstallMethod(exePath string) InstallMethod {
	p := strings.ToLower(strings.ReplaceAll(exePath, `\`, "/"))
	switch {
	case strings.Contains(p, "/cellar/"),
		strings.Contains(p, "/homebrew/"),
		strings.Contains(p, "/linuxbrew/"):
		return InstallHomebrew
	case strings.Contains(p, "/scoop/"):
		return InstallScoop
	default:
		return InstallManaged
	}
}
