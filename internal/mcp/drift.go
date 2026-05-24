package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/diff"
	syncpkg "github.com/sugihAF/contexo/internal/sync"
)

// driftCacheTTL is the freshness window for a drift check on a single page.
// Within this window we reuse the prior result instead of round-tripping to
// the server again. Sixty seconds is long enough to avoid hammering when the
// agent reads a page repeatedly, short enough that a teammate's push lands
// before the next session's reads.
const driftCacheTTL = 60 * time.Second

type driftCacheEntry struct {
	checkedAt time.Time
	notice    string
}

// driftChecker layers Layer 3 (pre-edit drift awareness) onto the MCP
// resource read path. It compares the local last-pulled sha for a page
// against the server's current sha and, when they differ, returns a notice
// to prepend to the page bytes so the agent sees the divergence before
// editing.
//
// The check is best-effort. Any failure (no creds, no network, server 5xx)
// silently degrades to "no notice" — drift detection should never break the
// underlying page read.
type driftChecker struct {
	hubRoot string

	mu    sync.Mutex
	cache map[string]driftCacheEntry
}

func newDriftChecker(hubRoot string) *driftChecker {
	return &driftChecker{
		hubRoot: hubRoot,
		cache:   make(map[string]driftCacheEntry),
	}
}

// maybeNotice returns the drift notice for relPath, or "" if there is no
// drift / drift checking is disabled / the lookup failed.
func (c *driftChecker) maybeNotice(relPath string) string {
	if os.Getenv("CONTEXO_DRIFT_DISABLE") == "1" {
		return ""
	}
	c.mu.Lock()
	if entry, ok := c.cache[relPath]; ok && time.Since(entry.checkedAt) < driftCacheTTL {
		c.mu.Unlock()
		return entry.notice
	}
	c.mu.Unlock()

	notice, err := c.computeNotice(relPath)
	if err != nil {
		// Don't poison the cache on transient failures; let the next read retry.
		return ""
	}
	c.mu.Lock()
	c.cache[relPath] = driftCacheEntry{checkedAt: time.Now(), notice: notice}
	c.mu.Unlock()
	return notice
}

func (c *driftChecker) computeNotice(relPath string) (string, error) {
	projectRoot := projectRootFromHub(c.hubRoot)
	cfg, _ := config.Load(projectRoot)
	creds, _ := config.LoadCredentials(projectRoot)
	if creds == nil || cfg.ServerURL == "" || cfg.RepoID == "" {
		return "", nil // unconfigured; nothing to check against
	}
	state, err := syncpkg.LoadState(c.hubRoot)
	if err != nil {
		return "", err
	}
	localSHA := state.PageSHAs[relPath]
	if localSHA == "" {
		// Never pulled this page — no baseline to detect drift against.
		// (Locally-authored pages that have never been pushed live here.)
		return "", nil
	}
	client := syncpkg.NewClient(cfg.ServerURL, creds.Bearer())
	_, serverSHA, err := client.ReadPage(cfg.RepoID, relPath)
	if err != nil {
		return "", err
	}
	if serverSHA == "" || serverSHA == localSHA {
		return "", nil
	}
	d, err := client.PageDiff(cfg.RepoID, relPath, localSHA, serverSHA, false)
	if err != nil {
		return "", err
	}
	return renderDriftNotice(relPath, localSHA, serverSHA, d), nil
}

// renderDriftNotice formats the agent-facing block that gets prepended to
// the page bytes. Kept compact: one summary line per section change so the
// notice doesn't dominate the page itself.
func renderDriftNotice(relPath, localSHA, serverSHA string, d *diff.SectionDiff) string {
	var sb strings.Builder
	sb.WriteString("<DRIFT_NOTICE>\n")
	fmt.Fprintf(&sb, "This page changed on the server since your last pull.\n")
	fmt.Fprintf(&sb, "  your version:  %s\n", shortSHADrift(localSHA))
	fmt.Fprintf(&sb, "  server now:    %s\n", shortSHADrift(serverSHA))
	sb.WriteString("\nWhat changed:\n")
	wrote := false
	if hasFrontmatterDiff(d) {
		sb.WriteString("  ~ frontmatter changed\n")
		wrote = true
	}
	for _, s := range d.Sections {
		switch s.Status {
		case diff.StatusAdded:
			fmt.Fprintf(&sb, "  + %s\n", s.Heading)
			wrote = true
		case diff.StatusRemoved:
			fmt.Fprintf(&sb, "  - %s\n", s.Heading)
			wrote = true
		case diff.StatusModified:
			fmt.Fprintf(&sb, "  ~ %s\n", s.Heading)
			wrote = true
		case diff.StatusRenamed:
			fmt.Fprintf(&sb, "  ~> %s (was %q)\n", s.Heading, s.OldHeading)
			wrote = true
		}
	}
	if !wrote {
		sb.WriteString("  (changes outside structured sections)\n")
	}
	sb.WriteString("\nConsider `ctx pull` before editing this page. If you push without\n")
	sb.WriteString("pulling, the server will 409 unless your local parent_sha matches.\n")
	sb.WriteString("Call ctx_diff(slug=...) for the full per-section diff.\n")
	sb.WriteString("</DRIFT_NOTICE>")
	return sb.String()
}

func hasFrontmatterDiff(d *diff.SectionDiff) bool {
	return len(d.Frontmatter.Changed)+len(d.Frontmatter.Added)+len(d.Frontmatter.Removed) > 0
}

func shortSHADrift(s string) string {
	if len(s) <= 7 {
		return s
	}
	return s[:7]
}

// projectRootFromHub derives the project root from the .contexo/ hub root.
// The hub is always at <project>/.contexo so the project root is the parent.
// Mirrors what Server.rootDir does, exposed here so the drift checker
// doesn't need a Server reference.
func projectRootFromHub(hubRoot string) string {
	return filepath.Dir(hubRoot)
}
