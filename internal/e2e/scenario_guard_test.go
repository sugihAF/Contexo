package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// gd is the helper prefix of this file (scenarios 04 and 05).

// gdDisableEnv is the E24 env bypass, passed to exactly one run each time.
const gdDisableEnv = "CONTEXO_DISTILL_DISABLE=1"

// gdPushedPaths returns the paths of doc.Pushed, in document order.
func gdPushedPaths(doc contract.PushDoc) []string {
	out := []string{}
	for _, p := range doc.Pushed {
		out = append(out, p.Path)
	}
	return out
}

// gdPOSTs counts every POST the proxy recorded.
func gdPOSTs(px *recordingProxy) int { return px.Count(http.MethodPost, "") }

// gdPushOK runs push with extraEnv and args and decodes the exit-0
// document.
func gdPushOK(t testing.TB, u *e2eUser, extraEnv []string, args ...string) contract.PushDoc {
	t.Helper()
	var doc contract.PushDoc
	assertOK(t, runCtxEnv(t, u, extraEnv, append([]string{"push", "--json"}, args...)...), &doc)
	return doc
}

// TestScenario04_CaptureGuard (D4): a fresh capture buffer plus a modified
// concept blocks the push (distill_required, nothing sent); a named entity
// still goes through; --no-distill and CONTEXO_DISTILL_DISABLE=1 bypass the
// guard and say so (E24).
func TestScenario04_CaptureGuard(t *testing.T) {
	srv := startServer(t)
	cspec := conceptPage("guarded-idea", "Concept v1.")
	espec := entityPage("guarded-tool", "Entity v1.")
	conceptRel, entityRel := pageRel(cspec), pageRel(espec)
	alice := newUser(t, srv, "alice", withPages(cspec, espec))

	// No capture yet: the first push is not guarded.
	first := mustPush(t, alice)
	if got := gdPushedPaths(first); len(got) != 2 || first.Distill != (contract.DistillCheck{}) {
		t.Fatalf("first push pushed %q distill %+v, want both pages and distill {false false}", got, first.Distill)
	}
	conceptSHA := serverSHA(t, srv, defaultRepo, conceptRel)

	const session = "e2e-guard-session"
	seedCapture(t, alice, session, 2)
	editPage(t, alice, conceptRel, "Concept edit 1.")
	editPage(t, alice, entityRel, "Entity edit 1.")

	st := mustStatus(t, alice, false)
	if !st.Captures.BlocksPush || st.Captures.Fresh != 1 || st.Captures.Pending != 1 {
		t.Fatalf("captures = %+v, want blocks_push true, fresh 1, pending 1", st.Captures)
	}
	if s := st.Captures.Sessions; len(s) != 1 || s[0].SessionID != session || s[0].Turns != 2 || !s[0].Fresh {
		t.Errorf("captures.sessions = %+v, want [{%s 2 turns fresh}]", s, session)
	}
	for _, rel := range []string{conceptRel, entityRel} {
		if e := mustPage(t, st, rel); e.State != contract.StateModified {
			t.Errorf("%s = %s, want modified", rel, e.State)
		}
	}

	// push --json refuses: exit 9, nothing sent, the server HEAD unchanged.
	head := serverHead(t, srv, defaultRepo)
	posts := gdPOSTs(srv.Proxy)
	env := assertFail(t, runCtx(t, alice, "push", "--json"), contract.CodeDistillRequired)
	d, ok := env.Details.(*contract.DistillRequiredDetails)
	if !ok {
		t.Fatalf("distill_required details = %T, want *DistillRequiredDetails\n%s", env.Details, env.Line)
	}
	if !equalStrings(d.Pages, []string{conceptRel}) {
		t.Errorf("details.pages = %q, want only the concept %q (an entity is not knowledge)", d.Pages, conceptRel)
	}
	if strings.TrimSpace(d.AgentPrompt) == "" || !strings.Contains(d.AgentPrompt, conceptRel) {
		t.Errorf("details.agent_prompt = %q, want a non-empty prompt naming %s", d.AgentPrompt, conceptRel)
	}
	if len(d.Sessions) != 1 || d.Sessions[0].SessionID != session || d.Sessions[0].Turns != 2 || !d.Sessions[0].Fresh {
		t.Errorf("details.sessions = %+v, want [{%s 2 turns fresh}]", d.Sessions, session)
	}
	if got := serverHead(t, srv, defaultRepo); got != head {
		t.Errorf("server HEAD moved %s -> %s on a refused push", head, got)
	}
	if got := gdPOSTs(srv.Proxy); got != posts {
		t.Errorf("refused push made %d POST(s), want 0", got-posts)
	}

	// A modified entity named explicitly is not guarded.
	doc := gdPushOK(t, alice, nil, "--yes", "--", entityRel)
	if got := gdPushedPaths(doc); !equalStrings(got, []string{entityRel}) || doc.Distill != (contract.DistillCheck{}) {
		t.Fatalf("named entity push pushed %q distill %+v, want [%s] and distill {false false}", got, doc.Distill, entityRel)
	}
	if len(doc.Skipped) != 0 {
		t.Errorf("named entity push skipped %+v, want none", doc.Skipped)
	}
	if got := serverSHA(t, srv, defaultRepo, conceptRel); got != conceptSHA {
		t.Errorf("the guarded concept reached the server: sha %s -> %s", conceptSHA, got)
	}

	// --no-distill pushes the concept and records the bypass.
	doc = gdPushOK(t, alice, nil, "--no-distill")
	if got := gdPushedPaths(doc); !equalStrings(got, []string{conceptRel}) {
		t.Errorf("--no-distill pushed %q, want [%s]", got, conceptRel)
	}
	if want := (contract.DistillCheck{Required: true, Bypassed: true}); doc.Distill != want {
		t.Errorf("--no-distill distill = %+v, want %+v", doc.Distill, want)
	}

	// E24: CONTEXO_DISTILL_DISABLE=1, set for single runs only.
	editPage(t, alice, conceptRel, "Concept edit 2.")
	if st := mustStatus(t, alice, false); !st.Captures.BlocksPush {
		t.Errorf("captures.blocks_push = false after a new concept edit, want true")
	}
	var stEnv contract.StatusDoc
	assertOK(t, runCtxEnv(t, alice, []string{gdDisableEnv}, "status", "--json"), &stEnv)
	if stEnv.Captures.BlocksPush {
		t.Errorf("with %s captures.blocks_push = true, want false (E24)", gdDisableEnv)
	}
	doc = gdPushOK(t, alice, []string{gdDisableEnv}, "--yes")
	if got := gdPushedPaths(doc); !equalStrings(got, []string{conceptRel}) {
		t.Errorf("env-bypass push pushed %q, want [%s]", got, conceptRel)
	}
	if want := (contract.DistillCheck{Required: true, Bypassed: true}); doc.Distill != want {
		t.Errorf("env-bypass distill = %+v, want %+v (E24)", doc.Distill, want)
	}
}

// TestScenario05_InvalidListedNotPushed: a parse-error file and a
// path_mismatch file are listed invalid with their stage, and neither a
// full push nor a push naming them sends them.
func TestScenario05_InvalidListedNotPushed(t *testing.T) {
	srv := startServer(t)
	good := conceptPage("valid-neighbour", "A valid page pushed beside the broken ones.")
	goodRel := pageRel(good)
	alice := newUser(t, srv, "alice", withPages(good))
	parseRel := writeBroken(t, alice, brokenParse)
	mismatchRel := writeBroken(t, alice, brokenPathMismatch)
	const relocated = "wiki/concepts/relocated.md"

	st := mustStatus(t, alice, false)
	p := mustPage(t, st, parseRel)
	if p.State != contract.StateInvalid || p.Invalid == nil || p.Invalid.Stage != contract.InvalidParse ||
		p.Invalid.Message == "" || p.Invalid.ExpectedPath != "" {
		t.Errorf("%s = %s invalid %+v, want invalid stage parse with a message", parseRel, p.State, p.Invalid)
	}
	m := mustPage(t, st, mismatchRel)
	if m.State != contract.StateInvalid || m.Invalid == nil || m.Invalid.Stage != contract.InvalidPathMismatch ||
		m.Invalid.Message == "" || m.Invalid.ExpectedPath != relocated {
		t.Errorf("%s = %s invalid %+v, want invalid stage path_mismatch expected_path %s", mismatchRel, m.State, m.Invalid, relocated)
	}
	for _, e := range []*contract.PageEntry{p, m} {
		if e.Local == nil || e.Local.File != e.Path || e.Local.Hash != "" || e.Base != nil || e.Remote != nil {
			t.Errorf("%s local %+v base %+v remote %+v, want local {file} without hash, no base, no remote",
				e.Path, e.Local, e.Base, e.Remote)
		}
	}
	if e := findPage(st, relocated); e != nil {
		t.Errorf("status lists %s (the mismatched page's frontmatter path): %+v", relocated, e)
	}
	if st.Counts.Invalid != 2 || st.Counts.New != 1 || st.Counts.Ahead != 1 {
		t.Errorf("counts = %+v, want invalid 2, new 1, ahead 1", st.Counts)
	}

	// A full push sends the valid page only and skips both broken files.
	doc := mustPush(t, alice)
	if got := gdPushedPaths(doc); !equalStrings(got, []string{goodRel}) {
		t.Errorf("push pushed %q, want [%s]", got, goodRel)
	}
	wantSkipped := []contract.SkippedPage{
		{Path: parseRel, State: contract.StateInvalid, Reason: contract.SkipInvalid},
		{Path: mismatchRel, State: contract.StateInvalid, Reason: contract.SkipInvalid},
	}
	if len(doc.Skipped) != len(wantSkipped) || doc.Skipped[0] != wantSkipped[0] || doc.Skipped[1] != wantSkipped[1] {
		t.Errorf("push skipped = %+v, want %+v", doc.Skipped, wantSkipped)
	}
	if got := srv.Proxy.PushedPaths(); !equalStrings(got, []string{goodRel}) {
		t.Errorf("proxy saw pushed paths %q, want only [%s]", got, goodRel)
	}

	// Named explicitly (E25 argv) they are still skipped, with no POST.
	posts := gdPOSTs(srv.Proxy)
	doc = mustPush(t, alice, "--yes", "--", parseRel, mismatchRel)
	if len(doc.Pushed) != 0 || len(doc.Skipped) != 2 || doc.Skipped[0] != wantSkipped[0] || doc.Skipped[1] != wantSkipped[1] {
		t.Errorf("named push pushed %+v skipped %+v, want nothing pushed and %+v", doc.Pushed, doc.Skipped, wantSkipped)
	}
	if got := gdPOSTs(srv.Proxy); got != posts {
		t.Errorf("named push of invalid pages made %d POST(s), want 0", got-posts)
	}
	for _, rel := range []string{parseRel, mismatchRel, relocated} {
		if sha := serverSHA(t, srv, defaultRepo, rel); sha != "" {
			t.Errorf("server has %s at %s; invalid files must never reach it", rel, sha)
		}
	}
	for _, rel := range srv.Proxy.PushedPaths() {
		if rel != goodRel {
			t.Errorf("proxy saw %s pushed", rel)
		}
	}
}
