package e2e

import (
	"bytes"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/testkit"
)

// fm is the helper prefix of this file.
//
// Scenario 12 is the WP-1.6 request routed to WP-2.2 (WP-1.I.md, "Requests
// for other WPs", WP-2.2): contract §7(i) "lossy frontmatter not detected"
// no longer holds for unknown keys, so an unknown frontmatter key must
// survive push -> pull end to end.

// fmExtraKeys are unknown (non-schema) frontmatter keys, written in the
// sorted order Normalize emits them after the schema keys.
const fmExtraKeys = "aliases:\n  - retries\nreview_state: pending\n"

// fmPageWithExtras returns spec's page bytes with fmExtraKeys added at the
// end of the frontmatter.
func fmPageWithExtras(t *testing.T, spec testkit.PageSpec) []byte {
	t.Helper()
	b := testkit.PageBytes(spec)
	end := bytes.Index(b[3:], []byte("\n---\n"))
	if !bytes.HasPrefix(b, []byte("---\n")) || end < 0 {
		t.Fatalf("unexpected page layout:\n%s", b)
	}
	end += 3 + 1 // index of the closing "---"
	out := append([]byte(nil), b[:end]...)
	out = append(out, fmExtraKeys...)
	return append(out, b[end:]...)
}

func TestScenario12_UnknownFrontmatterKeySurvivesPushPull(t *testing.T) {
	requireCtx(t)
	srv := startServer(t)
	spec := conceptPage("retry-policy", "Retry three times.")
	rel := pageRel(spec)
	alice := newUser(t, srv, "alice")
	alice.P.WriteRaw(rel, fmPageWithExtras(t, spec))

	push := mustPush(t, alice, "--yes")
	if len(push.Pushed) != 1 || push.Pushed[0].Path != rel || push.Pushed[0].Kind != contract.WriteCreate {
		t.Fatalf("push: pushed %+v, want a create of %s", push.Pushed, rel)
	}
	server := serverContent(t, srv, defaultRepo, rel)
	if !bytes.Contains(server, []byte(fmExtraKeys)) {
		t.Fatalf("the server lost the unknown keys:\n%s", server)
	}
	st := mustStatus(t, alice, false)
	if e := mustPage(t, st, rel); e.State != contract.StateUnchanged || st.Counts.Ahead != 0 {
		t.Fatalf("after the push %s is %s (ahead %d), want unchanged", rel, e.State, st.Counts.Ahead)
	}

	bob := newUser(t, srv, "bob")
	pull := mustPull(t, bob)
	if len(pull.Written) != 1 || pull.Written[0].Path != rel {
		t.Fatalf("bob pull: written %+v, want %s", pull.Written, rel)
	}
	if got := bob.P.Read(rel); !bytes.Contains(got, []byte(fmExtraKeys)) {
		t.Fatalf("bob's pulled file lost the unknown keys:\n%s", got)
	}

	// A teammate edit keeps them too, and alice gets them back.
	editPage(t, bob, rel, "Bob: cap the backoff at 30 s.")
	mustPush(t, bob, "--yes")
	if got := mustPull(t, alice); len(got.Written) != 1 {
		t.Fatalf("alice pull: written %+v, want %s", got.Written, rel)
	}
	if got := alice.P.Read(rel); !bytes.Contains(got, []byte(fmExtraKeys)) || !bytes.Contains(got, []byte("Bob: cap the backoff")) {
		t.Fatalf("alice's file after the round trip:\n%s", got)
	}
	st = mustStatus(t, alice, false)
	if e := mustPage(t, st, rel); e.State != contract.StateUnchanged {
		t.Fatalf("after the round trip %s is %s, want unchanged", rel, e.State)
	}
}
