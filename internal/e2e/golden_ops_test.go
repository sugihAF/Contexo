package e2e

import (
	"fmt"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// gop is the helper prefix of this file: the push, pull, resolve, show and
// repair goldens (E31 names). Extension commands use the E25 argv.
//
// pull.case-collision.json is not produced: it needs two server paths that
// differ only by letter case, which the in-process gitstore (a git working
// tree in a temp dir) cannot hold on the case-insensitive file systems of
// the macOS and Windows CI runners, so the golden would differ per OS. The
// canonical fixture internal/syncops/contract/testdata/v1/pull.case-collision.json
// stays the reference (WP-2.2.md records the gap).

// gopUnknownSHA is a well-formed sha no repo contains.
const gopUnknownSHA = "0123456789abcdef0123456789abcdef01234567"

func goldenOpGroups() []goldenGroup {
	push := func(name string) goldenFile { return stdoutDoc(name, contract.SchemaPush) }
	pull := func(name string) goldenFile { return stdoutDoc(name, contract.SchemaPull) }
	resolve := func(name string) goldenFile { return stdoutDoc(name, contract.SchemaResolve) }
	return []goldenGroup{
		{
			// Two pages pushed: one create, one update.
			Files: []goldenFile{push("push.ok.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				p := conceptPage("retry-policy", "Retry three times.")
				a := g.user(srv, "alice", withPages(p))
				mustPush(g.t, a, "--yes")
				editPage(g.t, a, pageRel(p), "Cap the backoff at 30 s.")
				a.P.WritePage(entityPage("ledger-service", "Owns the ledger."))
				return runCtx(g.t, a, "push", "--json", "--yes")
			},
		},
		{
			// A dry run: pushed without sha, a blocking skip, nothing written
			// (E31h).
			Files: []goldenFile{push("push.dry-run.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				p := conceptPage("retry-policy", "Retry three times.")
				a := g.user(srv, "alice", withPages(p))
				mustPush(g.t, a, "--yes")
				editPage(g.t, a, pageRel(p), "Cap the backoff at 30 s.")
				a.P.WritePage(entityPage("ledger-service", "Owns the ledger."))
				writeBroken(g.t, a, brokenParse)
				return runCtx(g.t, a, "push", "--json", "--yes", "--dry-run")
			},
		},
		{
			// A named path that is unchanged: pushed [], skipped unchanged.
			Files: []goldenFile{push("push.nothing.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				p := conceptPage("retry-policy", "Retry three times.")
				a := g.user(srv, "alice", withPages(p))
				mustPush(g.t, a, "--yes")
				return runCtx(g.t, a, "push", "--json", "--yes", "--", pageRel(p))
			},
		},
		{
			// Exit 6: the teammate's push lands between alice's listing and her
			// send, so one page conflicts (409) and the other is committed.
			Files: []goldenFile{push("push.partial-conflict.json"), stderrEnv("push.partial-conflict.stderr.json")},
			Run:   gopPartialConflict,
		},
		{
			// --no-distill past a blocking capture: required and bypassed.
			Files: []goldenFile{push("push.distill-bypassed.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				c := conceptPage("settlement-windows", "T+1.")
				a := g.user(srv, "alice", withPages(c))
				mustPush(g.t, a, "--yes")
				editPage(g.t, a, pageRel(c), "T+2 on holidays.")
				seedCapture(g.t, a, gsSession, 14)
				return runCtx(g.t, a, "push", "--json", "--yes", "--no-distill")
			},
		},
		{
			// A create and an update written, nothing adopted, reindexed.
			Files: []goldenFile{pull("pull.ok.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				p := conceptPage("idempotency-keys", "Keys expire after 24h.")
				a := g.user(srv, "alice", withPages(p))
				mustPush(g.t, a, "--yes")
				b := g.user(srv, "bob")
				mustPull(g.t, b)
				editPage(g.t, a, pageRel(p), "Alice: keys expire after 48h.")
				a.P.WritePage(conceptPage("webhook-signing", "HMAC-SHA256."))
				mustPush(g.t, a, "--yes")
				return runCtx(g.t, b, "pull", "--json")
			},
		},
		{
			// One adoption plus the skip reasons conflict, deleted_local,
			// invalid, modified and unverified.
			Files: []goldenFile{pull("pull.skipped-and-adopted.json")},
			Run:   gopSkippedAndAdopted,
		},
		{
			// A second pull has nothing to do.
			Files: []goldenFile{pull("pull.up-to-date.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				a := g.user(srv, "alice", withPages(conceptPage("retry-policy", "Retry three times.")))
				mustPush(g.t, a, "--yes")
				b := g.user(srv, "bob")
				mustPull(g.t, b)
				return runCtx(g.t, b, "pull", "--json")
			},
		},
		{
			Files: []goldenFile{resolve("resolve.ours.json")},
			Run: func(g *goldenEnv) ctxResult {
				a, rel, sha := gopConflict(g)
				return runCtx(g.t, a, "resolve", rel, "--ours", "--json", "--if-server-sha", sha)
			},
		},
		{
			Files: []goldenFile{resolve("resolve.theirs.json")},
			Run: func(g *goldenEnv) ctxResult {
				a, rel, sha := gopConflict(g)
				return runCtx(g.t, a, "resolve", rel, "--theirs", "--json", "--if-server-sha", sha)
			},
		},
		{
			// The base snapshot of a conflicting page (alice's last sync).
			Files: []goldenFile{rawFile("show.base.raw.md")},
			Run: func(g *goldenEnv) ctxResult {
				a, rel, _ := gopConflict(g)
				return runCtx(g.t, a, "show", rel, "--at", "base")
			},
		},
		{
			// The teammate's server version of the same page, sanitized.
			Files: []goldenFile{rawFile("show.server.raw.md")},
			Run: func(g *goldenEnv) ctxResult {
				a, rel, _ := gopConflict(g)
				return runCtx(g.t, a, "show", rel, "--at", "server")
			},
		},
		{
			// ctx repair over the old gitstore bug (page_shas = repo HEAD) and a
			// lost base: one repaired, one backfilled (E18 shape).
			Files: []goldenFile{stdoutDoc("repair.json", contract.SchemaRepair)},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				x := conceptPage("ledger-reconciliation", "Daily.")
				y := entityPage("ledger-service", "Owns the ledger.")
				a := g.user(srv, "alice", withPages(x, y))
				doc := mustPush(g.t, a, "--yes")
				head := doc.NewHead
				if head == "" || head == gsPushedSHA(g.t, doc, pageRel(x)) {
					g.t.Fatalf("want a repo HEAD that is not %s's own commit: %+v", pageRel(x), doc)
				}
				gsDropBase(g.t, a, pageRel(x))
				setPageSHA(g.t, a, pageRel(x), head)
				gsDropBase(g.t, a, pageRel(y))
				return runCtx(g.t, a, "repair", "--json")
			},
		},
	}
}

// gopConflict pushes P as alice, lets bob push an edit and makes a local
// alice edit: P is a both_modified conflict. It returns alice, P and the
// server sha.
func gopConflict(g *goldenEnv) (*e2eUser, string, string) {
	srv := g.server()
	p := conceptPage("retry-policy", "Retry three times.")
	rel := pageRel(p)
	a := g.user(srv, "alice", withPages(p))
	mustPush(g.t, a, "--yes")
	b := g.user(srv, "bob")
	mustPull(g.t, b)
	editPage(g.t, b, rel, "Bob: retry five times.")
	mustPush(g.t, b, "--yes")
	editPage(g.t, a, rel, "Alice: retry twice.")
	st := mustStatus(g.t, a, false)
	e := mustPage(g.t, st, rel)
	if e.State != contract.StateConflict || e.Remote == nil {
		g.t.Fatalf("setup: %s is %s, want conflict", rel, e.State)
	}
	return a, rel, e.Remote.SHA
}

// gopPartialConflict builds push exit 6 through the after-listing race.
func gopPartialConflict(g *goldenEnv) ctxResult {
	bin := requireCtx(g.t)
	srv := g.server()
	p1 := conceptPage("settlement-batch", "Nightly batch.")
	p2 := entityPage("ledger-service", "Owns the ledger.")
	a := g.user(srv, "alice", withPages(p1, p2))
	mustPush(g.t, a, "--yes")
	b := g.user(srv, "bob")
	mustPull(g.t, b)
	editPage(g.t, b, pageRel(p1), "Bob: batch at 02:00.")
	editPage(g.t, a, pageRel(p1), "Alice: batch at 03:00.")
	editPage(g.t, a, pageRel(p2), "Alice: also owns refunds.")

	type hookOut struct {
		r   ctxResult
		err error
	}
	done := make(chan hookOut, 1)
	srv.Proxy.SetAfterList(func() {
		r, err := execCtx(bin, b, nil, "push", "--json", "--yes")
		done <- hookOut{r, err}
	})
	r := runCtx(g.t, a, "push", "--json", "--yes")
	select {
	case h := <-done:
		if h.err != nil || h.r.Exit != 0 {
			g.t.Fatalf("teammate push in the race hook: %v\n%s", h.err, h.r)
		}
	default:
		g.t.Fatalf("the race hook did not run (no listing reached the proxy)\n%s", r)
	}
	gopCheckPartialConflict(g.t, r, pageRel(p1), pageRel(p2))
	return r
}

// gopCheckPartialConflict pins the race outcome before the golden is
// written, so a timing change can never record a different document.
func gopCheckPartialConflict(t testing.TB, r ctxResult, conflicted, pushed string) {
	t.Helper()
	doc, env := assertPushConflict(t, r)
	if len(doc.Pushed) != 1 || doc.Pushed[0].Path != pushed || len(doc.Conflicts) != 1 ||
		doc.Conflicts[0].Path != conflicted || doc.Conflicts[0].Kind != contract.ConflictBothModified {
		t.Fatalf("race outcome: pushed %+v conflicts %+v", doc.Pushed, doc.Conflicts)
	}
	d, ok := env.Details.(*contract.ConflictDetails)
	if !ok || len(d.Paths) != 1 || d.Paths[0] != conflicted || env.HTTPStatus != 409 {
		t.Fatalf("conflict envelope: %+v details %+v", env, env.Details)
	}
}

// gopSkippedAndAdopted builds one pull with an adoption and the skip
// reasons conflict, deleted_local, invalid, modified and unverified.
func gopSkippedAndAdopted(g *goldenEnv) ctxResult {
	srv := g.server()
	ad := conceptPage("ledger-reconciliation", "Daily.")
	co := conceptPage("retry-policy", "Retry three times.")
	dl := entityPage("fraud-service", "Scores payments.")
	in := conceptPage("webhook-signing", "HMAC-SHA256.")
	mo := entityPage("ledger-service", "Owns the ledger.")
	un := conceptPage("settlement-windows", "T+1.")
	a := g.user(srv, "alice", withPages(ad, co, dl, in, mo, un))
	mustPush(g.t, a, "--yes")

	b := g.user(srv, "bob")
	mustPull(g.t, b)
	same := "Reconciled at 06:00 UTC."
	editPage(g.t, b, pageRel(ad), same)
	editPage(g.t, b, pageRel(co), "Bob: retry five times.")
	editPage(g.t, b, pageRel(dl), "Bob: scores refunds too.")
	editPage(g.t, b, pageRel(in), "Bob: rotate keys yearly.")
	mustPush(g.t, b, "--yes")

	editPage(g.t, a, pageRel(ad), same) // byte-equal to bob's: adopted
	editPage(g.t, a, pageRel(co), "Alice: retry twice.")
	a.P.Remove(pageRel(dl))
	a.P.WriteRaw(pageRel(in), []byte("---\nschema: ctx.page.v1\nslug: [unclosed\n---\n# Broken\n"))
	editPage(g.t, a, pageRel(mo), "Alice: also owns refunds.")
	// A stale base whose recorded sha no server has: EnsureBase cannot verify
	// it (sha_not_found) and the local edit keeps pull from adopting it.
	gsDropBase(g.t, a, pageRel(un))
	setPageSHA(g.t, a, pageRel(un), gopUnknownSHA)
	editPage(g.t, a, pageRel(un), "Alice: T+2 on holidays.")
	r := runCtx(g.t, a, "pull", "--json")
	var doc contract.PullDoc
	assertOK(g.t, r, &doc)
	got := map[string]string{}
	for _, s := range doc.Skipped {
		got[s.Path] = string(s.Reason)
	}
	want := map[string]string{
		pageRel(co): "conflict", pageRel(dl): "deleted_local", pageRel(in): "invalid",
		pageRel(mo): "modified", pageRel(un): "unverified",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) || len(doc.Adopted) != 1 || doc.Adopted[0].Path != pageRel(ad) {
		g.t.Fatalf("pull outcome: skipped %v (want %v), adopted %+v", got, want, doc.Adopted)
	}
	return r
}
