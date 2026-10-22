package e2e

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	gosync "sync"
	"testing"
)

// Known deviations: a product behavior that differs from the contract is
// never patched from here. The scenario calls knownDeviation(t, "DEV-n"),
// which skips (or fails with CONTEXO_E2E_STRICT=1), and the id has a row in
// the "Known deviations" table of internal/syncops/CONTRACT.md with its
// owner (WP-3.1 or WP-4.1). Each file registers its ids from an init().

// contractMD is CONTRACT.md relative to this package.
const contractMD = "../syncops/CONTRACT.md"

var (
	deviationsMu gosync.Mutex
	deviations   = map[string]string{} // id -> one-line summary
	reDevID      = regexp.MustCompile(`^DEV-[1-9][0-9]*$`)
)

// registerDeviation records id; it panics on a malformed or duplicate id.
func registerDeviation(id, summary string) {
	deviationsMu.Lock()
	defer deviationsMu.Unlock()
	if !reDevID.MatchString(id) {
		panic(fmt.Sprintf("e2e: deviation id %q is not DEV-<n>", id))
	}
	if _, dup := deviations[id]; dup {
		panic(fmt.Sprintf("e2e: deviation %s registered twice", id))
	}
	deviations[id] = summary
}

// knownDeviation skips the rest of t because of a registered deviation;
// with CONTEXO_E2E_STRICT=1 it fails instead.
func knownDeviation(t testing.TB, id string) {
	t.Helper()
	deviationsMu.Lock()
	summary, ok := deviations[id]
	deviationsMu.Unlock()
	if !ok {
		t.Fatalf("knownDeviation(%q): not registered", id)
	}
	if os.Getenv(envStrict) == "1" {
		t.Fatalf("known deviation %s (%s); %s=1 makes it fail", id, summary, envStrict)
	}
	t.Skipf("known deviation %s: %s (see CONTRACT.md Known deviations)", id, summary)
}

// TestDeviationsDocumented: every registered id has a row in CONTRACT.md's
// Known deviations table.
func TestDeviationsDocumented(t *testing.T) {
	b, err := os.ReadFile(contractMD)
	if err != nil {
		t.Fatalf("read %s: %v", contractMD, err)
	}
	rows := knownDeviationRows(string(b))
	if rows == nil {
		t.Fatalf("%s has no \"## Known deviations\" table", contractMD)
	}
	deviationsMu.Lock()
	ids := make([]string, 0, len(deviations))
	for id := range deviations {
		ids = append(ids, id)
	}
	deviationsMu.Unlock()
	sort.Strings(ids)
	for _, id := range ids {
		if !rows[id] {
			t.Errorf("deviation %s is registered but has no row in %s Known deviations", id, contractMD)
		}
	}
}

// knownDeviationRows returns the ids of the table rows under the
// "## Known deviations" heading (nil when the heading is missing).
func knownDeviationRows(md string) map[string]bool {
	_, sec, ok := strings.Cut(md, "\n## Known deviations")
	if !ok {
		return nil
	}
	if i := strings.Index(sec, "\n## "); i >= 0 {
		sec = sec[:i]
	}
	rows := map[string]bool{}
	for _, line := range strings.Split(sec, "\n") {
		cells := strings.Split(strings.TrimSpace(line), "|")
		if len(cells) > 2 {
			if id := strings.TrimSpace(cells[1]); reDevID.MatchString(id) {
				rows[id] = true
			}
		}
	}
	return rows
}

func TestDeviations_RowParser(t *testing.T) {
	md := "# x\n\n## Known deviations\n\n| id | CONTRACT text | actual | owner |\n|---|---|---|---|\n| DEV-1 | a | b | WP-4.1 |\n|DEV-12|a|b|WP-3.1|\n\n## Other\n| DEV-9 | x |\n"
	rows := knownDeviationRows(md)
	if !rows["DEV-1"] || !rows["DEV-12"] || rows["DEV-9"] || len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	if knownDeviationRows("# no table\n") != nil {
		t.Fatal("missing heading must give nil")
	}
}
