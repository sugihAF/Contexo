package cli

// Two-user end-to-end tests for the safe `ctx pull --json` (WP-1.3): alice
// and bob are two projects on one testkit server and repo. Bob's edit
// reaches the server through the sync wire (pl13Push); the push CLI belongs
// to WP-1.2, and the wire keeps these tests about pull.

import (
	"bytes"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
)

// pl13Teammates is the shared two-user setup: P seeded at SHA1, alice and
// bob both pulled it, then bob's edit (BobBytes) was pushed at BobSHA.
type pl13Teammates struct {
	Srv        *testkit.Server
	Alice, Bob *testkit.Project
	P          string
	SHA1       string
	BobBytes   []byte
	BobSHA     string
}

func pl13NewTeammates(t *testing.T) *pl13Teammates {
	t.Helper()
	tm := &pl13Teammates{Srv: pl13Server(t), P: pl13Rel("shared")}
	tm.SHA1 = pl13Push(t, tm.Srv, tm.P, string(pl13Page("shared", "v1 from the seed")), "")

	tm.Alice = pl13Project(t, tm.Srv)
	tm.Bob = pl13Project(t, tm.Srv)
	for _, u := range []struct {
		name string
		p    *testkit.Project
	}{{"alice", tm.Alice}, {"bob", tm.Bob}} {
		doc := pl13PullJSON(t, u.p)
		pl13Equal(t, u.name+" initial written", doc.Written, []contract.PulledPage{
			{Path: tm.P, SHA: tm.SHA1, Kind: contract.WriteCreate},
		})
	}

	// Bob's edit goes to the server through the sync wire, with his base
	// sha as the parent (the push CLI is WP-1.2's).
	parent := pl13State(t, tm.Bob.HubDir).PageSHAs[tm.P]
	if parent != tm.SHA1 {
		t.Fatalf("bob page_shas[%s] = %q, want %q", tm.P, parent, tm.SHA1)
	}
	tm.BobBytes = pl13Page("shared", "v2 edited by bob")
	tm.BobSHA = pl13Push(t, tm.Srv, tm.P, string(tm.BobBytes), parent)
	return tm
}

func TestPullE2E_TeammateEditSkippedWhenLocalModified(t *testing.T) {
	tm := pl13NewTeammates(t)
	aliceBytes := pl13Page("shared", "alice edited locally")
	tm.Alice.WriteRaw(tm.P, aliceBytes)

	doc := pl13PullJSON(t, tm.Alice)
	pl13CheckHeader(t, doc, tm.Srv)
	pl13Equal(t, "skipped", doc.Skipped, []contract.SkippedPage{
		{Path: tm.P, State: contract.StateConflict, Reason: contract.SkipConflict, ServerSHA: tm.BobSHA},
	})
	pl13Equal(t, "written", doc.Written, []contract.PulledPage{})
	pl13Equal(t, "adopted", doc.Adopted, []contract.PulledPage{})

	if got := tm.Alice.Read(tm.P); !bytes.Equal(got, aliceBytes) {
		t.Errorf("alice's edit was overwritten:\n%s", got)
	}

	conflicts, err := syncstate.OpenConflicts(tm.Alice.HubDir)
	if err != nil {
		t.Fatalf("open alice's conflicts: %v", err)
	}
	rec, ok := conflicts.Get(tm.P)
	if !ok {
		t.Fatalf("alice's conflicts.json has no record for %s", tm.P)
	}
	if rec.ServerSHA != tm.BobSHA || rec.Source != syncstate.SourcePull ||
		rec.Kind != syncstate.KindBothModified || rec.BaseSHA != tm.SHA1 || !rec.HasContent {
		t.Errorf("conflict record = %+v, want server_sha %s, source pull, kind both_modified, base_sha %s, has_content",
			rec, tm.BobSHA, tm.SHA1)
	}
	side, err := conflicts.Content(tm.P)
	if err != nil {
		t.Fatalf("read alice's sidecar: %v", err)
	}
	if want := pl13Sanitized(tm.BobBytes); !bytes.Equal(side, want) {
		t.Errorf("sidecar content = %q, want bob's bytes %q", side, want)
	}

	if got := pl13State(t, tm.Alice.HubDir).PageSHAs[tm.P]; got != tm.SHA1 {
		t.Errorf("alice page_shas[%s] = %q, want it unchanged at %q", tm.P, got, tm.SHA1)
	}
}

func TestPullE2E_IncomingWrittenWhenClean(t *testing.T) {
	tm := pl13NewTeammates(t)

	doc := pl13PullJSON(t, tm.Alice)
	pl13CheckHeader(t, doc, tm.Srv)
	pl13Equal(t, "written", doc.Written, []contract.PulledPage{
		{Path: tm.P, SHA: tm.BobSHA, Kind: contract.WriteUpdate},
	})
	pl13Equal(t, "adopted", doc.Adopted, []contract.PulledPage{})
	pl13Equal(t, "skipped", doc.Skipped, []contract.SkippedPage{})

	if got, want := tm.Alice.Read(tm.P), pl13Sanitized(tm.BobBytes); !bytes.Equal(got, want) {
		t.Errorf("alice's file = %q, want bob's bytes %q", got, want)
	}
	if got := pl13State(t, tm.Alice.HubDir).PageSHAs[tm.P]; got != tm.BobSHA {
		t.Errorf("alice page_shas[%s] = %q, want bob's sha %q", tm.P, got, tm.BobSHA)
	}

	again := pl13PullJSON(t, tm.Alice)
	pl13Equal(t, "second pull written", again.Written, []contract.PulledPage{})
	pl13Equal(t, "second pull adopted", again.Adopted, []contract.PulledPage{})
	pl13Equal(t, "second pull skipped", again.Skipped, []contract.SkippedPage{})
}
