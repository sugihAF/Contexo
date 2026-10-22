package cli

// Tests of `ctx repair [--dry-run] [--json]` (WP-1.5): the command surface
// and capability, the --json document and its exit codes end to end on a
// testkit server, and the human summary through the repairEngine seam.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	stdsync "sync"
	"sync/atomic"
	"testing"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
)

// rp15Exec runs ctx in-process through Execute with the update check off.
// Unlike wp01Run it keeps HOME: testkit.NewProject already isolated it, and
// a project's own credentials live under its root anyway.
func rp15Exec(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	t.Setenv("CONTEXO_NO_UPDATE_CHECK", "1")
	var out, errb bytes.Buffer
	code = Execute(args, &out, &errb)
	return code, out.String(), errb.String()
}

// rp15Page returns the hub-relative path and the bytes of a valid concept
// page.
func rp15Page(slug, body string) (string, []byte) {
	return "wiki/concepts/" + slug + ".md", testkit.PageBytes(testkit.PageSpec{Slug: slug, Body: body})
}

// rp15Remote starts a testkit server, creates repo on it and returns the
// server with a client for seeding and inspecting it. The client exists in
// the tests only: repair.go gets its client from openRemote.
func rp15Remote(t *testing.T, repo string) (*testkit.Server, *sync.Client) {
	t.Helper()
	s := testkit.StartServer(t)
	c := sync.NewClient(s.URL, s.Key)
	if err := c.CreateRepo(repo); err != nil {
		t.Fatalf("CreateRepo(%s): %v", repo, err)
	}
	return s, c
}

// rp15Push writes one page in its own commit and returns that commit's sha.
func rp15Push(t *testing.T, c *sync.Client, repo, rel string, content []byte, parent string) string {
	t.Helper()
	resp, err := c.PushPages(repo, &sync.PushRequest{
		AuthorName: "Ana", AuthorEmail: "ana@acme.dev", Message: "rp15 " + rel,
		Files: []sync.PushFile{{Path: rel, Content: string(content), ParentSHA: parent}},
	})
	if err != nil || len(resp.Pushed) != 1 || len(resp.Conflicts) != 0 {
		t.Fatalf("push %s: %+v, %v", rel, resp, err)
	}
	return resp.Pushed[0].SHA
}

// rp15Listing returns the live listing as path -> last-touch sha.
func rp15Listing(t *testing.T, c *sync.Client, repo string) map[string]string {
	t.Helper()
	pages, err := c.ListPages(repo)
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	out := map[string]string{}
	for _, p := range pages {
		out[p.Path] = p.SHA
	}
	return out
}

// rp15SaveState writes .sync/state.json with the given page_shas.
func rp15SaveState(t *testing.T, hub string, shas map[string]string, lastPull string) {
	t.Helper()
	if err := sync.SaveState(hub, &sync.State{LastPullSHA: lastPull, PageSHAs: shas}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

// rp15PageSHAs reads page_shas back from state.json.
func rp15PageSHAs(t *testing.T, hub string) map[string]string {
	t.Helper()
	st, err := sync.LoadState(hub)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return st.PageSHAs
}

// rp15BaseValid reports whether rel has a base valid for sha that also
// still hashes to its record.
func rp15BaseValid(t *testing.T, hub, rel, sha string) bool {
	t.Helper()
	b, err := syncstate.OpenBases(hub)
	if err != nil {
		t.Fatalf("OpenBases: %v", err)
	}
	if !b.Valid(rel, sha) {
		return false
	}
	_, err = b.ReadVerified(rel)
	return err == nil
}

// rp15Classify classifies rel against the live listing, as status does.
func rp15Classify(t *testing.T, hub string, listing map[string]string, rel string) contract.PageState {
	t.Helper()
	v, err := syncstate.LoadView(hub, listing)
	if err != nil {
		t.Fatalf("LoadView: %v", err)
	}
	f := v.Facts[rel]
	if f == nil {
		t.Fatalf("%s is not an entry of the view", rel)
	}
	r, err := contract.ClassifySides(contract.Side(f.Local), contract.Side(f.Base), contract.Side(f.Remote))
	if err != nil {
		t.Fatalf("ClassifySides(%s): %v", rel, err)
	}
	return r.State
}

// rp15DecodeResult strictly decodes stdout as exactly one RepairResult.
func rp15DecodeResult(t *testing.T, stdout string) syncops.RepairResult {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	var res syncops.RepairResult
	if err := dec.Decode(&res); err != nil {
		t.Fatalf("stdout is not a strict RepairResult: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one document: %q", stdout)
	}
	return res
}

// rp15TopKeys returns the top-level keys of the JSON object doc, in order.
func rp15TopKeys(t *testing.T, doc string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(doc))
	tok, err := dec.Token()
	if d, ok := tok.(json.Delim); err != nil || !ok || d != '{' {
		t.Fatalf("document is not an object: %v %v", tok, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok {
			t.Fatalf("bad key token %v: %v", tok, err)
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("value of %q: %v", key, err)
		}
	}
	return keys
}

// rp15SkippedPaths returns the skipped paths, in document order.
func rp15SkippedPaths(res syncops.RepairResult) []string {
	out := []string{}
	for _, s := range res.Skipped {
		out = append(out, s.Path)
	}
	return out
}

// rp15CheckSkipWarning asserts the E18 relation between skipped, unverified
// and the single base_repair_skipped warning.
func rp15CheckSkipWarning(t *testing.T, res syncops.RepairResult) {
	t.Helper()
	skipped := rp15SkippedPaths(res)
	if !sort.StringsAreSorted(skipped) {
		t.Errorf("skipped is not sorted by path: %q", skipped)
	}
	if !reflect.DeepEqual(res.Unverified, skipped) {
		t.Errorf("unverified = %q, want the skipped paths %q", res.Unverified, skipped)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != contract.WarnBaseRepairSkipped {
		t.Fatalf("warnings = %+v, want one base_repair_skipped", res.Warnings)
	}
	if !reflect.DeepEqual(res.Warnings[0].Paths, skipped) {
		t.Errorf("warning paths = %q, want %q", res.Warnings[0].Paths, skipped)
	}
}

func TestRepairNotHidden(t *testing.T) {
	cmd, _, err := NewRootCmd().Find([]string{"repair"})
	if err != nil || cmd == nil || cmd.Name() != "repair" {
		t.Fatalf("ctx repair is not registered: %v", err)
	}
	if cmd.Hidden {
		t.Error("ctx repair is hidden; WP-1.5 ships it")
	}
}

func TestRepairFlagsAndArgs(t *testing.T) {
	repair := wp01Command(t, "repair")
	for _, name := range []string{"dry-run", "json"} {
		f := repair.Flags().Lookup(name)
		if f == nil {
			t.Errorf("--%s is not defined", name)
			continue
		}
		if f.Value.Type() != "bool" || f.DefValue != "false" {
			t.Errorf("--%s is %s=%q, want bool=false", name, f.Value.Type(), f.DefValue)
		}
	}

	// repair takes no positional args; one is usage.
	if c := wp01ArgsCode(t, repair); c != "" {
		t.Errorf("no args rejected: %q", c)
	}
	if c := wp01ArgsCode(t, repair, "extra"); c != contract.CodeUsage {
		t.Errorf("extra arg -> %q, want usage", c)
	}
	code, stdout, stderr := wp01Run(t, "repair", "extra", "--json", "--root", wp01InitRoot(t))
	if code != contract.ExitUsage || stdout != "" {
		t.Errorf("extra arg: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if env := wp01Envelope(t, stderr); env.Error.Code != contract.CodeUsage {
		t.Errorf("extra arg: code = %q, want usage", env.Error.Code)
	}
}

func TestRepairCapabilityAdvertised(t *testing.T) {
	code, stdout, stderr := wp01Run(t, "version", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("version --json: exit %d stderr %q", code, stderr)
	}
	caps := wp01DecodeVersionDoc(t, stdout).Capabilities
	wp01CheckCapabilities(t, caps)
	if n := len(caps); n == 0 || caps[n-1] != contract.CapRepair {
		t.Errorf("capabilities = %q, want repair last", caps)
	}
}

// One page with a state sha but no base: a dry run reports the backfill and
// writes nothing, the real run writes it. Both documents have the E18 shape.
func TestRepairJSON_Shape(t *testing.T) {
	const repo = "rp15-shape"
	s, c := rp15Remote(t, repo)
	rel, page := rp15Page("a", "v1")
	sha := rp15Push(t, c, repo, rel, page, "")
	p := testkit.NewProject(t, testkit.ProjectOptions{ServerURL: s.URL, Key: s.Key, RepoID: repo})
	p.WriteRaw(rel, page)
	rp15SaveState(t, p.HubDir, map[string]string{rel: sha}, sha)

	wantKeys := []string{"schema", "dry_run", "server_capabilities", "backfilled",
		"repaired", "skipped", "unverified", "warnings"}
	for _, dry := range []bool{true, false} {
		args := []string{"repair", "--json", "--root", p.Root}
		if dry {
			args = append(args, "--dry-run")
		}
		code, stdout, stderr := rp15Exec(t, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%q: exit %d, stderr %q", args, code, stderr)
		}
		res := rp15DecodeResult(t, stdout)
		if keys := rp15TopKeys(t, stdout); !reflect.DeepEqual(keys, wantKeys) {
			t.Errorf("%q: keys = %q, want %q", args, keys, wantKeys)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
			t.Fatal(err)
		}
		for _, k := range wantKeys[2:] {
			if !bytes.HasPrefix(bytes.TrimSpace(raw[k]), []byte("[")) {
				t.Errorf("%q: %s = %s, want an array", args, k, raw[k])
			}
		}
		var want bytes.Buffer
		if err := contract.WriteDoc(&want, res); err != nil {
			t.Fatal(err)
		}
		if stdout != want.String() {
			t.Errorf("%q: stdout is not WriteDoc output:\n got %q\nwant %q", args, stdout, want.String())
		}

		if res.Schema != contract.SchemaRepair || res.DryRun != dry {
			t.Errorf("%q: schema %q dry_run %v", args, res.Schema, res.DryRun)
		}
		hasAt := false
		for _, sc := range res.ServerCapabilities {
			hasAt = hasAt || sc == contract.SrvPagesAt
		}
		if !hasAt {
			t.Errorf("%q: server_capabilities = %q, want pages.at", args, res.ServerCapabilities)
		}
		if want := []syncops.RepairBackfilled{{Path: rel, SHA: sha}}; !reflect.DeepEqual(res.Backfilled, want) {
			t.Errorf("%q: backfilled = %+v, want %+v", args, res.Backfilled, want)
		}
		if n := len(res.Repaired) + len(res.Skipped) + len(res.Unverified) + len(res.Warnings); n != 0 {
			t.Errorf("%q: unexpected entries: %+v", args, res)
		}
		if valid := rp15BaseValid(t, p.HubDir, rel, sha); valid == dry {
			t.Errorf("%q: base valid = %v after the run", args, valid)
		}
	}
}

func TestRepairJSON_BusyExit12(t *testing.T) {
	const repo = "rp15-busy"
	s, c := rp15Remote(t, repo)
	rel, page := rp15Page("a", "v1")
	sha := rp15Push(t, c, repo, rel, page, "")
	p := testkit.NewProject(t, testkit.ProjectOptions{ServerURL: s.URL, Key: s.Key, RepoID: repo})
	p.WriteRaw(rel, page)
	rp15SaveState(t, p.HubDir, map[string]string{rel: sha}, sha)

	unlock, err := syncstate.Lock(p.HubDir, "push", 0)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer unlock()
	t.Setenv("CONTEXO_LOCK_TIMEOUT", "0")

	code, stdout, stderr := rp15Exec(t, "repair", "--json", "--root", p.Root)
	if code != contract.ExitBusy {
		t.Fatalf("exit = %d, want 12 (stderr %q)", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	lines := wp01NonEmptyLines(stderr)
	if len(lines) != 1 {
		t.Fatalf("stderr must be one envelope line: %q", stderr)
	}
	if env := wp01Envelope(t, stderr); env.Error.Code != contract.CodeBusy {
		t.Fatalf("code = %q, want busy", env.Error.Code)
	}
	var raw struct {
		Error struct {
			Details contract.BusyDetails `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &raw); err != nil {
		t.Fatal(err)
	}
	d := raw.Error.Details
	if d.PID != os.Getpid() || d.Command != "push" || d.Host == "" || d.StartedAt.IsZero() {
		t.Errorf("busy details = %+v, want this process's push lock", d)
	}
	if rp15BaseValid(t, p.HubDir, rel, sha) {
		t.Error("a busy repair wrote a base")
	}
}

// The old gitstore bug: page_shas[a] holds the repo HEAD (b's commit), and no
// base exists. Repair moves a back to its own last-touch commit through the
// ?at X-Page-SHA and backfills b, after which a classifies unchanged.
func TestRepairE2E_StateFromBuggyServer(t *testing.T) {
	const repo = "rp15-buggy"
	s, c := rp15Remote(t, repo)
	relA, pageA := rp15Page("a", "A v1")
	relB, pageB := rp15Page("b", "B v1")
	shaA := rp15Push(t, c, repo, relA, pageA, "")
	shaB := rp15Push(t, c, repo, relB, pageB, "")
	if shaA == shaB {
		t.Fatalf("two pushes made one commit %s", shaA)
	}

	p := testkit.NewProject(t, testkit.ProjectOptions{ServerURL: s.URL, Key: s.Key, RepoID: repo})
	p.WriteRaw(relA, pageA)
	p.WriteRaw(relB, pageB)
	rp15SaveState(t, p.HubDir, map[string]string{relA: shaB, relB: shaB}, shaB)
	if err := os.RemoveAll(syncstate.BaseFile(p.HubDir, "")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(syncstate.BasesPath(p.HubDir)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	code, stdout, stderr := rp15Exec(t, "repair", "--json", "--root", p.Root)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	res := rp15DecodeResult(t, stdout)
	wantRepaired := []syncops.RepairedPage{{Path: relA, OldSHA: shaB, NewSHA: shaA, Reason: "path_sha_fixed"}}
	if !reflect.DeepEqual(res.Repaired, wantRepaired) {
		t.Errorf("repaired = %+v, want %+v", res.Repaired, wantRepaired)
	}
	if want := []syncops.RepairBackfilled{{Path: relB, SHA: shaB}}; !reflect.DeepEqual(res.Backfilled, want) {
		t.Errorf("backfilled = %+v, want %+v", res.Backfilled, want)
	}
	if len(res.Skipped)+len(res.Unverified)+len(res.Warnings) != 0 {
		t.Errorf("unexpected skips: %+v", res)
	}

	if got := rp15PageSHAs(t, p.HubDir); got[relA] != shaA || got[relB] != shaB {
		t.Errorf("page_shas = %v, want a=%s b=%s", got, shaA, shaB)
	}
	if !rp15BaseValid(t, p.HubDir, relA, shaA) || !rp15BaseValid(t, p.HubDir, relB, shaB) {
		t.Error("bases are not valid and verified after repair")
	}
	listing := rp15Listing(t, c, repo)
	for _, rel := range []string{relA, relB} {
		if st := rp15Classify(t, p.HubDir, listing, rel); st != contract.StateUnchanged {
			t.Errorf("%s classifies %q, want unchanged", rel, st)
		}
	}
	if !bytes.Equal(p.Read(relA), pageA) || !bytes.Equal(p.Read(relB), pageB) {
		t.Error("repair changed a working file")
	}
}

// rp15ProxyStats counts what the rate-limiting proxy saw.
type rp15ProxyStats struct {
	atRequests atomic.Int32 // requests whose query has "at"
	limited    atomic.Int32 // 429 answers sent
}

// rp15RateLimitProxy is a reverse proxy in front of upstream that answers
// 429 to the first request whose query has "at", with the capability and
// version headers of the last upstream response (a rate limiter in front of
// a v1 server still carries them). Everything else is forwarded.
func rp15RateLimitProxy(t *testing.T, upstream string) (*httptest.Server, *rp15ProxyStats) {
	t.Helper()
	target, err := url.Parse(upstream)
	if err != nil {
		t.Fatal(err)
	}
	stats := &rp15ProxyStats{}
	var mu stdsync.Mutex
	seen := http.Header{}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.ModifyResponse = func(resp *http.Response) error {
		mu.Lock()
		defer mu.Unlock()
		for _, h := range []string{"X-Contexo-Capabilities", "X-Contexo-Server-Version"} {
			if v := resp.Header.Values(h); len(v) > 0 {
				seen[h] = append([]string(nil), v...)
			}
		}
		return nil
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("at") && stats.atRequests.Add(1) == 1 {
			stats.limited.Add(1)
			mu.Lock()
			for h, v := range seen {
				w.Header()[h] = append([]string(nil), v...)
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	return proxy, stats
}

// Several pages moved on the server since their state sha, so repair needs
// ?at for each; the first ?at answer is a 429. Repair stops there, reports
// rate_limited skips with the warning, and still exits 0.
func TestRepairCLI_StopsOn429ExitsZero(t *testing.T) {
	const repo = "rp15-429"
	s, c := rp15Remote(t, repo)
	const n = 6
	rels := make([]string, n)
	v1 := map[string][]byte{}
	shas := map[string]string{}
	for i := range rels {
		rel, page := rp15Page(fmt.Sprintf("p%d", i), "v1")
		rels[i], v1[rel] = rel, page
		shas[rel] = rp15Push(t, c, repo, rel, page, "")
	}
	for i, rel := range rels {
		_, page := rp15Page(fmt.Sprintf("p%d", i), "v2")
		rp15Push(t, c, repo, rel, page, shas[rel])
	}

	proxy, stats := rp15RateLimitProxy(t, s.URL)
	p := testkit.NewProject(t, testkit.ProjectOptions{ServerURL: proxy.URL, Key: s.Key, RepoID: repo})
	for _, rel := range rels {
		p.WriteRaw(rel, v1[rel])
	}
	rp15SaveState(t, p.HubDir, shas, "")

	code, stdout, stderr := rp15Exec(t, "repair", "--json", "--root", p.Root)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	res := rp15DecodeResult(t, stdout)
	if stats.limited.Load() != 1 {
		t.Errorf("the proxy sent %d 429s, want 1", stats.limited.Load())
	}
	if got := stats.atRequests.Load(); got < 1 || got > n {
		t.Errorf("%d ?at requests, want 1..%d", got, n)
	}
	if len(res.Skipped) == 0 {
		t.Fatalf("no skips after a 429: %+v", res)
	}
	for _, sk := range res.Skipped {
		if sk.Reason != string(contract.CodeRateLimited) {
			t.Errorf("skip %+v, want reason rate_limited", sk)
		}
	}
	rp15CheckSkipWarning(t, res)

	// Every stale path is either backfilled at its state sha or skipped.
	done := map[string]bool{}
	for _, b := range res.Backfilled {
		if b.SHA != shas[b.Path] {
			t.Errorf("backfilled %+v, want sha %s", b, shas[b.Path])
		}
		if !rp15BaseValid(t, p.HubDir, b.Path, b.SHA) {
			t.Errorf("backfilled %s has no valid base", b.Path)
		}
		done[b.Path] = true
	}
	for _, sk := range res.Skipped {
		if done[sk.Path] {
			t.Errorf("%s is both backfilled and skipped", sk.Path)
		}
		if rp15BaseValid(t, p.HubDir, sk.Path, shas[sk.Path]) {
			t.Errorf("skipped %s got a base", sk.Path)
		}
		done[sk.Path] = true
	}
	if len(done) != n || len(res.Repaired) != 0 {
		t.Errorf("accounted for %d of %d paths (repaired %+v)", len(done), n, res.Repaired)
	}
}

func TestRepairJSON_ErrorExits(t *testing.T) {
	// unreachable is never contacted: these failures happen before the
	// engine (or, for repo_not_found, a real server is used).
	const unreachable = "http://127.0.0.1:1"
	cases := []struct {
		name   string
		root   func(t *testing.T) string
		engine bool // the engine may run (it reports the failure)
		exit   int
		code   contract.ErrorCode
		advice []string // checked exactly when non-nil
	}{
		{name: "not initialized", root: func(t *testing.T) string {
			wp01IsolateEnv(t)
			return t.TempDir()
		}, exit: contract.ExitConfig, code: contract.CodeNotInitialized, advice: []string{"ctx init"}},
		{name: "no credentials", root: func(t *testing.T) string {
			return testkit.NewProject(t, testkit.ProjectOptions{ServerURL: unreachable, RepoID: "rp15", NoCreds: true}).Root
		}, exit: contract.ExitAuth, code: contract.CodeNotAuthenticated, advice: []string{"ctx auth login"}},
		{name: "server unresolved", root: func(t *testing.T) string {
			return testkit.NewProject(t, testkit.ProjectOptions{Key: "rp15-key", RepoID: "rp15"}).Root
		}, exit: contract.ExitConfig, code: contract.CodeServerUnresolved},
		{name: "empty repo id", root: func(t *testing.T) string {
			return testkit.NewProject(t, testkit.ProjectOptions{ServerURL: unreachable, Key: "rp15-key"}).Root
		}, exit: contract.ExitConfig, code: contract.CodeRepoNotConfigured, advice: []string{"ctx remote set-repo <id>"}},
		{name: "unknown repo on the server", root: func(t *testing.T) string {
			s := testkit.StartServer(t)
			return testkit.NewProject(t, testkit.ProjectOptions{ServerURL: s.URL, Key: s.Key, RepoID: "rp15-missing"}).Root
		}, engine: true, exit: contract.ExitNotFound, code: contract.CodeRepoNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := c.root(t)
			if !c.engine {
				prev := repairEngine
				repairEngine = func(context.Context, syncops.Env, syncops.RepairOptions) (*syncops.RepairResult, error) {
					t.Error("the engine ran although the CLI preflight must fail first")
					return nil, syncops.New(contract.CodeInternal, "rp15: engine reached")
				}
				t.Cleanup(func() { repairEngine = prev })
			}
			code, stdout, stderr := rp15Exec(t, "repair", "--json", "--root", root)
			wp01CheckEnvelopeCase(t, wp01EnvelopeCase{
				name: c.name, exit: c.exit, code: c.code, advice: c.advice,
			}, code, stdout, stderr)
		})
	}
}

// rp15SeamProject is a project whose remote resolves (credentials, server
// URL, repo) but is never contacted: its tests replace the engine.
func rp15SeamProject(t *testing.T) *testkit.Project {
	t.Helper()
	return testkit.NewProject(t, testkit.ProjectOptions{
		ServerURL: "http://127.0.0.1:1", Key: "rp15-key", RepoID: "rp15-seam",
	})
}

// rp15SetEngine replaces repairEngine for the rest of the test.
func rp15SetEngine(t *testing.T, f func(context.Context, syncops.Env, syncops.RepairOptions) (*syncops.RepairResult, error)) {
	t.Helper()
	prev := repairEngine
	repairEngine = f
	t.Cleanup(func() { repairEngine = prev })
}

// rp15SampleResult has one of everything: two backfills, a path_sha_fixed
// repair and a budget_exhausted plus a rate_limited skip.
func rp15SampleResult(dryRun bool) *syncops.RepairResult {
	r := syncops.NewRepairResult(dryRun)
	r.ServerCapabilities = []contract.ServerCapability{contract.SrvPagesAt}
	r.Backfilled = []syncops.RepairBackfilled{
		{Path: "wiki/concepts/b.md", SHA: strings.Repeat("b", 40)},
		{Path: "wiki/concepts/c.md", SHA: strings.Repeat("c", 40)},
	}
	r.Repaired = []syncops.RepairedPage{{
		Path: "wiki/concepts/a.md", OldSHA: "2222222222" + strings.Repeat("0", 30),
		NewSHA: "1111111111" + strings.Repeat("0", 30), Reason: "path_sha_fixed",
	}}
	r.Skipped = []syncops.RepairSkipped{
		{Path: "wiki/concepts/d.md", Reason: "budget_exhausted"},
		{Path: "wiki/concepts/e.md", Reason: "rate_limited"},
	}
	r.Unverified = []string{"wiki/concepts/d.md", "wiki/concepts/e.md"}
	r.Warnings = []contract.Warning{{
		Code:    contract.WarnBaseRepairSkipped,
		Message: "2 page(s) left unverified: base repair skipped (budget_exhausted, rate_limited)",
		Paths:   r.Unverified,
	}}
	return r
}

// The CLI hands the engine the resolved project and remote and the
// --dry-run flag, and --json prints exactly the engine's document.
func TestRepairCLI_EngineSeamGetsEnv(t *testing.T) {
	p := rp15SeamProject(t)
	for _, dry := range []bool{false, true} {
		var calls int
		var gotEnv syncops.Env
		var gotOpts syncops.RepairOptions
		want := rp15SampleResult(dry)
		rp15SetEngine(t, func(ctx context.Context, env syncops.Env, o syncops.RepairOptions) (*syncops.RepairResult, error) {
			calls++
			if ctx == nil {
				t.Error("engine got a nil context")
			}
			gotEnv, gotOpts = env, o
			return want, nil
		})
		args := []string{"repair", "--json", "--root", p.Root}
		if dry {
			args = append(args, "--dry-run")
		}
		code, stdout, stderr := rp15Exec(t, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%q: exit %d stderr %q", args, code, stderr)
		}
		if calls != 1 {
			t.Fatalf("%q: engine ran %d times", args, calls)
		}
		var doc bytes.Buffer
		if err := contract.WriteDoc(&doc, want); err != nil {
			t.Fatal(err)
		}
		if stdout != doc.String() {
			t.Errorf("%q: stdout is not the engine's document:\n got %q\nwant %q", args, stdout, doc.String())
		}
		if gotOpts.DryRun != dry {
			t.Errorf("%q: DryRun = %v", args, gotOpts.DryRun)
		}
		if gotEnv.Root != p.Root || gotEnv.HubDir != filepath.Join(p.Root, ".contexo") ||
			gotEnv.RepoID != "rp15-seam" || gotEnv.ServerURL != "http://127.0.0.1:1" {
			t.Errorf("%q: env = root %q hub %q repo %q server %q", args,
				gotEnv.Root, gotEnv.HubDir, gotEnv.RepoID, gotEnv.ServerURL)
		}
		if gotEnv.Remote == nil || !gotEnv.Online() {
			t.Errorf("%q: env has no remote", args)
		}
		if gotEnv.Author != (syncops.Author{}) {
			t.Errorf("%q: author = %+v, want zero (repair never commits)", args, gotEnv.Author)
		}
	}
}

func TestRepairHuman_Summary(t *testing.T) {
	p := rp15SeamProject(t)

	t.Run("real run", func(t *testing.T) {
		rp15SetEngine(t, func(_ context.Context, _ syncops.Env, o syncops.RepairOptions) (*syncops.RepairResult, error) {
			return rp15SampleResult(o.DryRun), nil
		})
		code, stdout, stderr := rp15Exec(t, "repair", "--root", p.Root)
		if code != 0 || stderr != "" {
			t.Fatalf("exit %d stderr %q", code, stderr)
		}
		for _, want := range []string{
			"2 backfilled", "1 repaired", "2 skipped",
			"wiki/concepts/a.md", "22222222", "11111111", "path_sha_fixed",
			"1 budget_exhausted", "1 rate_limited", "ctx repair",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("human output lacks %q:\n%s", want, stdout)
			}
		}
		if strings.Contains(strings.ToLower(stdout), "would") || strings.Contains(stdout, "{") {
			t.Errorf("real-run output reads like a dry run or JSON:\n%s", stdout)
		}
	})

	t.Run("dry run", func(t *testing.T) {
		var gotDry bool
		rp15SetEngine(t, func(_ context.Context, _ syncops.Env, o syncops.RepairOptions) (*syncops.RepairResult, error) {
			gotDry = o.DryRun
			return rp15SampleResult(o.DryRun), nil
		})
		code, stdout, stderr := rp15Exec(t, "repair", "--dry-run", "--root", p.Root)
		if code != 0 || stderr != "" {
			t.Fatalf("exit %d stderr %q", code, stderr)
		}
		if !gotDry {
			t.Error("--dry-run did not reach the engine")
		}
		for _, want := range []string{"would backfill 2", "repair 1", "skip 2", "wiki/concepts/a.md"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("dry-run output lacks %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("nothing to repair", func(t *testing.T) {
		rp15SetEngine(t, func(_ context.Context, _ syncops.Env, o syncops.RepairOptions) (*syncops.RepairResult, error) {
			return syncops.NewRepairResult(o.DryRun), nil
		})
		code, stdout, stderr := rp15Exec(t, "repair", "--root", p.Root)
		if code != 0 || stderr != "" {
			t.Fatalf("exit %d stderr %q", code, stderr)
		}
		if !strings.Contains(stdout, "Nothing to repair") {
			t.Errorf("output = %q, want Nothing to repair", stdout)
		}
		if strings.Contains(stdout, "ctx repair") {
			t.Errorf("a clean run advises a re-run: %q", stdout)
		}
	})

	t.Run("engine error shows its advice", func(t *testing.T) {
		rp15SetEngine(t, func(context.Context, syncops.Env, syncops.RepairOptions) (*syncops.RepairResult, error) {
			return nil, contract.New(contract.CodeNotAuthenticated, "repair: not logged in", "ctx auth login")
		})
		code, stdout, stderr := rp15Exec(t, "repair", "--root", p.Root)
		if code != 1 || stdout != "" {
			t.Fatalf("human: exit %d stdout %q", code, stdout)
		}
		if !strings.Contains(stderr, "repair: not logged in") || !strings.Contains(stderr, "ctx auth login") {
			t.Errorf("human error lacks the message or its advice: %q", stderr)
		}

		// The envelope keeps the code and the advice, once.
		code, stdout, stderr = rp15Exec(t, "repair", "--json", "--root", p.Root)
		wp01CheckEnvelopeCase(t, wp01EnvelopeCase{
			name: "engine error", exit: contract.ExitAuth, code: contract.CodeNotAuthenticated,
			advice: []string{"ctx auth login"},
		}, code, stdout, stderr)
	})
}
