package e2e

import (
	"net/http"
	"strings"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// ge is the helper prefix of this file: one error.<code>.json envelope per
// code reached by scenarios 07-09, i.e. every code except internal and
// not_implemented (22 files). Each group runs one failing command with
// --json (show is always machine mode, E21).

// geQuotaBody is the 402 body of handler.writeQuotaError
// (internal/server/handler/handler.go:51-63).
const geQuotaBody = `{"code":"quota_repos","error":"Your plan allows 1 repository. Upgrade to create more.","limit":1,"upgrade_url":"https://contexo.live/pricing"}`

func goldenErrorGroups() []goldenGroup {
	return []goldenGroup{
		geGroup(contract.CodeUsage, func(g *goldenEnv) ctxResult {
			a, rel := geProject(g)
			return runCtx(g.t, a, "resolve", rel, "--ours", "--theirs", "--json")
		}),
		geGroup(contract.CodeInvalidPath, func(g *goldenEnv) ctxResult {
			a, _ := geProject(g)
			return runCtx(g.t, a, "show", "../x.md", "--at", "base")
		}),
		geGroup(contract.CodeNotAuthenticated, func(g *goldenEnv) ctxResult {
			srv := g.server()
			a := g.user(srv, "alice", withNoCreds())
			return runCtx(g.t, a, "pull", "--json")
		}),
		geGroup(contract.CodeAuthRejected, func(g *goldenEnv) ctxResult {
			srv := g.server()
			a := g.user(srv, "alice", withKey(goldenWrongKey), withPages(conceptPage("retry-policy", "Retry three times.")))
			return runCtx(g.t, a, "push", "--json", "--yes")
		}),
		geGroup(contract.CodeForbidden, func(g *goldenEnv) ctxResult {
			return geSynthetic(g, http.StatusForbidden, `{"error":"not a member of this repo"}`)
		}),
		geGroup(contract.CodeNotInitialized, func(g *goldenEnv) ctxResult {
			return runCtx(g.t, g.bareUser("alice"), "status", "--json")
		}),
		geGroup(contract.CodeRepoNotConfigured, func(g *goldenEnv) ctxResult {
			srv := g.server()
			a := g.user(srv, "alice", withRepo(""))
			return runCtx(g.t, a, "pull", "--json")
		}),
		geGroup(contract.CodeServerUnresolved, func(g *goldenEnv) ctxResult {
			// Global credentials without server_url: a global token is never
			// sent to the workspace's server URL.
			srv := g.server()
			a := g.user(srv, "alice", withNoCreds())
			writeGlobalCreds(g.t, a, config.Credentials{Token: srv.Key})
			return runCtx(g.t, a, "pull", "--json")
		}),
		{
			// The stderr half of push exit 6 (the document is
			// push.partial-conflict.json).
			Files: []goldenFile{stderrEnv("error.conflict.json")},
			Run:   gopPartialConflict,
		},
		geGroup(contract.CodeStale, func(g *goldenEnv) ctxResult {
			a, rel, _ := gopConflict(g)
			return runCtx(g.t, a, "resolve", rel, "--ours", "--json", "--if-server-sha", gopUnknownSHA)
		}),
		geGroup(contract.CodeNetwork, func(g *goldenEnv) ctxResult {
			closed := freeClosedPort(g.t)
			g.url(closed)
			a := g.user(nil, "alice", withServerURL(closed), withKey("testkit-key"))
			return runCtx(g.t, a, "pull", "--json")
		}),
		geGroup(contract.CodeServerError, func(g *goldenEnv) ctxResult {
			return geSynthetic(g, http.StatusInternalServerError, `{"error":"internal server error"}`)
		}),
		geGroup(contract.CodeRateLimited, func(g *goldenEnv) ctxResult {
			return geSynthetic(g, http.StatusTooManyRequests, `{"error":"rate limit exceeded"}`)
		}),
		geGroup(contract.CodeRepoNotFound, func(g *goldenEnv) ctxResult {
			// pull's listing names the code in its message; show and resolve
			// raise the same code with the legacy "sync: page not found" text.
			srv := g.server()
			a := g.user(srv, "alice", withRepo("missing-repo"))
			return runCtx(g.t, a, "pull", "--json")
		}),
		geGroup(contract.CodePageNotFound, func(g *goldenEnv) ctxResult {
			a, _ := geProject(g)
			return runCtx(g.t, a, "show", "wiki/concepts/never-pushed.md", "--at", "server")
		}),
		geGroup(contract.CodeBaseUnavailable, func(g *goldenEnv) ctxResult {
			a, _ := geProject(g)
			a.P.WritePage(conceptPage("chargeback-flow", "Not pushed yet."))
			return runCtx(g.t, a, "show", "wiki/concepts/chargeback-flow.md", "--at", "base")
		}),
		geGroup(contract.CodeSHANotFound, func(g *goldenEnv) ctxResult {
			a, rel := geProject(g)
			return runCtx(g.t, a, "show", rel, "--at", gopUnknownSHA)
		}),
		geGroup(contract.CodeDistillRequired, func(g *goldenEnv) ctxResult {
			a, rel := geProject(g)
			editPage(g.t, a, rel, "Cap the backoff at 30 s.")
			seedCapture(g.t, a, gsSession, 14)
			return runCtx(g.t, a, "push", "--json", "--yes")
		}),
		geGroup(contract.CodeNotResolvable, func(g *goldenEnv) ctxResult {
			srv := g.server()
			p := conceptPage("idempotency-keys", "Keys expire after 24h.")
			a := g.user(srv, "alice", withPages(p))
			mustPush(g.t, a, "--yes")
			b := g.user(srv, "bob")
			mustPull(g.t, b)
			editPage(g.t, b, pageRel(p), "Bob: keys expire after 48h.")
			mustPush(g.t, b, "--yes")
			return runCtx(g.t, a, "resolve", pageRel(p), "--ours", "--json")
		}),
		geGroup(contract.CodeServerUnsupported, func(g *goldenEnv) ctxResult {
			srv := g.oldServer()
			p := conceptPage("retry-policy", "Retry three times.")
			a := g.user(srv, "alice", withPages(p))
			doc := mustPush(g.t, a, "--yes")
			return runCtx(g.t, a, "show", pageRel(p), "--at", gsPushedSHA(g.t, doc, pageRel(p)))
		}),
		geGroup(contract.CodeQuotaExceeded, func(g *goldenEnv) ctxResult {
			// The first push auto-creates the repo; the plan cap answers 402.
			up := startUpstream(g.t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sync/push") {
					w.WriteHeader(http.StatusPaymentRequired)
					_, _ = w.Write([]byte(geQuotaBody))
					return
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"repo not found"}`))
			})
			g.url(up)
			a := g.user(nil, "alice", withServerURL(up), withKey("testkit-key"),
				withPages(conceptPage("retry-policy", "Retry three times.")))
			return runCtx(g.t, a, "push", "--json", "--yes")
		}),
		geGroup(contract.CodeBusy, func(g *goldenEnv) ctxResult {
			a, rel := geProject(g)
			editPage(g.t, a, rel, "Cap the backoff at 30 s.")
			holdLock(g.t, a, "e2e")
			return runCtxEnv(g.t, a, []string{"CONTEXO_LOCK_TIMEOUT=500ms"}, "push", "--json", "--yes")
		}),
	}
}

// geGroup is the group of error.<code>.json.
func geGroup(code contract.ErrorCode, run func(g *goldenEnv) ctxResult) goldenGroup {
	return goldenGroup{Files: []goldenFile{stderrEnv("error." + string(code) + ".json")}, Run: run}
}

// geProject is alice with one pushed concept page.
func geProject(g *goldenEnv) (*e2eUser, string) {
	srv := g.server()
	p := conceptPage("retry-policy", "Retry three times.")
	a := g.user(srv, "alice", withPages(p))
	mustPush(g.t, a, "--yes")
	return a, pageRel(p)
}

// geSynthetic runs pull --json against an upstream that answers every
// request with status and body.
func geSynthetic(g *goldenEnv, status int, body string) ctxResult {
	up := startUpstream(g.t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	g.url(up)
	a := g.user(nil, "alice", withServerURL(up), withKey("testkit-key"))
	return runCtx(g.t, a, "pull", "--json")
}
