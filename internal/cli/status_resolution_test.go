package cli

import (
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
)

// wp03SeedState records a pushed sha for page foo, so `ctx status` has a
// synced page to check against the server's listing.
func wp03SeedState(t *testing.T, root string) {
	t.Helper()
	state := &sync.State{PageSHAs: map[string]string{
		"wiki/concepts/foo.md": "0123456789abcdef0123456789abcdef01234567",
	}}
	if err := sync.SaveState(config.ContexoDirPath(root), state); err != nil {
		t.Fatal(err)
	}
}

func TestStatusUsesResolvedServer(t *testing.T) {
	tr := wp03TrapServers(t)
	root := wp03Project(t, tr)
	wp03SeedState(t, root)

	stdout, _, _ := wp03Run(t, root, "status")
	tr.AssertOnlyA(t, "status listing", wp03Token)
	if !strings.Contains(stdout, tr.A.URL) {
		t.Errorf("status does not show the resolved server %s:\n%s", tr.A.URL, stdout)
	}
}

func TestStatusShowsMismatchWarning(t *testing.T) {
	tr := wp03TrapServers(t)
	root := wp03Project(t, tr)

	stdout, _, _ := wp03Run(t, root, "status")
	if !strings.Contains(stdout, tr.B.URL) {
		t.Errorf("status does not name the config server_url %s:\n%s", tr.B.URL, stdout)
	}
	advice := "ctx auth login --server " + tr.B.URL
	if n := strings.Count(stdout, advice); n != 1 {
		t.Errorf("status prints %q %d times, want once:\n%s", advice, n, stdout)
	}
	wp03AssertSafeLoginHint(t, "status", stdout)
}

func TestStatusGlobalCredsWithoutServerSendsNothing(t *testing.T) {
	tr := wp03TrapServers(t)
	root := wp03UnresolvedProject(t, tr)
	wp03SeedState(t, root)

	stdout, _, err := wp03Run(t, root, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(stdout, "unresolved") {
		t.Errorf("status does not say the server is unresolved:\n%s", stdout)
	}
	wp03AssertNothingSent(t, tr, "status")
}
