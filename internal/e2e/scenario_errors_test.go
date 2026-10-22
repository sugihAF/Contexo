package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// er is the helper prefix of this file (scenarios 07, 08 and 11).

// erUnknownSHA is a well-formed 40-hex sha no test repo contains.
const erUnknownSHA = "0123456789abcdef0123456789abcdef01234567"

// erSyntheticKey is the token of users wired to a synthetic upstream.
const erSyntheticKey = "e2e-synthetic-key"

// TestScenario07_ErrorEnvelopesPart1: configuration, auth, network, usage
// and path failures, one subtest per code. Every run passes --json except
// show (E21).
func TestScenario07_ErrorEnvelopesPart1(t *testing.T) {
	requireCtx(t)
	srv := startServer(t)
	spec := conceptPage("alpha", "Alpha body.")
	rel := pageRel(spec)

	t.Run("not_initialized", func(t *testing.T) {
		u := newBareUser(t, "bare")
		r := runCtx(t, u, "status", "--json")
		e := assertFail(t, r, contract.CodeNotInitialized)
		erLog(t, r, e)
		erWantAdvice(t, e, "ctx init")
		erWantNoDetails(t, e)
		if _, err := os.Stat(u.Hub); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("status created %s (stat err %v)", u.Hub, err)
		}
	})

	t.Run("not_authenticated", func(t *testing.T) {
		u := newUser(t, srv, "nocreds", withNoCreds(), withPages(spec))
		srv.Proxy.Reset()
		r := runCtx(t, u, "push", "--json", "--yes")
		e := assertFail(t, r, contract.CodeNotAuthenticated)
		erLog(t, r, e)
		erWantAdvice(t, e, "ctx auth login")
		erWantNoDetails(t, e)
		erWantNoRequests(t, srv)
	})

	t.Run("auth_rejected", func(t *testing.T) {
		u := newUser(t, srv, "wrongkey", withKey("e2e-wrong-key"), withPages(spec))
		srv.Proxy.Reset()
		r := runCtx(t, u, "push", "--json", "--yes")
		e := assertFail(t, r, contract.CodeAuthRejected)
		erLog(t, r, e)
		erWantHTTP(t, e, http.StatusUnauthorized)
		erWantNoDetails(t, e)
		if n := srv.Proxy.Count(http.MethodPost, "/sync/push"); n != 0 {
			t.Errorf("a rejected push still sent %d push request(s)", n)
		}
		// The repo was never created (ListPages fails on a missing repo).
		if pages, err := srv.TK.Store.ListPages(defaultRepo); err == nil && len(pages) != 0 {
			t.Errorf("server has pages %+v after a rejected push", pages)
		}

		// status reports the rejection inside the document and exits 0.
		doc := mustStatus(t, u, false)
		if doc.Auth.State != contract.AuthRejected {
			t.Errorf("auth.state = %q, want %q", doc.Auth.State, contract.AuthRejected)
		}
		if doc.Remote.Source != contract.RemoteNone {
			t.Errorf("remote.source = %q, want %q", doc.Remote.Source, contract.RemoteNone)
		}
		re := doc.Remote.Error
		if re == nil {
			t.Fatalf("remote.error missing: %+v", doc.Remote)
		}
		if re.Code != contract.CodeAuthRejected || re.HTTPStatus != http.StatusUnauthorized || re.Retryable {
			t.Errorf("remote.error = %+v, want auth_rejected http_status 401 retryable false", *re)
		}
		if !equalStrings(re.Advice, []string{"ctx auth login"}) {
			t.Errorf("remote.error.advice = %q, want [\"ctx auth login\"] (WP-1.1 R4)", re.Advice)
		}
		t.Logf("status remote.error: code %s message %q advice %q http_status %d",
			re.Code, re.Message, re.Advice, re.HTTPStatus)
	})

	t.Run("server_unresolved", func(t *testing.T) {
		u := newUser(t, srv, "globalnourl", withNoCreds(), withPages(spec))
		writeGlobalCreds(t, u, config.Credentials{Token: srv.Key})
		srv.Proxy.Reset()
		r := runCtx(t, u, "pull", "--json")
		e := assertFail(t, r, contract.CodeServerUnresolved)
		erLog(t, r, e)
		erWantAdvice(t, e, "ctx auth login --server <url>")
		erWantNoDetails(t, e)
		erWantNoRequests(t, srv)
	})

	t.Run("repo_not_configured", func(t *testing.T) {
		u := newUser(t, srv, "norepo", withRepo(""), withPages(spec))
		srv.Proxy.Reset()
		r := runCtx(t, u, "pull", "--json")
		e := assertFail(t, r, contract.CodeRepoNotConfigured)
		erLog(t, r, e)
		erWantAdvice(t, e, "ctx remote set-repo <id>")
		erWantNoDetails(t, e)
		erWantNoRequests(t, srv)
	})

	t.Run("network", func(t *testing.T) {
		u := newUser(t, nil, "offline", withServerURL(freeClosedPort(t)), withKey(erSyntheticKey), withPages(spec))
		r := runCtx(t, u, "pull", "--json")
		e := assertFail(t, r, contract.CodeNetwork)
		erLog(t, r, e)
		erWantHTTP(t, e, 0)
		erWantNoDetails(t, e)
		if !e.Retryable {
			t.Errorf("network must be retryable")
		}
	})

	t.Run("usage", func(t *testing.T) {
		u := newUser(t, srv, "usage", withPages(spec))
		srv.Proxy.Reset()

		r := runCtx(t, u, "resolve", rel, "--ours", "--theirs", "--json")
		e := assertFail(t, r, contract.CodeUsage)
		erLog(t, r, e)
		erWantNoDetails(t, e)

		r = runCtx(t, u, "status", "--json", "--no-such-flag")
		e = assertFail(t, r, contract.CodeUsage)
		erLog(t, r, e)
		erWantNoDetails(t, e)

		erWantNoRequests(t, srv)
	})

	t.Run("invalid_path", func(t *testing.T) {
		u := newUser(t, srv, "paths", withPages(spec))
		srv.Proxy.Reset()
		for _, p := range []string{"../x.md", ".sync/state.json"} {
			r := runCtx(t, u, "show", p, "--at", "base")
			e := assertFail(t, r, contract.CodeInvalidPath)
			erLog(t, r, e)
			erWantAdvice(t, e)
			erWantNoDetails(t, e)
		}
		erWantNoRequests(t, srv)
	})
}

// TestScenario07_ErrorEnvelopesPart2: failures that need server state. The
// shared setup leaves alice with P in conflict (both_modified) and Q
// incoming.
func TestScenario07_ErrorEnvelopesPart2(t *testing.T) {
	requireCtx(t)
	srv := startServer(t)
	pSpec := conceptPage("contested", "Contested body.")
	qSpec := entityPage("teammate-edited", "Teammate body.")
	p, q := pageRel(pSpec), pageRel(qSpec)

	alice := newUser(t, srv, "alice", withPages(pSpec, qSpec))
	mustPush(t, alice, "--yes")
	baseP, baseQ := serverSHA(t, srv, defaultRepo, p), serverSHA(t, srv, defaultRepo, q)
	bob := newUser(t, srv, "bob")
	mustPull(t, bob)
	editPage(t, bob, p, "Bob's edit of P.")
	editPage(t, bob, q, "Bob's edit of Q.")
	mustPush(t, bob, "--yes")
	bobP := serverSHA(t, srv, defaultRepo, p)
	if bobP == baseP || serverSHA(t, srv, defaultRepo, q) == baseQ {
		t.Fatalf("bob's push did not move P and Q")
	}
	editPage(t, alice, p, "Alice's edit of P.")

	st := mustStatus(t, alice, false)
	if e := mustPage(t, st, p); e.State != contract.StateConflict || e.ConflictKind != contract.ConflictBothModified {
		t.Fatalf("precondition: %s is %s/%s, want conflict/both_modified", p, e.State, e.ConflictKind)
	}
	if e := mustPage(t, st, q); e.State != contract.StateIncoming {
		t.Fatalf("precondition: %s is %s, want incoming", q, e.State)
	}

	t.Run("stale", func(t *testing.T) {
		before := erSyncSnapshot(t, alice)
		r := runCtx(t, alice, "resolve", p, "--ours", "--json", "--if-server-sha", baseP)
		e := assertFail(t, r, contract.CodeStale)
		erLog(t, r, e)
		d, ok := e.Details.(*contract.StaleDetails)
		if !ok {
			t.Fatalf("stale details = %#v, want *contract.StaleDetails", e.Details)
		}
		want := contract.StaleDetails{Path: p, ExpectedSHA: baseP, ServerSHA: bobP}
		if *d != want {
			t.Errorf("stale details = %+v, want %+v", *d, want)
		}
		erWantHTTP(t, e, 0)
		erWantSyncUnchanged(t, alice, before)
	})

	t.Run("not_resolvable", func(t *testing.T) {
		before := erSyncSnapshot(t, alice)
		r := runCtx(t, alice, "resolve", q, "--ours", "--json")
		e := assertFail(t, r, contract.CodeNotResolvable)
		erLog(t, r, e)
		d, ok := e.Details.(*contract.NotResolvableDetails)
		if !ok {
			t.Fatalf("not_resolvable details = %#v", e.Details)
		}
		want := contract.NotResolvableDetails{Path: q, State: contract.StateIncoming, Strategy: contract.ResolveOurs}
		if *d != want {
			t.Errorf("not_resolvable details = %+v, want %+v", *d, want)
		}
		erWantSyncUnchanged(t, alice, before)
	})

	t.Run("page_not_found", func(t *testing.T) {
		const absent = "wiki/concepts/absent-on-server.md"
		before := erSyncSnapshot(t, alice)
		r := runCtx(t, alice, "resolve", absent, "--theirs", "--json")
		e := assertFail(t, r, contract.CodePageNotFound)
		erLog(t, r, e)
		erWantHTTP(t, e, http.StatusNotFound)
		erWantNoDetails(t, e)
		if b := readHubFile(t, alice, absent); b != nil {
			t.Errorf("resolve --theirs created %s", absent)
		}
		erWantSyncUnchanged(t, alice, before)
	})

	t.Run("base_unavailable", func(t *testing.T) {
		n := alice.P.WritePage(conceptPage("local-only", "Never pushed."))
		srv.Proxy.Reset()
		r := runCtx(t, alice, "show", n, "--at", "base")
		e := assertFail(t, r, contract.CodeBaseUnavailable)
		erLog(t, r, e)
		erWantAdvice(t, e, "ctx repair")
		erWantNoDetails(t, e)
		erWantNoRequests(t, srv)
	})

	t.Run("sha_not_found", func(t *testing.T) {
		r := runCtx(t, alice, "show", p, "--at", erUnknownSHA)
		e := assertFail(t, r, contract.CodeSHANotFound)
		erLog(t, r, e)
		erWantHTTP(t, e, http.StatusNotFound)
		erWantNoDetails(t, e)
	})

	t.Run("repo_not_found", func(t *testing.T) {
		u := newUser(t, srv, "dora", withRepo("e2e-missing"), withPages(pSpec))
		r := runCtx(t, u, "pull", "--json")
		e := assertFail(t, r, contract.CodeRepoNotFound)
		erLog(t, r, e)
		erWantHTTP(t, e, http.StatusNotFound)
		erWantNoDetails(t, e)

		// The contract's raisers, show and resolve. E14 order: a ?at read of
		// a missing repo is repo_not_found, never sha_not_found.
		for _, args := range [][]string{
			{"show", p, "--at", erUnknownSHA},
			{"show", p, "--at", "server"},
			{"resolve", p, "--theirs", "--json"},
		} {
			r = runCtx(t, u, args...)
			e = assertFail(t, r, contract.CodeRepoNotFound)
			erLog(t, r, e)
			erWantHTTP(t, e, http.StatusNotFound)
			erWantNoDetails(t, e)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		erPushRace(t, srv)
	})

	// Synthetic upstreams. A hosted server answers a listing of a repo the
	// user cannot see with 403 and of a missing repo with 404; push treats
	// both as an empty listing (WP-1.2 R1), so forbidden and quota_exceeded
	// come from the push request itself (the 402 is the first push
	// auto-creating the repo). rate_limited and server_error come from the
	// listing.
	const (
		listing = "GET /v1/repos/e2e/pages"
		send    = "POST /v1/repos/e2e/sync/push"
	)
	quotaBody := `{"error":"Free tier is limited to 3 repos.","code":"quota_repos","limit":3,"upgrade_url":"https://contexo.live/#pricing"}`
	upstreams := []struct {
		code   contract.ErrorCode
		status int
		up     erUpstreamSpec
		reqs   []string
	}{
		{contract.CodeForbidden, http.StatusForbidden, erUpstreamSpec{
			listStatus: http.StatusForbidden, listBody: `{"error":"not a member of this repo"}`,
			pushStatus: http.StatusForbidden, pushBody: `{"error":"not a member of this repo"}`,
		}, []string{listing, send}},
		{contract.CodeQuotaExceeded, http.StatusPaymentRequired, erUpstreamSpec{
			listStatus: http.StatusNotFound, listBody: `{"error":"repo not found","code":"repo_not_found"}`,
			pushStatus: http.StatusPaymentRequired, pushBody: quotaBody,
		}, []string{listing, send}},
		{contract.CodeRateLimited, http.StatusTooManyRequests, erUpstreamSpec{
			listStatus: http.StatusTooManyRequests, listBody: `{"error":"rate limit exceeded"}`,
		}, []string{listing}},
		{contract.CodeServerError, http.StatusInternalServerError, erUpstreamSpec{
			listStatus: http.StatusInternalServerError, listBody: `{"error":"internal server error"}`,
		}, []string{listing}},
	}
	for _, c := range upstreams {
		t.Run(string(c.code), func(t *testing.T) {
			up := erStartUpstream(t, c.up)
			u := newUser(t, nil, "synthetic", withServerURL(up.url), withKey(erSyntheticKey), withPages(pSpec))
			r := runCtx(t, u, "push", "--json", "--yes")
			e := assertFail(t, r, c.code)
			erLog(t, r, e)
			erWantHTTP(t, e, c.status)
			if got := up.requests(); !equalStrings(got, c.reqs) {
				t.Errorf("upstream saw %q, want %q", got, c.reqs)
			}
			if c.code == contract.CodeQuotaExceeded {
				d, ok := e.Details.(*contract.QuotaDetails)
				if !ok {
					t.Fatalf("quota details = %#v", e.Details)
				}
				want := contract.QuotaDetails{QuotaCode: "quota_repos", Limit: 3, UpgradeURL: "https://contexo.live/#pricing"}
				if *d != want {
					t.Errorf("quota details = %+v, want %+v", *d, want)
				}
			} else {
				erWantNoDetails(t, e)
			}
			if sha := loadState(t, u).PageSHAs[p]; sha != "" {
				t.Errorf("a failed push recorded page_shas[%s] = %s", p, sha)
			}
			if len(readBases(t, u)) != 0 {
				t.Errorf("a failed push wrote bases: %v", readBases(t, u))
			}
		})
	}
}

// erPushRace is the 409 path (WP-1.2 R16): dave's push lands between
// carol's listing and her send, so the server refuses carol's stale parent.
func erPushRace(t *testing.T, srv *e2eServer) {
	t.Helper()
	const repo = "e2e-race"
	spec := conceptPage("raced", "Raced body.")
	rel := pageRel(spec)
	carol := newUser(t, srv, "carol", withRepo(repo), withPages(spec))
	mustPush(t, carol, "--yes")
	base := serverSHA(t, srv, repo, rel)
	dave := newUser(t, srv, "dave", withRepo(repo))
	mustPull(t, dave)
	editPage(t, dave, rel, "Dave's racing edit.")
	editPage(t, carol, rel, "Carol's racing edit.")
	baseFile, err := os.ReadFile(syncstate.BaseFile(carol.Hub, rel))
	if err != nil {
		t.Fatal(err)
	}

	type hookResult struct {
		r   ctxResult
		err error
	}
	hook := make(chan hookResult, 1)
	bin := requireCtx(t)
	srv.Proxy.Reset()
	srv.Proxy.SetAfterList(func() {
		r, err := execCtx(bin, dave, nil, "push", "--json", "--yes")
		hook <- hookResult{r, err}
	})
	r := runCtx(t, carol, "push", "--json", "--yes")
	var h hookResult
	select {
	case h = <-hook:
	case <-time.After(runTimeout):
		t.Fatal("the after-list hook never ran")
	}
	if h.err != nil || h.r.Exit != 0 {
		t.Fatalf("dave's racing push failed: %v\n%s", h.err, h.r)
	}
	daveSHA := serverSHA(t, srv, repo, rel)
	if daveSHA == base {
		t.Fatalf("dave's push did not move %s", rel)
	}

	doc, e := assertPushConflict(t, r)
	t.Logf("argv %q exit %d\nstdout:\n%sstderr: %s", r.Args, r.Exit, r.Stdout, e.Line)
	wantConflict := contract.PushConflict{Path: rel, Kind: contract.ConflictBothModified, BaseSHA: base, ServerSHA: daveSHA}
	if len(doc.Conflicts) != 1 || doc.Conflicts[0] != wantConflict {
		t.Errorf("conflicts = %+v, want [%+v]", doc.Conflicts, wantConflict)
	}
	if len(doc.Pushed) != 0 {
		t.Errorf("pushed = %+v, want []", doc.Pushed)
	}
	if doc.Schema != contract.SchemaPush || doc.DryRun || doc.RepoID != repo {
		t.Errorf("doc header = %q dry_run %v repo %q", doc.Schema, doc.DryRun, doc.RepoID)
	}
	d, ok := e.Details.(*contract.ConflictDetails)
	if !ok || !equalStrings(d.Paths, []string{rel}) {
		t.Errorf("conflict details = %#v, want paths [%s]", e.Details, rel)
	}
	erWantHTTP(t, e, http.StatusConflict)
	erWantAdvice(t, e, "ctx resolve "+rel+" --ours", "ctx resolve "+rel+" --theirs")

	// The server saw carol's push with the stale parent and answered 409.
	var last *proxyRecord
	recs := srv.Proxy.Records()
	for i := range recs {
		if recs[i].Method == http.MethodPost && strings.HasSuffix(recs[i].Path, "/sync/push") {
			last = &recs[i]
		}
	}
	if last == nil || last.Status != http.StatusConflict {
		t.Errorf("carol's push request = %+v, want a 409", last)
	} else if len(last.PushFiles) != 1 || last.PushFiles[0].ParentSHA != base || last.PushFiles[0].ExpectAbsent {
		t.Errorf("carol sent %+v, want [%s parent %s]", last.PushFiles, rel, base)
	}

	// page_shas and the base did not move; a push sidecar holds dave's sha.
	if got := loadState(t, carol).PageSHAs[rel]; got != base {
		t.Errorf("page_shas[%s] = %s, want the unchanged %s", rel, got, base)
	}
	if b := readBases(t, carol)[rel]; b.SHA != base {
		t.Errorf("bases[%s].sha = %s, want %s", rel, b.SHA, base)
	}
	if got, err := os.ReadFile(syncstate.BaseFile(carol.Hub, rel)); err != nil || !bytes.Equal(got, baseFile) {
		t.Errorf("base file of %s changed (err %v)", rel, err)
	}
	c, ok := readConflicts(t, carol)[rel]
	if !ok {
		t.Fatalf("no conflict sidecar for %s", rel)
	}
	if c.ServerSHA != daveSHA || c.BaseSHA != base || c.Kind != string(contract.ConflictBothModified) ||
		c.Source != "push" || !c.HasContent {
		t.Errorf("sidecar = %+v, want server_sha %s base_sha %s both_modified source push has_content", c, daveSHA, base)
	}
	if !reStrictTime.MatchString(c.DetectedAt) {
		t.Errorf("sidecar detected_at %q is not a whole-second UTC time", c.DetectedAt)
	}
	side, err := os.ReadFile(syncstate.ConflictFile(carol.Hub, rel))
	want := schema.SanitizeContent(string(serverContent(t, srv, repo, rel)))
	if err != nil || string(side) != want {
		t.Errorf("sidecar content (err %v) differs from the sanitized server content", err)
	}
}

// TestScenario08_Busy: a live in-process holder of .sync/lock makes push
// fail busy after CONTEXO_LOCK_TIMEOUT, while status (offline and online)
// never waits (E5).
func TestScenario08_Busy(t *testing.T) {
	requireCtx(t)
	srv := startServer(t)
	spec := conceptPage("locked", "Locked body.")
	rel := pageRel(spec)
	u := newUser(t, srv, "alice", withPages(spec))
	mustPush(t, u, "--yes")
	editPage(t, u, rel, "Edited while the lock is held.")

	holdLock(t, u, "e2e")
	lockBytes, err := os.ReadFile(syncstate.LockPath(u.Hub))
	if err != nil {
		t.Fatal(err)
	}
	var holder syncstate.LockInfo
	if err := json.Unmarshal(lockBytes, &holder); err != nil {
		t.Fatalf("lock file %q: %v", lockBytes, err)
	}
	head := serverHead(t, srv, defaultRepo)

	srv.Proxy.Reset()
	r := runCtxEnv(t, u, []string{"CONTEXO_LOCK_TIMEOUT=500ms"}, "push", "--json", "--yes")
	e := assertFail(t, r, contract.CodeBusy)
	erLog(t, r, e)
	if !e.Retryable {
		t.Errorf("busy must be retryable")
	}
	d, ok := e.Details.(*contract.BusyDetails)
	if !ok {
		t.Fatalf("busy details = %#v", e.Details)
	}
	if d.PID != os.Getpid() || d.Host != holder.Host || d.Command != "e2e" {
		t.Errorf("busy details = %+v, want pid %d host %q command e2e", *d, os.Getpid(), holder.Host)
	}
	if want := holder.StartedAt.UTC().Truncate(time.Second); !d.StartedAt.Equal(want) {
		t.Errorf("busy started_at = %s, want %s", d.StartedAt, want)
	}
	erWantNoRequests(t, srv)
	if got := serverHead(t, srv, defaultRepo); got != head {
		t.Errorf("server HEAD moved from %s to %s", head, got)
	}

	// status never waits on the lock. A generous CONTEXO_LOCK_TIMEOUT makes
	// a status that did wait take a minute.
	wait := []string{"CONTEXO_LOCK_TIMEOUT=60s"}
	var off contract.StatusDoc
	assertOK(t, runCtxEnv(t, u, wait, "status", "--json", "--offline"), &off)
	if !off.Offline || mustPage(t, off, rel).State != contract.StateModified {
		t.Errorf("offline status: offline %v, %s %s", off.Offline, rel, mustPage(t, off, rel).State)
	}

	start := time.Now()
	r = runCtxEnv(t, u, wait, "status", "--json")
	elapsed := time.Since(start)
	var on contract.StatusDoc
	assertOK(t, r, &on)
	if elapsed >= 30*time.Second {
		t.Errorf("online status took %s with the lock held; it must not wait (E5)", elapsed)
	}
	if on.Remote.Source != contract.RemoteLive || mustPage(t, on, rel).State != contract.StateModified {
		t.Errorf("online status: remote.source %s, %s %s", on.Remote.Source, rel, mustPage(t, on, rel).State)
	}

	if got, err := os.ReadFile(syncstate.LockPath(u.Hub)); err != nil || !bytes.Equal(got, lockBytes) {
		t.Errorf("the held lock changed under status: %q (err %v), want %q", got, err, lockBytes)
	}
}

// TestScenario11_HumanModeUnchanged (E21): without --json a usage error is
// human text and exit 1, never an envelope.
func TestScenario11_HumanModeUnchanged(t *testing.T) {
	requireCtx(t)
	srv := startServer(t)
	spec := conceptPage("human", "Human body.")
	u := newUser(t, srv, "alice", withPages(spec))
	r := runCtx(t, u, "resolve", pageRel(spec), "--ours", "--theirs")
	t.Logf("%s", r)
	if r.Exit != 1 {
		t.Fatalf("want exit 1 in human mode\n%s", r)
	}
	assertNoEnvelope(t, r)
	if len(bytes.TrimSpace(r.Stderr)) == 0 {
		t.Errorf("human failure printed nothing on stderr\n%s", r)
	}
}

// ---------------------------------------------------------------- helpers

// erLog records the run and its envelope line (the error.<code> goldens
// reuse these setups).
func erLog(t *testing.T, r ctxResult, e *envelope) {
	t.Helper()
	t.Logf("argv %q exit %d\nenvelope: %s", r.Args, r.Exit, e.Line)
}

// erWantAdvice checks the envelope advice exactly (none: []).
func erWantAdvice(t *testing.T, e *envelope, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !equalStrings(e.Advice, want) {
		t.Errorf("advice = %q, want %q", e.Advice, want)
	}
}

// erWantNoDetails checks that a code without a details type has none.
func erWantNoDetails(t *testing.T, e *envelope) {
	t.Helper()
	if e.Details != nil {
		t.Errorf("unexpected details %#v", e.Details)
	}
}

// erWantHTTP checks the envelope http_status (0: absent).
func erWantHTTP(t *testing.T, e *envelope, status int) {
	t.Helper()
	if e.HTTPStatus != status {
		t.Errorf("http_status = %d, want %d", e.HTTPStatus, status)
	}
}

// erWantNoRequests checks that the proxy saw nothing since its last Reset.
func erWantNoRequests(t *testing.T, srv *e2eServer) {
	t.Helper()
	if recs := srv.Proxy.Records(); len(recs) != 0 {
		t.Errorf("ctx made %d request(s), want none: %+v", len(recs), recs)
	}
}

// erSyncFiles are the .sync index files a refused resolve must not touch.
var erSyncFiles = []string{".sync/state.json", ".sync/bases.json", ".sync/conflicts.json"}

// erSyncSnapshot returns the bytes of u's .sync index files.
func erSyncSnapshot(t *testing.T, u *e2eUser) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, f := range erSyncFiles {
		out[f] = readHubFile(t, u, f)
	}
	return out
}

// erWantSyncUnchanged checks u's .sync index files against a snapshot.
func erWantSyncUnchanged(t *testing.T, u *e2eUser, before map[string][]byte) {
	t.Helper()
	for _, f := range erSyncFiles {
		if got := readHubFile(t, u, f); !bytes.Equal(got, before[f]) {
			t.Errorf("%s changed:\nbefore %s\nafter  %s", f, before[f], got)
		}
	}
}

// erUpstreamSpec is what a synthetic server answers for repo e2e: the
// listing and the push, each a status and a JSON body.
type erUpstreamSpec struct {
	listStatus int
	listBody   string
	pushStatus int
	pushBody   string
}

// erUpstream is a running synthetic server; anything but the listing and
// the push of repo e2e is a non-JSON 404. It records "METHOD path".
type erUpstream struct {
	url  string
	mu   gosync.Mutex
	reqs []string
}

func (up *erUpstream) requests() []string {
	up.mu.Lock()
	defer up.mu.Unlock()
	return append([]string(nil), up.reqs...)
}

func erStartUpstream(t *testing.T, spec erUpstreamSpec) *erUpstream {
	t.Helper()
	up := &erUpstream{}
	reply := func(w http.ResponseWriter, status int, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
	up.url = startUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		up.mu.Lock()
		up.reqs = append(up.reqs, r.Method+" "+r.URL.Path)
		up.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/repos/e2e/pages":
			reply(w, spec.listStatus, spec.listBody)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/repos/e2e/sync/push" && spec.pushStatus != 0:
			reply(w, spec.pushStatus, spec.pushBody)
		default:
			http.NotFound(w, r)
		}
	})
	return up
}
