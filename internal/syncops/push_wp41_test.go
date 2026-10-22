package syncops

// WP-4.1 regression tests for the push engine: GFR-1 (E10 as extended by
// lead ruling L28: a conflict found from the live listing writes a push
// sidecar exactly like a 409).

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// wp41Theirs is a teammate's version with ESC, bidi and zero-width
// characters, so a sidecar that skips SanitizeContent shows up.
var wp41Theirs = []byte("---\nslug: x\n---\n# X\n\nTeammate \x1b[31medit\x1b[0m ‮hidden​.\n")

// wp41OfflineEntry is rel's entry in an offline status of fx.
func wp41OfflineEntry(t *testing.T, fx *p12Fix, rel string) contract.PageEntry {
	t.Helper()
	doc, err := Status(context.Background(), fx.env, StatusOptions{Offline: true})
	if err != nil {
		t.Fatalf("offline status: %v", err)
	}
	for _, e := range doc.Pages {
		if e.Path == rel {
			return e
		}
	}
	t.Fatalf("offline status has no entry for %s: %+v", rel, doc.Pages)
	return contract.PageEntry{}
}

// wp41WantSidecar checks rel's push sidecar: server sha, base sha, kind,
// source push, detected_at TimeOf(p12Now) and SanitizeContent(theirs).
func wp41WantSidecar(t *testing.T, fx *p12Fix, rel, server, base, kind string, theirs []byte) {
	t.Helper()
	rec, ok := fx.conflicts().Get(rel)
	if !ok {
		t.Fatalf("conflicts.json has no record for %s (E10/L28: a listing conflict writes a push sidecar)", rel)
	}
	want := syncstate.ConflictRecord{Path: rel, ServerSHA: server, BaseSHA: base, Kind: kind,
		Source: syncstate.SourcePush, DetectedAt: contract.TimeOf(p12Now), HasContent: true}
	if !reflect.DeepEqual(rec, want) {
		t.Fatalf("sidecar = %+v\nwant      %+v", rec, want)
	}
	content, err := fx.conflicts().Content(rel)
	if err != nil {
		t.Fatal(err)
	}
	if want := schema.SanitizeContent(string(theirs)); string(content) != want {
		t.Fatalf("sidecar content = %q, want SanitizeContent(server) %q", content, want)
	}
}

func TestWP41_GFR1_ListingConflictWritesSidecar(t *testing.T) {
	for _, withPushed := range []bool{false, true} {
		t.Run(fmt.Sprintf("withPushed=%v", withPushed), func(t *testing.T) {
			fx := p12Env(t)
			relX, dataX := fx.page(schema.TypeConcept, "x", "# X\n")
			sb := fx.synced(relX, dataX)
			relY, dataY := fx.page(schema.TypeEntity, "y", "# Y\n")
			fx.synced(relY, dataY)
			// The teammate's commit is already in our listing: no 409.
			srv := fx.fake.Seed(relX, wp41Theirs)
			fx.edit(relX, schema.TypeConcept, "# X\n\nOur edit.\n")
			if withPushed {
				fx.edit(relY, schema.TypeEntity, "# Y\n\nOur edit.\n")
			}
			baseX := fx.baseBytes(relX)

			doc, err := fx.push(PushOptions{})
			if err != nil {
				t.Fatalf("Push: %v (a listing conflict is a skip, exit 0)", err)
			}
			p12AssertDoc(t, doc)
			p12AssertSkipped(t, doc.Skipped, []contract.SkippedPage{
				{Path: relX, State: contract.StateConflict, Reason: contract.SkipConflict, ServerSHA: srv},
			})
			if len(doc.Conflicts) != 0 {
				t.Fatalf("conflicts = %+v, want none (no 409)", doc.Conflicts)
			}
			if n := len(doc.Pushed); (n == 1) != withPushed {
				t.Fatalf("pushed = %+v", doc.Pushed)
			}
			wp41WantSidecar(t, fx, relX, srv, sb, syncstate.KindBothModified, wp41Theirs)
			if got := fx.pageSHAs()[relX]; got != sb {
				t.Fatalf("page_shas[%s] = %q, want unchanged %q", relX, got, sb)
			}
			if got := fx.baseBytes(relX); string(got) != string(baseX) {
				t.Fatalf("base of %s changed: %q", relX, got)
			}
			if rec, _ := fx.baseRecord(relX); rec.SHA != sb {
				t.Fatalf("bases.json[%s].sha = %q, want %q", relX, rec.SHA, sb)
			}

			e := wp41OfflineEntry(t, fx, relX)
			if e.State != contract.StateConflict || e.ConflictKind != contract.ConflictBothModified {
				t.Fatalf("offline %s = %s/%s, want conflict/both_modified (E9)", relX, e.State, e.ConflictKind)
			}
			if e.Remote == nil || *e.Remote != (contract.RemoteSide{SHA: srv}) {
				t.Fatalf("offline %s remote = %+v, want {sha: %s}", relX, e.Remote, srv)
			}
		})
	}
}

func TestWP41_GFR1_ListingConflictBothAddedSidecar(t *testing.T) {
	fx := p12Env(t)
	relX, _ := fx.page(schema.TypeConcept, "x", "# X\n\nOurs.\n")
	srv := fx.fake.Seed(relX, wp41Theirs)

	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	p12AssertSkipped(t, doc.Skipped, []contract.SkippedPage{
		{Path: relX, State: contract.StateConflict, Reason: contract.SkipConflict, ServerSHA: srv},
	})
	wp41WantSidecar(t, fx, relX, srv, "", syncstate.KindBothAdded, wp41Theirs)
	if got, ok := fx.pageSHAs()[relX]; ok {
		t.Fatalf("page_shas[%s] = %q, want no entry", relX, got)
	}
	e := wp41OfflineEntry(t, fx, relX)
	if e.State != contract.StateConflict || e.ConflictKind != contract.ConflictBothAdded {
		t.Fatalf("offline %s = %s/%s, want conflict/both_added", relX, e.State, e.ConflictKind)
	}
}

// A dry run, a declined Confirm and a failed send write no sidecar (E8).
func TestWP41_GFR1_ListingConflictNoWriteWithoutSend(t *testing.T) {
	cases := []struct {
		name  string
		opts  PushOptions
		setup func(fx *p12Fix)
	}{
		{name: "dry-run", opts: PushOptions{DryRun: true}},
		{name: "declined", opts: PushOptions{Confirm: func([]PushPlanFile) (bool, error) { return false, nil }}},
		{name: "send-failed", setup: func(fx *p12Fix) {
			fx.fake.FailNext("PushPages", &sync.NetworkError{Op: "push", Err: fmt.Errorf("connection reset")})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := p12Env(t)
			relX, dataX := fx.page(schema.TypeConcept, "x", "# X\n")
			fx.synced(relX, dataX)
			relY, dataY := fx.page(schema.TypeEntity, "y", "# Y\n")
			fx.synced(relY, dataY)
			fx.fake.Seed(relX, wp41Theirs)
			fx.edit(relX, schema.TypeConcept, "# X\n\nOur edit.\n")
			fx.edit(relY, schema.TypeEntity, "# Y\n\nOur edit.\n")
			if tc.setup != nil {
				tc.setup(fx)
			}
			before := p12Tree(t, fx.hub)
			_, _ = fx.push(tc.opts)
			if after := p12Tree(t, fx.hub); !reflect.DeepEqual(before, after) {
				t.Fatalf("%s wrote files:\nbefore %v\nafter  %v", tc.name, before, after)
			}
			if n := fx.requests("ReadPage") + fx.requests("PullPages"); n != 0 {
				t.Fatalf("%s fetched server content: %v", tc.name, fx.fake.Requests())
			}
		})
	}
}

// A second push over the same conflict keeps the sidecar as it is (no
// fetch, detected_at unchanged); a newer server sha replaces it.
func TestWP41_GFR1_ListingConflictSidecarKeptOrRefreshed(t *testing.T) {
	fx := p12Env(t)
	relX, dataX := fx.page(schema.TypeConcept, "x", "# X\n")
	sb := fx.synced(relX, dataX)
	srv := fx.fake.Seed(relX, wp41Theirs)
	fx.edit(relX, schema.TypeConcept, "# X\n\nOur edit.\n")
	if _, err := fx.push(PushOptions{}); err != nil {
		t.Fatal(err)
	}
	wp41WantSidecar(t, fx, relX, srv, sb, syncstate.KindBothModified, wp41Theirs)
	reads := fx.requests("ReadPage") + fx.requests("PullPages")
	before := p12Tree(t, fx.hub)

	if _, err := fx.push(PushOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := fx.requests("ReadPage") + fx.requests("PullPages"); n != reads {
		t.Fatalf("a second push over a recorded conflict fetched again: %v", fx.fake.Requests())
	}
	if after := p12Tree(t, fx.hub); !reflect.DeepEqual(before, after) {
		t.Fatalf("a second push rewrote files:\nbefore %v\nafter  %v", before, after)
	}

	theirs2 := []byte("---\nslug: x\n---\n# X\n\nTeammate again.\n")
	srv2 := fx.fake.Seed(relX, theirs2)
	doc, err := fx.push(PushOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p12AssertSkipped(t, doc.Skipped, []contract.SkippedPage{
		{Path: relX, State: contract.StateConflict, Reason: contract.SkipConflict, ServerSHA: srv2},
	})
	wp41WantSidecar(t, fx, relX, srv2, sb, syncstate.KindBothModified, theirs2)
}

// More than 8 listing conflicts cost one PullPages("") instead of a
// ReadPage each (E12 budget, as pull).
func TestWP41_GFR1_ListingConflictFetchBudget(t *testing.T) {
	fx := p12Env(t)
	var rels []string
	srvs := map[string]string{}
	for i := 0; i < 9; i++ {
		rel, data := fx.page(schema.TypeConcept, fmt.Sprintf("c%d", i), "# C\n")
		fx.synced(rel, data)
		srvs[rel] = fx.fake.Seed(rel, []byte(fmt.Sprintf("theirs %d\n", i)))
		fx.edit(rel, schema.TypeConcept, "# C\n\nOurs.\n")
		rels = append(rels, rel)
	}
	if _, err := fx.push(PushOptions{}); err != nil {
		t.Fatal(err)
	}
	if r, p := fx.requests("ReadPage"), fx.requests("PullPages"); r != 0 || p != 1 {
		t.Fatalf("ReadPage %d, PullPages %d; want 0 and 1", r, p)
	}
	c := fx.conflicts()
	for i, rel := range rels {
		rec, ok := c.Get(rel)
		if !ok || rec.ServerSHA != srvs[rel] || !rec.HasContent {
			t.Fatalf("sidecar of %s = %+v (ok %v), want server %s with content", rel, rec, ok, srvs[rel])
		}
		if got, _ := c.Content(rel); string(got) != fmt.Sprintf("theirs %d\n", i) {
			t.Fatalf("sidecar content of %s = %q", rel, got)
		}
	}
}

// When the server content cannot be fetched, the sidecar still records the
// listing sha (without content) and the push still succeeds.
func TestWP41_GFR1_ListingConflictFetchFailureKeepsSHA(t *testing.T) {
	fx := p12Env(t)
	relX, dataX := fx.page(schema.TypeConcept, "x", "# X\n")
	sb := fx.synced(relX, dataX)
	srv := fx.fake.Seed(relX, wp41Theirs)
	fx.edit(relX, schema.TypeConcept, "# X\n\nOur edit.\n")
	fx.fake.FailNext("ReadPage", &sync.NetworkError{Op: "read", Err: fmt.Errorf("connection reset")})

	if _, err := fx.push(PushOptions{}); err != nil {
		t.Fatalf("Push: %v", err)
	}
	rec, ok := fx.conflicts().Get(relX)
	want := syncstate.ConflictRecord{Path: relX, ServerSHA: srv, BaseSHA: sb, Kind: syncstate.KindBothModified,
		Source: syncstate.SourcePush, DetectedAt: contract.TimeOf(p12Now)}
	if !ok || !reflect.DeepEqual(rec, want) {
		t.Fatalf("sidecar = %+v (ok %v), want %+v", rec, ok, want)
	}
	if e := wp41OfflineEntry(t, fx, relX); e.State != contract.StateConflict {
		t.Fatalf("offline %s = %s, want conflict", relX, e.State)
	}
}
