package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sugihAF/contexo/internal/updater"
	"github.com/sugihAF/contexo/internal/version"
)

const updateCheckTTL = 24 * time.Hour

type updateCache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

// suppressedCommands never emit the nudge: mcp/capture produce machine-read
// output (a stray line corrupts JSON-RPC or the capture buffer), and
// version/update would be redundant or recursive.
var suppressedCommands = map[string]bool{
	"mcp":     true,
	"capture": true,
	"version": true,
	"update":  true,
}

// shouldRunUpdateCheck decides whether the post-command nudge should run. Pure
// (env injected) for testability.
func shouldRunUpdateCheck(cmdName string, isTTY, isDev bool, env func(string) string) bool {
	if isDev || !isTTY {
		return false
	}
	if env("CONTEXO_NO_UPDATE_CHECK") != "" || env("CI") != "" {
		return false
	}
	return !suppressedCommands[cmdName]
}

func cacheFresh(c updateCache, now time.Time, ttl time.Duration) bool {
	return !c.CheckedAt.IsZero() && now.Sub(c.CheckedAt) < ttl
}

// rootChildName returns the name of the top-level command (direct child of the
// root) that was executed, so `ctx capture turn` resolves to "capture".
func rootChildName(cmd *cobra.Command) string {
	for cmd.HasParent() && cmd.Parent().HasParent() {
		cmd = cmd.Parent()
	}
	return cmd.Name()
}

// maybeNudge runs after a successful command and prints a one-line "update
// available" notice to stderr at most once per 24h. Best-effort: any error
// (network, cache I/O, even a panic) is swallowed so it can never affect the
// command the user actually ran.
func maybeNudge(cmd *cobra.Command) {
	defer func() { _ = recover() }()

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	if !shouldRunUpdateCheck(rootChildName(cmd), isTTY, version.IsDevBuild(), os.Getenv) {
		return
	}

	now := time.Now()
	cache, _ := loadUpdateCache()

	latest := cache.LatestVersion
	if !cacheFresh(cache, now, updateCheckTTL) {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		rel, err := updater.LatestRelease(ctx)
		if err != nil {
			return
		}
		latest = rel.Version()
		_ = saveUpdateCache(updateCache{CheckedAt: now, LatestVersion: latest})
	}

	if latest != "" && updater.IsNewer(latest, version.Version) {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"\n\033[2m✨ ctx %s available (you have %s) — run `ctx update`\033[0m\n",
			latest, version.Version)
	}
}

func updateCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "contexo", "update-check.json"), nil
}

func loadUpdateCache() (updateCache, error) {
	var c updateCache
	path, err := updateCachePath()
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(data, &c)
	return c, err
}

func saveUpdateCache(c updateCache) error {
	path, err := updateCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
