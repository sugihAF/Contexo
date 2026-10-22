package e2e

import (
	"runtime"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// sv is the helper prefix of this file (scenarios 09 and 10).

// svWantCapabilities is the E19 order: the 11 v1 capabilities, then repair.
var svWantCapabilities = []contract.Capability{
	"status.json", "status.offline", "show.base", "show.server", "show.sha",
	"pull.json", "push.json", "push.paths", "push.distill_guard", "resolve",
	"errors.envelope", "repair",
}

// TestScenario10_VersionJSON: the handshake with no project and an empty
// HOME. With CONTEXO_E2E_CTX (a stamped binary) only the strict decode and
// the capability check run.
func TestScenario10_VersionJSON(t *testing.T) {
	u := newBareUser(t, "nobody")
	r := runCtx(t, u, "version", "--json")
	var doc contract.VersionDoc
	assertOK(t, r, &doc)
	if doc.Schema != contract.SchemaVersion || doc.ContractVersion != 1 {
		t.Errorf("schema %q contract_version %d, want %s 1", doc.Schema, doc.ContractVersion, contract.SchemaVersion)
	}
	got := make([]contract.Capability, len(doc.Capabilities))
	copy(got, doc.Capabilities)
	if len(got) != len(svWantCapabilities) {
		t.Fatalf("capabilities = %q, want exactly %q (E19 order)", got, svWantCapabilities)
	}
	for i := range got {
		if got[i] != svWantCapabilities[i] {
			t.Fatalf("capabilities = %q, want exactly %q (E19 order)", got, svWantCapabilities)
		}
	}
	if ctxPrebuilt {
		return
	}
	if doc.Version != "dev" || doc.Commit != "none" || doc.Date != "unknown" || !doc.DevBuild {
		t.Errorf("unstamped build: version %q commit %q date %q dev_build %v, want dev none unknown true",
			doc.Version, doc.Commit, doc.Date, doc.DevBuild)
	}
	if doc.OS != runtime.GOOS || doc.Arch != runtime.GOARCH {
		t.Errorf("os/arch = %s/%s, want %s/%s", doc.OS, doc.Arch, runtime.GOOS, runtime.GOARCH)
	}

	// --short keeps its exact one-line output.
	s := runCtx(t, u, "version", "--short")
	if s.Exit != 0 || string(s.Stdout) != "dev\n" || len(s.Stderr) != 0 {
		t.Errorf("version --short: want exit 0, stdout \"dev\\n\", empty stderr\n%s", s)
	}
}
