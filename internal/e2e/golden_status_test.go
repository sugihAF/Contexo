package e2e

import (
	"testing"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// gs is the helper prefix of this file: the version and status goldens
// (E31 names).

// gsSession is the session id of every seeded capture buffer.
const gsSession = "5d0c6a3e-1f2b-4c7d-9e8f-0a1b2c3d4e5f"

// gsConfigURL is a config server_url that differs from the credentials'
// server (status.url-mismatch.json).
const gsConfigURL = "https://contexo-config.example.invalid"

func goldenStatusGroups() []goldenGroup {
	status := func(name string) goldenFile { return stdoutDoc(name, contract.SchemaStatus) }
	return []goldenGroup{
		{
			Files: []goldenFile{stdoutDoc("version.dev.json", contract.SchemaVersion)},
			Skip: func() string {
				if ctxPrebuilt {
					return "CONTEXO_E2E_CTX is a prebuilt (possibly stamped) binary; version.dev.json pins an unstamped build"
				}
				return ""
			},
			Run: func(g *goldenEnv) ctxResult {
				return runCtx(g.t, g.bareUser("nobody"), "version", "--json")
			},
		},
		{
			// A freshly initialized project against an empty repo.
			Files: []goldenFile{status("status.empty.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				if _, err := srv.TK.Store.Create(defaultRepo); err != nil {
					g.t.Fatal(err)
				}
				a := g.user(srv, "alice")
				return runCtx(g.t, a, "status", "--json")
			},
		},
		{
			// One online run: unchanged, modified, new, incoming, incoming_new,
			// conflict both_modified + both_added, deleted_local, invalid parse
			// + path_mismatch.
			Files: []goldenFile{status("status.all-states.json")},
			Run:   gsAllStates,
		},
		{
			// After a pull conflict skip: offline, the conflict comes from the
			// sidecar with remote {sha} only (E9, E31a).
			Files: []goldenFile{status("status.offline-sidecar.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				p := conceptPage("retry-policy", "Retry three times.")
				q := entityPage("ledger-service", "Owns the ledger.")
				a := g.user(srv, "alice", withPages(p, q))
				mustPush(g.t, a)
				b := g.user(srv, "bob")
				mustPull(g.t, b)
				editPage(g.t, b, pageRel(p), "Bob: retry five times.")
				mustPush(g.t, b)
				editPage(g.t, a, pageRel(p), "Alice: retry twice.")
				mustPull(g.t, a)
				return runCtx(g.t, a, "status", "--json", "--offline")
			},
		},
		{
			// Offline, no sidecars: new, modified, unchanged, invalid,
			// deleted_local and unverified (r12, R unknown) (E31c).
			Files: []goldenFile{status("status.offline-nocache.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				u := conceptPage("ledger-reconciliation", "Daily.")
				m := entityPage("ledger-service", "Owns the ledger.")
				d := entityPage("fraud-service", "Scores payments.")
				v := conceptPage("settlement-windows", "T+1.")
				a := g.user(srv, "alice", withPages(u, m, d, v))
				mustPush(g.t, a)
				gsDropBase(g.t, a, pageRel(v))
				editPage(g.t, a, pageRel(m), "Also owns refunds.")
				a.P.Remove(pageRel(d))
				a.P.WritePage(conceptPage("refund-retries", "New page."))
				writeBroken(g.t, a, brokenParse)
				return runCtx(g.t, a, "status", "--json", "--offline")
			},
		},
		{
			// No credentials: remote.error not_authenticated (E22, E31e).
			Files: []goldenFile{status("status.not-authenticated.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				a := g.user(srv, "alice", withNoCreds(), withPages(conceptPage("retry-policy", "Retry three times.")))
				return runCtx(g.t, a, "status", "--json")
			},
		},
		{
			// A revoked token: auth.state rejected, remote.error auth_rejected
			// 401, remote.source none (E31b).
			Files: []goldenFile{status("status.auth-rejected.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				a := g.user(srv, "alice", withKey(goldenWrongKey), withPages(conceptPage("retry-policy", "Retry three times.")))
				return runCtx(g.t, a, "status", "--json")
			},
		},
		{
			// The server is unreachable: remote.error network, exit 0 (E31b).
			Files: []goldenFile{status("status.network-error.json")},
			Run: func(g *goldenEnv) ctxResult {
				closed := freeClosedPort(g.t)
				g.url(closed)
				a := g.user(nil, "alice", withServerURL(closed), withKey("testkit-key"),
					withPages(conceptPage("retry-policy", "Retry three times.")))
				return runCtx(g.t, a, "status", "--json")
			},
		},
		{
			// A pre-v1 server: capabilities [] known, server_outdated, and a
			// stale base it cannot repair (no pages.at) left unverified with
			// base_repair_skipped.
			Files: []goldenFile{status("status.old-server.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.oldServer()
				u := conceptPage("ledger-reconciliation", "Daily.")
				v := conceptPage("settlement-windows", "T+1.")
				m := entityPage("ledger-service", "Owns the ledger.")
				a := g.user(srv, "alice", withPages(u, v, m))
				doc := mustPush(g.t, a)
				// page_shas[v] := another commit, base dropped: only ?at could
				// repair it.
				gsDropBase(g.t, a, pageRel(v))
				setPageSHA(g.t, a, pageRel(v), gsPushedSHA(g.t, doc, pageRel(u)))
				editPage(g.t, a, pageRel(m), "Also owns refunds.")
				return runCtx(g.t, a, "status", "--json")
			},
		},
		{
			// The repo does not exist yet: repo_exists false, every page new.
			Files: []goldenFile{status("status.repo-missing.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				a := g.user(srv, "alice", withPages(
					conceptPage("retry-policy", "Retry three times."),
					entityPage("ledger-service", "Owns the ledger.")))
				return runCtx(g.t, a, "status", "--json")
			},
		},
		{
			// config.server_url names another server than the credentials:
			// warning server_url_mismatch, the credentials' server is used.
			Files: []goldenFile{status("status.url-mismatch.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				a := g.user(srv, "alice", withPages(
					conceptPage("ledger-reconciliation", "Daily."),
					entityPage("ledger-service", "Owns the ledger.")))
				mustPush(g.t, a)
				cfg, err := config.Load(a.Root)
				if err != nil {
					g.t.Fatal(err)
				}
				cfg.ServerURL = gsConfigURL
				if err := config.Save(a.Root, cfg); err != nil {
					g.t.Fatal(err)
				}
				return runCtx(g.t, a, "status", "--json")
			},
		},
		{
			// A fresh capture buffer plus a modified concept: blocks_push.
			Files: []goldenFile{status("status.captures-blocking.json")},
			Run: func(g *goldenEnv) ctxResult {
				srv := g.server()
				c := conceptPage("settlement-windows", "T+1.")
				e := entityPage("ledger-service", "Owns the ledger.")
				a := g.user(srv, "alice", withPages(c, e))
				mustPush(g.t, a)
				editPage(g.t, a, pageRel(c), "T+2 on holidays.")
				seedCapture(g.t, a, gsSession, 14)
				return runCtx(g.t, a, "status", "--json")
			},
		},
	}
}

// gsAllStates builds every online state for alice, with bob as the teammate.
func gsAllStates(g *goldenEnv) ctxResult {
	srv := g.server()
	un := conceptPage("ledger-reconciliation", "Daily.")
	mo := entityPage("ledger-service", "Owns the ledger.")
	in := conceptPage("idempotency-keys", "Keys expire after 24h.")
	co := conceptPage("retry-policy", "Retry three times.")
	dl := entityPage("fraud-service", "Scores payments.")
	a := g.user(srv, "alice", withPages(un, mo, in, co, dl))
	mustPush(g.t, a)

	b := g.user(srv, "bob")
	mustPull(g.t, b)
	editPage(g.t, b, pageRel(in), "Bob: keys expire after 48h.")
	editPage(g.t, b, pageRel(co), "Bob: retry five times.")
	b.P.WritePage(conceptPage("webhook-signing", "Bob: HMAC-SHA256."))
	b.P.WritePage(entityPage("settlement-batch", "Bob: nightly batch."))
	mustPush(g.t, b)

	editPage(g.t, a, pageRel(mo), "Alice: also owns refunds.")
	editPage(g.t, a, pageRel(co), "Alice: retry twice.")
	a.P.Remove(pageRel(dl))
	a.P.WritePage(conceptPage("chargeback-flow", "Alice: new page."))
	a.P.WritePage(conceptPage("webhook-signing", "Alice: Ed25519."))
	writeBroken(g.t, a, brokenParse)
	writeBroken(g.t, a, brokenPathMismatch)
	return runCtx(g.t, a, "status", "--json")
}

// gsDropBase removes rel's base record and file, as a lost bases.json entry.
func gsDropBase(t testing.TB, u *e2eUser, rel string) {
	t.Helper()
	b, err := syncstate.OpenBases(u.Hub)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(rel); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
}

// gsPushedSHA returns the sha a push document reports for rel.
func gsPushedSHA(t testing.TB, doc contract.PushDoc, rel string) string {
	t.Helper()
	for _, p := range doc.Pushed {
		if p.Path == rel && p.SHA != "" {
			return p.SHA
		}
	}
	t.Fatalf("push did not report a sha for %s: %+v", rel, doc.Pushed)
	return ""
}
