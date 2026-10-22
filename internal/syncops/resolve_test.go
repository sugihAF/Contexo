package syncops

// Owner: WP-1.4. Resolve engine tests. They use syncopstest.FakeRemote
// together with resolve.go's unexported seams (syncopstest never imports
// syncops). Helpers are prefixed sr14Resolve so they never collide with
// show_test.go's sr14Show* helpers.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sugihAF/contexo/internal/fsutil"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncops/syncopstest"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
	"github.com/sugihAF/contexo/internal/ui"
)

// sr14ResolveRel is the page every resolve fixture works on.
const sr14ResolveRel = "wiki/concepts/a.md"

// sr14ResolveNow is the fixed clock of the resolve fixtures:
// 2026-11-03T09:15:30+07:00, which is 02:15:30 UTC.
var sr14ResolveNow = time.Date(2026, 11, 3, 9, 15, 30, 0, time.FixedZone("WIB", 7*60*60))

// sr14ResolveBackupRel is BackupRel(sr14ResolveRel, sr14ResolveNow).
const sr14ResolveBackupRel = ".sync/backup/wiki/concepts/a.md.20261103T021530Z"

// sr14ResolvePage returns a valid concept page with slug "a" (path
// sr14ResolveRel) whose body ends with body.
func sr14ResolvePage(body string) []byte {
	return testkit.PageBytes(testkit.PageSpec{Slug: "a", Body: body})
}

// sr14ResolveSanitize is what resolve stores and writes for server bytes.
func sr14ResolveSanitize(b []byte) []byte {
	return []byte(schema.SanitizeContent(string(b)))
}

// sr14ResolveIsolate points HOME and USERPROFILE at a fresh temp dir, so no
// resolve test can reach the developer's real credentials.
func sr14ResolveIsolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// sr14ResolveEnv is an online Env over a fresh, empty hub with a
// FakeRemote and the fixed clock. HOME and USERPROFILE are isolated.
func sr14ResolveEnv(t *testing.T) (Env, *syncopstest.FakeRemote) {
	t.Helper()
	sr14ResolveIsolate(t)
	fake := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := wp01StubEnv(t)
	env.Remote = fake
	env.Now = func() time.Time { return sr14ResolveNow }
	return env, fake
}

// sr14ResolveAbs is the working file of rel.
func sr14ResolveAbs(env Env, rel string) string {
	return filepath.Join(env.HubDir, filepath.FromSlash(rel))
}

// sr14ResolveWrite writes data as the working file rel.
func sr14ResolveWrite(t *testing.T, env Env, rel string, data []byte) {
	t.Helper()
	abs := sr14ResolveAbs(env, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// sr14ResolveRead returns the bytes at abs, failing the test when missing.
func sr14ResolveRead(t *testing.T, abs string) []byte {
	t.Helper()
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	return b
}

// sr14ResolvePageSHA sets state.json page_shas[rel] = sha.
func sr14ResolvePageSHA(t *testing.T, env Env, rel, sha string) {
	t.Helper()
	st, _, err := sync.LoadStateOrEmpty(env.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	st.PageSHAs[rel] = sha
	if err := sync.SaveState(env.HubDir, st); err != nil {
		t.Fatal(err)
	}
}

// sr14ResolveBase records content as the valid base of rel at sha: the
// base file, its bases.json record and page_shas[rel].
func sr14ResolveBase(t *testing.T, env Env, rel string, content []byte, sha string) {
	t.Helper()
	b, err := syncstate.OpenBases(env.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Put(rel, content, sha, sr14ResolveNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	sr14ResolvePageSHA(t, env, rel, sha)
}

// sr14ResolveSidecar records a conflict sidecar with content.
func sr14ResolveSidecar(t *testing.T, env Env, rec syncstate.ConflictRecord, content []byte) {
	t.Helper()
	c, err := syncstate.OpenConflicts(env.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.DetectedAt.IsZero() {
		rec.DetectedAt = sr14ResolveNow.Add(-time.Minute)
	}
	if err := c.Put(rec, content); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
}

// sr14ResolveFix is a hub plus a fake server around sr14ResolveRel.
type sr14ResolveFix struct {
	env  Env
	fake *syncopstest.FakeRemote
	abs  string // the working file
	// s1 is the base sha, s2 the server's current sha.
	s1, s2 string
	// base is the base content (at s1), local the working file, server the
	// raw server content at s2 (it holds an ESC byte).
	base, local, server []byte
}

// sr14ResolveConflict is the both_modified fixture: base A at S1, the
// working file edited, the server at S2 with a teammate's edit holding an
// ESC byte, and the push sidecar {server_sha S2, base_sha S1}.
func sr14ResolveConflict(t *testing.T) *sr14ResolveFix {
	t.Helper()
	env, fake := sr14ResolveEnv(t)
	x := &sr14ResolveFix{env: env, fake: fake, abs: sr14ResolveAbs(env, sr14ResolveRel)}
	x.base = sr14ResolvePage("The base version.")
	x.s1 = fake.Seed(sr14ResolveRel, x.base)
	sr14ResolveBase(t, env, sr14ResolveRel, x.base, x.s1)
	x.local = sr14ResolvePage("My local edit.")
	sr14ResolveWrite(t, env, sr14ResolveRel, x.local)
	x.server = sr14ResolvePage("A teammate's edit \x1b[31mwith an escape\x1b[0m.")
	if string(sr14ResolveSanitize(x.server)) == string(x.server) {
		t.Fatal("fixture: the server content lost its ESC byte")
	}
	x.s2 = fake.Seed(sr14ResolveRel, x.server)
	sr14ResolveSidecar(t, env, syncstate.ConflictRecord{
		Path: sr14ResolveRel, ServerSHA: x.s2, BaseSHA: x.s1,
		Kind: syncstate.KindBothModified, Source: syncstate.SourcePush,
	}, sr14ResolveSanitize(x.server))
	return x
}

// sr14ResolveRun runs Resolve on sr14ResolveRel.
func sr14ResolveRun(env Env, s contract.ResolveStrategy, ifServerSHA string) (*contract.ResolveDoc, error) {
	return Resolve(context.Background(), env, ResolveOptions{
		Path: sr14ResolveRel, Strategy: s, IfServerSHA: ifServerSHA,
	})
}

// sr14ResolveCode asserts err maps (through ui.ToContractError, as the
// envelope does) to code and returns the contract error.
func sr14ResolveCode(t *testing.T, err error, code contract.ErrorCode) *contract.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("got a nil error, want %s", code)
	}
	ce := ui.ToContractError(err)
	if ce.Code != code {
		t.Fatalf("code = %q, want %q (error %v)", ce.Code, code, err)
	}
	return ce
}

// sr14ResolveTree snapshots every file under hub (symlinks as their
// target), keyed by its '/'-separated path. .sync/.gitignore is left out:
// Lock creates it on the way to .sync/lock.
func sr14ResolveTree(t *testing.T, hub string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(hub, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(hub, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[rel] = "symlink -> " + target
		case d.IsDir(), rel == ".sync/.gitignore":
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// sr14ResolveSameTree fails for every file that differs between two
// sr14ResolveTree snapshots.
func sr14ResolveSameTree(t *testing.T, before, after map[string]string) {
	t.Helper()
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	var changed []string
	for k := range keys {
		b, inBefore := before[k]
		a, inAfter := after[k]
		if inBefore != inAfter || a != b {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	if len(changed) > 0 {
		t.Errorf("files changed although nothing may be written: %q", changed)
	}
}

// sr14ResolveNoBackup fails when .sync/backup exists.
func sr14ResolveNoBackup(t *testing.T, env Env) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(syncstate.SyncDir(env.HubDir), "backup")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(".sync/backup exists (or stat failed: %v)", err)
	}
}

// sr14ResolveRequests fails unless the fake saw exactly want.
func sr14ResolveRequests(t *testing.T, fake *syncopstest.FakeRemote, want map[string]int) {
	t.Helper()
	if got := fake.Requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

// sr14ResolveNoSHA is a FakeRemote whose ReadPage answers without an
// X-Page-SHA header.
type sr14ResolveNoSHA struct{ *syncopstest.FakeRemote }

func (r sr14ResolveNoSHA) ReadPage(repoID, path string) ([]byte, string, error) {
	b, _, err := r.FakeRemote.ReadPage(repoID, path)
	return b, "", err
}

// --- PREPARE ---

func TestResolvePreflight(t *testing.T) {
	sr14ResolveIsolate(t)
	env := wp01StubEnv(t)
	sha := strings.Repeat("ab", 20)
	for _, o := range []ResolveOptions{
		{Path: sr14ResolveRel, Strategy: contract.ResolveOurs},
		{Path: ".contexo/" + sr14ResolveRel, Strategy: contract.ResolveTheirs, IfServerSHA: sha},
		{Path: sr14ResolveAbs(env, sr14ResolveRel), Strategy: contract.ResolveTheirs, IfServerSHA: strings.ToUpper(sha[:7])},
	} {
		rel, err := ResolvePreflight(env, o)
		if err != nil || rel != sr14ResolveRel {
			t.Errorf("ResolvePreflight(%+v) = %q, %v; want %q", o, rel, err, sr14ResolveRel)
		}
	}
}

func TestResolve_BadArgs(t *testing.T) {
	for _, c := range []struct {
		name string
		o    ResolveOptions
		code contract.ErrorCode
	}{
		{"dot-dot path", ResolveOptions{Path: "../x.md", Strategy: contract.ResolveOurs}, contract.CodeInvalidPath},
		{"sync file", ResolveOptions{Path: ".sync/state.json", Strategy: contract.ResolveTheirs}, contract.CodeInvalidPath},
		{"credentials", ResolveOptions{Path: "credentials.json", Strategy: contract.ResolveTheirs}, contract.CodeInvalidPath},
		{"path wins over strategy", ResolveOptions{Path: "wiki/.hidden.md", Strategy: "both"}, contract.CodeInvalidPath},
		{"both strategies", ResolveOptions{Path: sr14ResolveRel, Strategy: "both"}, contract.CodeUsage},
		{"no strategy", ResolveOptions{Path: sr14ResolveRel}, contract.CodeUsage},
		{"non-hex sha", ResolveOptions{Path: sr14ResolveRel, Strategy: contract.ResolveOurs, IfServerSHA: "zz"}, contract.CodeUsage},
		{"6-hex sha", ResolveOptions{Path: sr14ResolveRel, Strategy: contract.ResolveOurs, IfServerSHA: "abc123"}, contract.CodeUsage},
		{"65-hex sha", ResolveOptions{Path: sr14ResolveRel, Strategy: contract.ResolveTheirs, IfServerSHA: strings.Repeat("a", 65)}, contract.CodeUsage},
	} {
		t.Run(c.name, func(t *testing.T) {
			x := sr14ResolveConflict(t)
			before := sr14ResolveTree(t, x.env.HubDir)
			_, perr := ResolvePreflight(x.env, c.o)
			sr14ResolveCode(t, perr, c.code)
			doc, err := Resolve(context.Background(), x.env, c.o)
			if doc != nil {
				t.Errorf("Resolve returned a document with an error: %+v", doc)
			}
			ce := sr14ResolveCode(t, err, c.code)
			if got := contract.ExitCodeFor(ce.Code); got != contract.ExitUsage {
				t.Errorf("exit = %d, want 3", got)
			}
			if len(ce.Advice) != 0 {
				t.Errorf("advice = %#v, want none", ce.Advice)
			}
			sr14ResolveRequests(t, x.fake, map[string]int{})
			sr14ResolveSameTree(t, before, sr14ResolveTree(t, x.env.HubDir))
		})
	}
}

// A missing .contexo is not_initialized before Lock, which would otherwise
// create .contexo/.sync on the way to the lock file.
func TestResolve_MissingHubCreatesNothing(t *testing.T) {
	sr14ResolveIsolate(t)
	fake := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	fake.Seed(sr14ResolveRel, sr14ResolvePage("server"))
	root := t.TempDir()
	env := NewEnv(root, "acme-kb", "https://kb.acme.dev", Author{}, fake)
	for _, s := range []contract.ResolveStrategy{contract.ResolveOurs, contract.ResolveTheirs} {
		_, err := sr14ResolveRun(env, s, "")
		ce := sr14ResolveCode(t, err, contract.CodeNotInitialized)
		if !reflect.DeepEqual(ce.Advice, []string{"ctx init"}) {
			t.Errorf("advice = %#v, want [ctx init]", ce.Advice)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".contexo")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Resolve created %s/.contexo (stat err %v)", root, err)
	}
	sr14ResolveRequests(t, fake, map[string]int{})
}

func TestResolve_NilRemote(t *testing.T) {
	sr14ResolveIsolate(t)
	env := wp01StubEnv(t)
	_, err := sr14ResolveRun(env, contract.ResolveTheirs, "")
	ce := sr14ResolveCode(t, err, contract.CodeNotAuthenticated)
	if !reflect.DeepEqual(ce.Advice, []string{"ctx auth login"}) {
		t.Errorf("advice = %#v", ce.Advice)
	}
	if _, err := os.Stat(syncstate.SyncDir(env.HubDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Resolve touched .sync without a remote (stat err %v)", err)
	}

	fake := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env.Remote, env.RepoID = fake, ""
	_, err = sr14ResolveRun(env, contract.ResolveOurs, "")
	sr14ResolveCode(t, err, contract.CodeRepoNotConfigured)
	sr14ResolveRequests(t, fake, map[string]int{})
}

func TestResolve_IfServerShaMismatchIsStale(t *testing.T) {
	for _, s := range []contract.ResolveStrategy{contract.ResolveOurs, contract.ResolveTheirs} {
		t.Run(string(s), func(t *testing.T) {
			x := sr14ResolveConflict(t)
			before := sr14ResolveTree(t, x.env.HubDir)
			doc, err := sr14ResolveRun(x.env, s, x.s1)
			if doc != nil {
				t.Errorf("document with a stale error: %+v", doc)
			}
			ce := sr14ResolveCode(t, err, contract.CodeStale)
			if got := contract.ExitCodeFor(ce.Code); got != 6 {
				t.Errorf("exit = %d, want 6", got)
			}
			want := contract.StaleDetails{Path: sr14ResolveRel, ExpectedSHA: x.s1, ServerSHA: x.s2}
			if !reflect.DeepEqual(ce.Details, want) {
				t.Errorf("details = %#v, want %#v", ce.Details, want)
			}
			raw, err := json.Marshal(ce.Details)
			if err != nil || !strings.Contains(string(raw), `"server_sha":"`+x.s2+`"`) {
				t.Errorf("details JSON = %s (%v), want server_sha %s", raw, err, x.s2)
			}
			if len(ce.Advice) != 0 {
				t.Errorf("advice = %#v, want none", ce.Advice)
			}
			sr14ResolveSameTree(t, before, sr14ResolveTree(t, x.env.HubDir))
			sr14ResolveNoBackup(t, x.env)
		})
	}
}

// --if-server-sha matches X-Page-SHA in full and in either case; an
// abbreviation is stale, not a prefix match.
func TestResolve_IfServerShaMatchesCaseInsensitive(t *testing.T) {
	x := sr14ResolveConflict(t)
	doc, err := sr14ResolveRun(x.env, contract.ResolveOurs, strings.ToUpper(x.s2))
	if err != nil {
		t.Fatalf("Resolve with the upper-case live sha: %v", err)
	}
	if doc.BaseSHA != x.s2 {
		t.Errorf("base_sha = %q, want %q", doc.BaseSHA, x.s2)
	}

	y := sr14ResolveConflict(t)
	_, err = sr14ResolveRun(y.env, contract.ResolveTheirs, y.s2[:12])
	sr14ResolveCode(t, err, contract.CodeStale)
}

// A page the server lacks (a local new page) is page_not_found: resolve
// needs the server version, and there is no sidecar fallback.
func TestResolve_NewPageIsPageNotFound(t *testing.T) {
	for _, s := range []contract.ResolveStrategy{contract.ResolveOurs, contract.ResolveTheirs} {
		t.Run(string(s), func(t *testing.T) {
			env, fake := sr14ResolveEnv(t)
			local := sr14ResolvePage("A page nobody pushed yet.")
			sr14ResolveWrite(t, env, sr14ResolveRel, local)
			before := sr14ResolveTree(t, env.HubDir)
			_, err := sr14ResolveRun(env, s, "")
			ce := sr14ResolveCode(t, err, contract.CodePageNotFound)
			if got := contract.ExitCodeFor(ce.Code); got != 8 {
				t.Errorf("exit = %d, want 8", got)
			}
			if !strings.Contains(ce.Message, sr14ResolveRel) {
				t.Errorf("message %q does not name the path", ce.Message)
			}
			if got := sr14ResolveRead(t, sr14ResolveAbs(env, sr14ResolveRel)); string(got) != string(local) {
				t.Error("the working file changed")
			}
			sr14ResolveSameTree(t, before, sr14ResolveTree(t, env.HubDir))
			sr14ResolveNoBackup(t, env)
			sr14ResolveRequests(t, fake, map[string]int{"ReadPage": 1})
		})
	}
}

// E14: a 404 whose body says the repo is missing stays repo_not_found.
func TestResolve_RepoNotFound(t *testing.T) {
	x := sr14ResolveConflict(t)
	x.fake.FailNext("ReadPage", sync.NewHTTPError(404, []byte(`{"code":"repo_not_found","error":"repo not found"}`),
		sync.ErrPageNotFound.Error(), sync.ErrPageNotFound, false))
	before := sr14ResolveTree(t, x.env.HubDir)
	_, err := sr14ResolveRun(x.env, contract.ResolveTheirs, "")
	ce := sr14ResolveCode(t, err, contract.CodeRepoNotFound)
	if ce.HTTPStatus != 404 {
		t.Errorf("http_status = %d, want 404", ce.HTTPStatus)
	}
	sr14ResolveSameTree(t, before, sr14ResolveTree(t, x.env.HubDir))
}

// Offline is network (7): resolve has no sidecar-content fallback in v1.
func TestResolve_NetworkErrorIsNetwork(t *testing.T) {
	x := sr14ResolveConflict(t)
	x.fake.FailNext("ReadPage", &sync.NetworkError{Op: "read page", Err: errors.New("dial tcp: connection refused")})
	before := sr14ResolveTree(t, x.env.HubDir)
	_, err := sr14ResolveRun(x.env, contract.ResolveTheirs, "")
	ce := sr14ResolveCode(t, err, contract.CodeNetwork)
	if got := contract.ExitCodeFor(ce.Code); got != 7 {
		t.Errorf("exit = %d, want 7", got)
	}
	sr14ResolveSameTree(t, before, sr14ResolveTree(t, x.env.HubDir))
	sr14ResolveNoBackup(t, x.env)
}

// A 200 without X-Page-SHA is a garbled answer: server_error, nothing
// written.
func TestResolve_EmptyServerSHAIsServerError(t *testing.T) {
	x := sr14ResolveConflict(t)
	x.env.Remote = sr14ResolveNoSHA{x.fake}
	before := sr14ResolveTree(t, x.env.HubDir)
	for _, s := range []contract.ResolveStrategy{contract.ResolveOurs, contract.ResolveTheirs} {
		_, err := sr14ResolveRun(x.env, s, "")
		sr14ResolveCode(t, err, contract.CodeServerError)
	}
	sr14ResolveSameTree(t, before, sr14ResolveTree(t, x.env.HubDir))
}

// A cancelled context stops resolve before it writes anything.
func TestResolve_CancelledContextWritesNothing(t *testing.T) {
	x := sr14ResolveConflict(t)
	before := sr14ResolveTree(t, x.env.HubDir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	doc, err := Resolve(ctx, x.env, ResolveOptions{Path: sr14ResolveRel, Strategy: contract.ResolveTheirs})
	if err == nil || doc != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve with a cancelled context = %+v, %v; want context.Canceled", doc, err)
	}
	sr14ResolveSameTree(t, before, sr14ResolveTree(t, x.env.HubDir))
}

// Resolve makes exactly one ReadPage: no listing, no ?at, no pull, no push,
// and so no EnsureBase (E5).
func TestResolve_NoEnsureBaseCall(t *testing.T) {
	for _, s := range []contract.ResolveStrategy{contract.ResolveOurs, contract.ResolveTheirs} {
		t.Run(string(s), func(t *testing.T) {
			x := sr14ResolveConflict(t)
			if _, err := sr14ResolveRun(x.env, s, ""); err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			sr14ResolveRequests(t, x.fake, map[string]int{"ReadPage": 1})
		})
	}
}

// Busy keeps the lock holder as the envelope details: the chain holds no
// *contract.Error, so ui.ToContractError reads the *syncstate.BusyError.
func TestResolve_BusyExit12(t *testing.T) {
	x := sr14ResolveConflict(t)
	t.Setenv("CONTEXO_LOCK_TIMEOUT", "200ms")
	unlock, err := syncstate.Lock(x.env.HubDir, "push", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	before := sr14ResolveTree(t, x.env.HubDir)

	doc, err := sr14ResolveRun(x.env, contract.ResolveTheirs, "")
	if doc != nil {
		t.Errorf("document while busy: %+v", doc)
	}
	var coded *contract.Error
	if errors.As(err, &coded) {
		t.Errorf("busy error carries a *contract.Error (%v), which would drop the holder details", coded)
	}
	var be *syncstate.BusyError
	if !errors.As(err, &be) {
		t.Fatalf("error %v does not wrap *syncstate.BusyError", err)
	}
	ce := sr14ResolveCode(t, err, contract.CodeBusy)
	if got := contract.ExitCodeFor(ce.Code); got != 12 {
		t.Errorf("exit = %d, want 12", got)
	}
	raw, err := json.Marshal(ce.Details)
	if err != nil {
		t.Fatal(err)
	}
	var details map[string]any
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatalf("details %s: %v", raw, err)
	}
	var keys []string
	for k := range details {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"command", "host", "pid", "started_at"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("details keys = %q, want %q", keys, want)
	}
	if details["command"] != "push" {
		t.Errorf("details.command = %v, want push", details["command"])
	}
	sr14ResolveRequests(t, x.fake, map[string]int{})
	sr14ResolveSameTree(t, before, sr14ResolveTree(t, x.env.HubDir))
}

// --- --ours ---

// sr14ResolveDocBasics checks the fields every successful document shares.
func sr14ResolveDocBasics(t *testing.T, doc *contract.ResolveDoc, s contract.ResolveStrategy, sha string) {
	t.Helper()
	if doc == nil {
		t.Fatal("nil document")
	}
	if doc.Schema != contract.SchemaResolve || doc.Path != sr14ResolveRel || doc.Strategy != s || doc.BaseSHA != sha {
		t.Errorf("doc = %+v, want schema %s, path %s, strategy %s, base_sha %s",
			doc, contract.SchemaResolve, sr14ResolveRel, s, sha)
	}
	if doc.Warnings == nil {
		t.Error("warnings is nil; it must encode as []")
	}
}

// sr14ResolveAssertBase checks the valid base of rel: bases.json record at
// sha, base bytes == content (verified against the record's hash) and
// page_shas[rel] == sha.
func sr14ResolveAssertBase(t *testing.T, env Env, content []byte, sha string) {
	t.Helper()
	b, err := syncstate.OpenBases(env.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := b.Get(sr14ResolveRel)
	if !ok || rec.SHA != sha {
		t.Errorf("bases.json record = %+v (%v), want sha %s", rec, ok, sha)
	}
	got, err := b.ReadVerified(sr14ResolveRel)
	if err != nil {
		t.Fatalf("ReadVerified: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("base bytes = %q, want %q", got, content)
	}
	st, corrupt, err := sync.LoadStateOrEmpty(env.HubDir)
	if err != nil || corrupt {
		t.Fatalf("state.json: corrupt %v, err %v", corrupt, err)
	}
	if st.PageSHAs[sr14ResolveRel] != sha {
		t.Errorf("page_shas = %q, want %q", st.PageSHAs[sr14ResolveRel], sha)
	}
	if !b.Valid(sr14ResolveRel, st.PageSHAs[sr14ResolveRel]) {
		t.Error("the base is not valid for page_shas")
	}
}

// sr14ResolveAssertNoSidecar checks that rel has no conflicts.json record
// and no conflicts/<rel> file.
func sr14ResolveAssertNoSidecar(t *testing.T, env Env) {
	t.Helper()
	c, err := syncstate.OpenConflicts(env.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := c.Get(sr14ResolveRel); ok {
		t.Errorf("the sidecar is still recorded: %+v", rec)
	}
	if _, err := os.Stat(syncstate.ConflictFile(env.HubDir, sr14ResolveRel)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("conflicts/%s still exists (stat err %v)", sr14ResolveRel, err)
	}
}

func TestResolveOurs_KeepsFileRebasesBase(t *testing.T) {
	x := sr14ResolveConflict(t)
	doc, err := sr14ResolveRun(x.env, contract.ResolveOurs, x.s2)
	if err != nil {
		t.Fatalf("Resolve --ours: %v", err)
	}
	sr14ResolveDocBasics(t, doc, contract.ResolveOurs, x.s2)
	if doc.PreviousState != contract.StateConflict || doc.State != contract.StateModified {
		t.Errorf("previous_state %q -> state %q, want conflict -> modified", doc.PreviousState, doc.State)
	}
	if doc.FileWritten || doc.BackupFile != "" {
		t.Errorf("file_written %v, backup_file %q: --ours never touches the file", doc.FileWritten, doc.BackupFile)
	}
	if len(doc.Warnings) != 0 {
		t.Errorf("warnings = %+v, want none", doc.Warnings)
	}
	if got := sr14ResolveRead(t, x.abs); string(got) != string(x.local) {
		t.Errorf("the working file changed: %q", got)
	}
	sr14ResolveAssertBase(t, x.env, sr14ResolveSanitize(x.server), x.s2)
	sr14ResolveAssertNoSidecar(t, x.env)
	sr14ResolveNoBackup(t, x.env)
}

// Local content equal to the server's leaves nothing to push: unchanged.
func TestResolveOurs_EqualContentIsUnchanged(t *testing.T) {
	env, fake := sr14ResolveEnv(t)
	base := sr14ResolvePage("The base version.")
	s1 := fake.Seed(sr14ResolveRel, base)
	sr14ResolveBase(t, env, sr14ResolveRel, base, s1)
	same := sr14ResolvePage("We both wrote this.")
	sr14ResolveWrite(t, env, sr14ResolveRel, same)
	s2 := fake.Seed(sr14ResolveRel, same)

	doc, err := sr14ResolveRun(env, contract.ResolveOurs, "")
	if err != nil {
		t.Fatalf("Resolve --ours: %v", err)
	}
	sr14ResolveDocBasics(t, doc, contract.ResolveOurs, s2)
	if doc.PreviousState != contract.StateConflict || doc.State != contract.StateUnchanged {
		t.Errorf("previous_state %q -> state %q, want conflict -> unchanged", doc.PreviousState, doc.State)
	}
	sr14ResolveAssertBase(t, env, same, s2)
}

// both_added: no base at all, the server has the path.
func TestResolveOurs_BothAdded(t *testing.T) {
	env, fake := sr14ResolveEnv(t)
	local := sr14ResolvePage("I created this page.")
	sr14ResolveWrite(t, env, sr14ResolveRel, local)
	server := sr14ResolvePage("So did a teammate.")
	s1 := fake.Seed(sr14ResolveRel, server)
	sr14ResolveSidecar(t, env, syncstate.ConflictRecord{
		Path: sr14ResolveRel, ServerSHA: s1, Kind: syncstate.KindBothAdded, Source: syncstate.SourcePush,
	}, server)

	doc, err := sr14ResolveRun(env, contract.ResolveOurs, s1)
	if err != nil {
		t.Fatalf("Resolve --ours: %v", err)
	}
	sr14ResolveDocBasics(t, doc, contract.ResolveOurs, s1)
	if doc.PreviousState != contract.StateConflict || doc.State != contract.StateModified {
		t.Errorf("previous_state %q -> state %q, want conflict -> modified", doc.PreviousState, doc.State)
	}
	if got := sr14ResolveRead(t, sr14ResolveAbs(env, sr14ResolveRel)); string(got) != string(local) {
		t.Error("the working file changed")
	}
	sr14ResolveAssertBase(t, env, server, s1)
	sr14ResolveAssertNoSidecar(t, env)
}

// unverified: page_shas names a sha but there is no base record for it.
func TestResolveOurs_FromUnverified(t *testing.T) {
	env, fake := sr14ResolveEnv(t)
	s1 := fake.Seed(sr14ResolveRel, sr14ResolvePage("The old server version."))
	sr14ResolvePageSHA(t, env, sr14ResolveRel, s1)
	local := sr14ResolvePage("My edit, made by an old ctx.")
	sr14ResolveWrite(t, env, sr14ResolveRel, local)
	server := sr14ResolvePage("The current server version.")
	s2 := fake.Seed(sr14ResolveRel, server)

	doc, err := sr14ResolveRun(env, contract.ResolveOurs, "")
	if err != nil {
		t.Fatalf("Resolve --ours: %v", err)
	}
	sr14ResolveDocBasics(t, doc, contract.ResolveOurs, s2)
	if doc.PreviousState != contract.StateUnverified || doc.State != contract.StateModified {
		t.Errorf("previous_state %q -> state %q, want unverified -> modified", doc.PreviousState, doc.State)
	}
	sr14ResolveAssertBase(t, env, server, s2)
}

// After --ours the next push (parent = the new base sha, content =
// Normalize(local)) goes through without a 409.
func TestResolveOurs_ThenPushSucceeds(t *testing.T) {
	x := sr14ResolveConflict(t)
	doc, err := sr14ResolveRun(x.env, contract.ResolveOurs, "")
	if err != nil {
		t.Fatalf("Resolve --ours: %v", err)
	}
	norm, err := syncstate.Normalize(x.local)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := x.fake.PushPages(x.env.RepoID, &sync.PushRequest{Files: []sync.PushFile{{
		Path: sr14ResolveRel, Content: string(norm), ParentSHA: doc.BaseSHA,
	}}})
	if err != nil {
		t.Fatalf("PushPages: %v", err)
	}
	if len(resp.Conflicts) != 0 || len(resp.Pushed) != 1 {
		t.Fatalf("push after --ours: pushed %+v, conflicts %+v", resp.Pushed, resp.Conflicts)
	}
	if got, _ := x.fake.Content(sr14ResolveRel); string(got) != string(norm) {
		t.Errorf("server content = %q, want Normalize(local)", got)
	}
}

// sr14ResolveAssertNotResolvable runs --ours and checks the refusal:
// not_resolvable, exit 9, details {path, state, strategy}, nothing written.
func sr14ResolveAssertNotResolvable(t *testing.T, env Env, state contract.PageState) {
	t.Helper()
	before := sr14ResolveTree(t, env.HubDir)
	doc, err := sr14ResolveRun(env, contract.ResolveOurs, "")
	if doc != nil {
		t.Errorf("document with a refusal: %+v", doc)
	}
	ce := sr14ResolveCode(t, err, contract.CodeNotResolvable)
	if got := contract.ExitCodeFor(ce.Code); got != 9 {
		t.Errorf("exit = %d, want 9", got)
	}
	want := contract.NotResolvableDetails{Path: sr14ResolveRel, State: state, Strategy: contract.ResolveOurs}
	if !reflect.DeepEqual(ce.Details, want) {
		t.Errorf("details = %#v, want %#v", ce.Details, want)
	}
	if !strings.Contains(ce.Message, sr14ResolveRel) {
		t.Errorf("message %q does not name the path", ce.Message)
	}
	sr14ResolveSameTree(t, before, sr14ResolveTree(t, env.HubDir))
	sr14ResolveNoBackup(t, env)
}

// --ours on incoming would revert a teammate's change.
func TestResolveOurs_FromIncomingIsNotResolvable(t *testing.T) {
	env, fake := sr14ResolveEnv(t)
	base := sr14ResolvePage("The base version.")
	s1 := fake.Seed(sr14ResolveRel, base)
	sr14ResolveBase(t, env, sr14ResolveRel, base, s1)
	sr14ResolveWrite(t, env, sr14ResolveRel, base)
	fake.Seed(sr14ResolveRel, sr14ResolvePage("A teammate's edit."))
	sr14ResolveAssertNotResolvable(t, env, contract.StateIncoming)
}

// modified (and unchanged) are refused too: WP-1.2's Resolved pre-step
// reads details.state.
func TestResolveOurs_FromModifiedIsNotResolvable(t *testing.T) {
	env, fake := sr14ResolveEnv(t)
	base := sr14ResolvePage("The base version.")
	s1 := fake.Seed(sr14ResolveRel, base)
	sr14ResolveBase(t, env, sr14ResolveRel, base, s1)
	sr14ResolveWrite(t, env, sr14ResolveRel, sr14ResolvePage("My local edit."))
	sr14ResolveAssertNotResolvable(t, env, contract.StateModified)
}

func TestResolveOurs_OtherStatesAreNotResolvable(t *testing.T) {
	for _, c := range []struct {
		state contract.PageState
		setup func(t *testing.T, env Env, fake *syncopstest.FakeRemote)
	}{
		{contract.StateUnchanged, func(t *testing.T, env Env, fake *syncopstest.FakeRemote) {
			base := sr14ResolvePage("The base version.")
			sr14ResolveBase(t, env, sr14ResolveRel, base, fake.Seed(sr14ResolveRel, base))
			sr14ResolveWrite(t, env, sr14ResolveRel, base)
		}},
		{contract.StateInvalid, func(t *testing.T, env Env, fake *syncopstest.FakeRemote) {
			base := sr14ResolvePage("The base version.")
			sr14ResolveBase(t, env, sr14ResolveRel, base, fake.Seed(sr14ResolveRel, base))
			sr14ResolveWrite(t, env, sr14ResolveRel, []byte("no frontmatter at all\n"))
		}},
		{contract.StateDeletedLocal, func(t *testing.T, env Env, fake *syncopstest.FakeRemote) {
			base := sr14ResolvePage("The base version.")
			sr14ResolveBase(t, env, sr14ResolveRel, base, fake.Seed(sr14ResolveRel, base))
		}},
		{contract.StateIncomingNew, func(t *testing.T, env Env, fake *syncopstest.FakeRemote) {
			fake.Seed(sr14ResolveRel, sr14ResolvePage("A teammate's new page."))
		}},
	} {
		t.Run(string(c.state), func(t *testing.T) {
			env, fake := sr14ResolveEnv(t)
			c.setup(t, env, fake)
			sr14ResolveAssertNotResolvable(t, env, c.state)
		})
	}
}

// E9: the live sha equals the base, but a sidecar says the server held
// another version (an old binary re-based the base onto it). The overlay
// makes the modified page a conflict, so --ours is allowed.
func TestResolveOurs_SidecarOverlayIsConflict(t *testing.T) {
	env, fake := sr14ResolveEnv(t)
	base := sr14ResolvePage("The base version.")
	s1 := fake.Seed(sr14ResolveRel, base)
	sr14ResolveBase(t, env, sr14ResolveRel, base, s1)
	sr14ResolveWrite(t, env, sr14ResolveRel, sr14ResolvePage("My local edit."))
	other := strings.Repeat("9", 40)
	sr14ResolveSidecar(t, env, syncstate.ConflictRecord{
		Path: sr14ResolveRel, ServerSHA: other, BaseSHA: s1,
		Kind: syncstate.KindBothModified, Source: syncstate.SourcePull,
	}, sr14ResolvePage("What the server held then."))

	doc, err := sr14ResolveRun(env, contract.ResolveOurs, "")
	if err != nil {
		t.Fatalf("Resolve --ours: %v", err)
	}
	sr14ResolveDocBasics(t, doc, contract.ResolveOurs, s1)
	if doc.PreviousState != contract.StateConflict || doc.State != contract.StateModified {
		t.Errorf("previous_state %q -> state %q, want conflict -> modified", doc.PreviousState, doc.State)
	}
	sr14ResolveAssertBase(t, env, base, s1)
	sr14ResolveAssertNoSidecar(t, env)
}

// Idempotent per strategy: a repeated --ours is refused as
// modified and leaves the on-disk state of the first run.
func TestResolveOurs_SecondRunChangesNothing(t *testing.T) {
	x := sr14ResolveConflict(t)
	if _, err := sr14ResolveRun(x.env, contract.ResolveOurs, ""); err != nil {
		t.Fatalf("first --ours: %v", err)
	}
	sr14ResolveAssertNotResolvable(t, x.env, contract.StateModified)
}

// Corrupt index files are treated as empty and named in a state_unreadable
// warning.
func TestResolve_CorruptStateWarns(t *testing.T) {
	x := sr14ResolveConflict(t)
	if err := os.WriteFile(sync.StatePath(x.env.HubDir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Without page_shas the page has no base: both_added against S2.
	doc, err := sr14ResolveRun(x.env, contract.ResolveOurs, "")
	if err != nil {
		t.Fatalf("Resolve --ours: %v", err)
	}
	if doc.PreviousState != contract.StateConflict || doc.State != contract.StateModified {
		t.Errorf("previous_state %q -> state %q, want conflict -> modified", doc.PreviousState, doc.State)
	}
	if len(doc.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want one state_unreadable", doc.Warnings)
	}
	w := doc.Warnings[0]
	if w.Code != contract.WarnStateUnreadable || !strings.Contains(w.Message, "state.json") || w.Paths == nil || len(w.Paths) != 0 {
		t.Errorf("warning = %#v, want state_unreadable naming state.json with paths []", w)
	}
	sr14ResolveAssertBase(t, x.env, sr14ResolveSanitize(x.server), x.s2)
}

// --- --theirs ---

// sr14ResolveBackups lists the backup files of sr14ResolveRel.
func sr14ResolveBackups(t *testing.T, env Env) []string {
	t.Helper()
	dir := filepath.Join(syncstate.SyncDir(env.HubDir), "backup", "wiki", "concepts")
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// sr14ResolveAssertTheirs checks the end state every --theirs that took the
// server version shares: the working file is C, the base is C at sha, no
// sidecar, and the document says unchanged.
func sr14ResolveAssertTheirs(t *testing.T, env Env, doc *contract.ResolveDoc, c []byte, sha string) {
	t.Helper()
	sr14ResolveDocBasics(t, doc, contract.ResolveTheirs, sha)
	if doc.State != contract.StateUnchanged {
		t.Errorf("state = %q, want unchanged", doc.State)
	}
	if got := sr14ResolveRead(t, sr14ResolveAbs(env, sr14ResolveRel)); string(got) != string(c) {
		t.Errorf("working file = %q, want the sanitized server bytes %q", got, c)
	}
	sr14ResolveAssertBase(t, env, c, sha)
	sr14ResolveAssertNoSidecar(t, env)
}

func TestResolveTheirs_OverwritesBacksUpAndClearsSidecar(t *testing.T) {
	x := sr14ResolveConflict(t)
	doc, err := sr14ResolveRun(x.env, contract.ResolveTheirs, x.s2)
	if err != nil {
		t.Fatalf("Resolve --theirs: %v", err)
	}
	c := sr14ResolveSanitize(x.server)
	sr14ResolveAssertTheirs(t, x.env, doc, c, x.s2)
	if doc.PreviousState != contract.StateConflict || !doc.FileWritten {
		t.Errorf("previous_state %q, file_written %v; want conflict, true", doc.PreviousState, doc.FileWritten)
	}
	if doc.BackupFile != sr14ResolveBackupRel {
		t.Errorf("backup_file = %q, want %q", doc.BackupFile, sr14ResolveBackupRel)
	}
	backup := filepath.Join(x.env.HubDir, filepath.FromSlash(sr14ResolveBackupRel))
	if got := sr14ResolveRead(t, backup); string(got) != string(x.local) {
		t.Errorf("backup = %q, want the old local bytes %q", got, x.local)
	}
	if len(doc.Warnings) != 0 {
		t.Errorf("warnings = %+v, want none", doc.Warnings)
	}
	idx := sr14ResolveRead(t, filepath.Join(x.env.HubDir, "index.md"))
	if !strings.Contains(string(idx), "("+sr14ResolveRel+")") {
		t.Errorf("index.md was not regenerated with the page:\n%s", idx)
	}
}

// deleted_local: the file (and its directory) is gone, the base is valid.
func TestResolveTheirs_RestoresDeletedLocal(t *testing.T) {
	env, fake := sr14ResolveEnv(t)
	server := sr14ResolvePage("The server version.")
	s1 := fake.Seed(sr14ResolveRel, server)
	sr14ResolveBase(t, env, sr14ResolveRel, server, s1)
	if err := os.RemoveAll(filepath.Join(env.HubDir, "wiki")); err != nil {
		t.Fatal(err)
	}

	doc, err := sr14ResolveRun(env, contract.ResolveTheirs, "")
	if err != nil {
		t.Fatalf("Resolve --theirs: %v", err)
	}
	sr14ResolveAssertTheirs(t, env, doc, server, s1)
	if doc.PreviousState != contract.StateDeletedLocal || !doc.FileWritten || doc.BackupFile != "" {
		t.Errorf("previous_state %q, file_written %v, backup_file %q; want deleted_local, true, none",
			doc.PreviousState, doc.FileWritten, doc.BackupFile)
	}
	sr14ResolveNoBackup(t, env)
}

// incoming_new: no local file and no base; the server has the page.
func TestResolveTheirs_IncomingNew(t *testing.T) {
	env, fake := sr14ResolveEnv(t)
	server := sr14ResolvePage("A teammate's new page.")
	s1 := fake.Seed(sr14ResolveRel, server)

	doc, err := sr14ResolveRun(env, contract.ResolveTheirs, "")
	if err != nil {
		t.Fatalf("Resolve --theirs: %v", err)
	}
	sr14ResolveAssertTheirs(t, env, doc, server, s1)
	if doc.PreviousState != contract.StateIncomingNew || !doc.FileWritten || doc.BackupFile != "" {
		t.Errorf("previous_state %q, file_written %v, backup_file %q; want incoming_new, true, none",
			doc.PreviousState, doc.FileWritten, doc.BackupFile)
	}
	sr14ResolveNoBackup(t, env)
}

// --theirs is allowed from every state in which the server has the page.
func TestResolveTheirs_FromAnyServerState(t *testing.T) {
	for _, c := range []struct {
		state  contract.PageState
		backup bool
		setup  func(t *testing.T, env Env, fake *syncopstest.FakeRemote) []byte
	}{
		{contract.StateIncoming, true, func(t *testing.T, env Env, fake *syncopstest.FakeRemote) []byte {
			base := sr14ResolvePage("The base version.")
			sr14ResolveBase(t, env, sr14ResolveRel, base, fake.Seed(sr14ResolveRel, base))
			sr14ResolveWrite(t, env, sr14ResolveRel, base)
			fake.Seed(sr14ResolveRel, sr14ResolvePage("A teammate's edit."))
			return base
		}},
		{contract.StateModified, true, func(t *testing.T, env Env, fake *syncopstest.FakeRemote) []byte {
			base := sr14ResolvePage("The base version.")
			sr14ResolveBase(t, env, sr14ResolveRel, base, fake.Seed(sr14ResolveRel, base))
			local := sr14ResolvePage("My local edit.")
			sr14ResolveWrite(t, env, sr14ResolveRel, local)
			return local
		}},
		{contract.StateUnverified, true, func(t *testing.T, env Env, fake *syncopstest.FakeRemote) []byte {
			sr14ResolvePageSHA(t, env, sr14ResolveRel, fake.Seed(sr14ResolveRel, sr14ResolvePage("Old.")))
			local := sr14ResolvePage("My edit, made by an old ctx.")
			sr14ResolveWrite(t, env, sr14ResolveRel, local)
			fake.Seed(sr14ResolveRel, sr14ResolvePage("The current server version."))
			return local
		}},
		{contract.StateInvalid, true, func(t *testing.T, env Env, fake *syncopstest.FakeRemote) []byte {
			base := sr14ResolvePage("The base version.")
			sr14ResolveBase(t, env, sr14ResolveRel, base, fake.Seed(sr14ResolveRel, base))
			local := []byte("no frontmatter at all\n")
			sr14ResolveWrite(t, env, sr14ResolveRel, local)
			return local
		}},
	} {
		t.Run(string(c.state), func(t *testing.T) {
			env, fake := sr14ResolveEnv(t)
			old := c.setup(t, env, fake)
			server, _ := fake.Content(sr14ResolveRel)
			doc, err := sr14ResolveRun(env, contract.ResolveTheirs, "")
			if err != nil {
				t.Fatalf("Resolve --theirs: %v", err)
			}
			sr14ResolveAssertTheirs(t, env, doc, sr14ResolveSanitize(server), fake.Head())
			if doc.PreviousState != c.state || !doc.FileWritten {
				t.Errorf("previous_state %q, file_written %v; want %s, true", doc.PreviousState, doc.FileWritten, c.state)
			}
			if doc.BackupFile != sr14ResolveBackupRel {
				t.Fatalf("backup_file = %q, want %q", doc.BackupFile, sr14ResolveBackupRel)
			}
			backup := filepath.Join(env.HubDir, filepath.FromSlash(doc.BackupFile))
			if got := sr14ResolveRead(t, backup); string(got) != string(old) {
				t.Errorf("backup = %q, want %q", got, old)
			}
		})
	}
}

// From unchanged --theirs is a successful no-op: nothing is written.
func TestResolveTheirs_FromUnchangedIsNoop(t *testing.T) {
	env, fake := sr14ResolveEnv(t)
	base := sr14ResolvePage("The base version.")
	s1 := fake.Seed(sr14ResolveRel, base)
	sr14ResolveBase(t, env, sr14ResolveRel, base, s1)
	sr14ResolveWrite(t, env, sr14ResolveRel, base)
	before := sr14ResolveTree(t, env.HubDir)

	doc, err := sr14ResolveRun(env, contract.ResolveTheirs, s1)
	if err != nil {
		t.Fatalf("Resolve --theirs: %v", err)
	}
	sr14ResolveDocBasics(t, doc, contract.ResolveTheirs, s1)
	if doc.PreviousState != contract.StateUnchanged || doc.State != contract.StateUnchanged {
		t.Errorf("previous_state %q -> state %q, want unchanged -> unchanged", doc.PreviousState, doc.State)
	}
	if doc.FileWritten || doc.BackupFile != "" {
		t.Errorf("file_written %v, backup_file %q; want a no-op", doc.FileWritten, doc.BackupFile)
	}
	sr14ResolveSameTree(t, before, sr14ResolveTree(t, env.HubDir))
	sr14ResolveNoBackup(t, env)
}

// A sidecar lingering on an unchanged page is still cleared (E10):
// offline status would otherwise keep reading it as incoming. Nothing else
// is written.
func TestResolveTheirs_NoopClearsLingeringSidecar(t *testing.T) {
	env, fake := sr14ResolveEnv(t)
	base := sr14ResolvePage("The base version.")
	s1 := fake.Seed(sr14ResolveRel, base)
	sr14ResolveBase(t, env, sr14ResolveRel, base, s1)
	sr14ResolveWrite(t, env, sr14ResolveRel, base)
	sr14ResolveSidecar(t, env, syncstate.ConflictRecord{
		Path: sr14ResolveRel, ServerSHA: strings.Repeat("7", 40), BaseSHA: s1,
		Kind: syncstate.KindBothModified, Source: syncstate.SourcePull,
	}, sr14ResolvePage("What the server held then."))
	before := sr14ResolveTree(t, env.HubDir)

	doc, err := sr14ResolveRun(env, contract.ResolveTheirs, "")
	if err != nil {
		t.Fatalf("Resolve --theirs: %v", err)
	}
	if doc.PreviousState != contract.StateUnchanged || doc.FileWritten || doc.BackupFile != "" {
		t.Errorf("doc = %+v, want an unchanged no-op", doc)
	}
	sr14ResolveAssertNoSidecar(t, env)
	after := sr14ResolveTree(t, env.HubDir)
	for _, k := range []string{".sync/conflicts.json", ".sync/conflicts/" + sr14ResolveRel} {
		delete(before, k)
		delete(after, k)
	}
	sr14ResolveSameTree(t, before, after)
}

// A lost --theirs response retried by the extension is a no-op the second
// time: no second backup (Review Focus 5). The clock moves on, so a second
// backup would get a name of its own.
func TestResolveTheirs_SecondRunIsNoop(t *testing.T) {
	x := sr14ResolveConflict(t)
	if _, err := sr14ResolveRun(x.env, contract.ResolveTheirs, x.s2); err != nil {
		t.Fatalf("first --theirs: %v", err)
	}
	x.env.Now = func() time.Time { return sr14ResolveNow.Add(time.Hour) }
	doc, err := sr14ResolveRun(x.env, contract.ResolveTheirs, x.s2)
	if err != nil {
		t.Fatalf("second --theirs: %v", err)
	}
	if doc.PreviousState != contract.StateUnchanged || doc.State != contract.StateUnchanged {
		t.Errorf("previous_state %q -> state %q, want unchanged -> unchanged", doc.PreviousState, doc.State)
	}
	if doc.FileWritten || doc.BackupFile != "" {
		t.Errorf("file_written %v, backup_file %q; want a no-op", doc.FileWritten, doc.BackupFile)
	}
	if got := sr14ResolveBackups(t, x.env); len(got) != 1 {
		t.Errorf("backups = %q, want exactly one", got)
	}
}

// The backup holds the bytes on disk right before the overwrite, not the
// scan's copy: a file edited after classification is what gets saved
// (Review Focus 2).
func TestResolveTheirs_BackupHoldsCurrentBytes(t *testing.T) {
	x := sr14ResolveConflict(t)
	later := sr14ResolvePage("An edit saved while resolve was running.")
	resolveBeforeWrite = func() {
		if err := os.WriteFile(x.abs, later, 0o644); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { resolveBeforeWrite = nil })

	doc, err := sr14ResolveRun(x.env, contract.ResolveTheirs, "")
	if err != nil {
		t.Fatalf("Resolve --theirs: %v", err)
	}
	backup := filepath.Join(x.env.HubDir, filepath.FromSlash(doc.BackupFile))
	if got := sr14ResolveRead(t, backup); string(got) != string(later) {
		t.Errorf("backup = %q, want the bytes written just before the overwrite %q", got, later)
	}
	sr14ResolveAssertTheirs(t, x.env, doc, sr14ResolveSanitize(x.server), x.s2)
}

// The backup is written before the working file, and a failed working-file
// write leaves the base, the sidecar and state.json exactly as they were.
func TestResolveTheirs_WriteOrder(t *testing.T) {
	t.Run("backup first", func(t *testing.T) {
		x := sr14ResolveConflict(t)
		var calls []string
		resolveWriteFile = func(path string, data []byte, perm os.FileMode) error {
			calls = append(calls, path)
			return fsutil.WriteFileAtomic(path, data, perm)
		}
		t.Cleanup(func() { resolveWriteFile = fsutil.WriteFileAtomic })
		if _, err := sr14ResolveRun(x.env, contract.ResolveTheirs, ""); err != nil {
			t.Fatalf("Resolve --theirs: %v", err)
		}
		want := []string{syncstate.BackupFile(x.env.HubDir, sr14ResolveRel, sr14ResolveNow), x.abs}
		if !reflect.DeepEqual(calls, want) {
			t.Errorf("writes = %q, want %q", calls, want)
		}
	})

	t.Run("working file write fails", func(t *testing.T) {
		x := sr14ResolveConflict(t)
		boom := errors.New("disk full")
		resolveWriteFile = func(path string, data []byte, perm os.FileMode) error {
			if path == x.abs {
				return boom
			}
			return fsutil.WriteFileAtomic(path, data, perm)
		}
		t.Cleanup(func() { resolveWriteFile = fsutil.WriteFileAtomic })
		before := sr14ResolveTree(t, x.env.HubDir)

		doc, err := sr14ResolveRun(x.env, contract.ResolveTheirs, "")
		if doc != nil || !errors.Is(err, boom) {
			t.Fatalf("Resolve = %+v, %v; want the write error", doc, err)
		}
		after := sr14ResolveTree(t, x.env.HubDir)
		if got := after[sr14ResolveBackupRel]; got != string(x.local) {
			t.Errorf("backup = %q, want the old local bytes", got)
		}
		delete(after, sr14ResolveBackupRel)
		sr14ResolveSameTree(t, before, after)
		c, err := syncstate.OpenConflicts(x.env.HubDir)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := c.Get(sr14ResolveRel); !ok {
			t.Error("the sidecar was cleared although the file was not written")
		}
	})
}

// A symlinked directory inside the hub must not redirect the write outside
// it: invalid_path, nothing written anywhere.
func TestResolveTheirs_SymlinkedDirOutsideHubRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory symlinks need privileges on windows")
	}
	env, fake := sr14ResolveEnv(t)
	server := sr14ResolvePage("The server version.")
	s1 := fake.Seed(sr14ResolveRel, server)
	sr14ResolveBase(t, env, sr14ResolveRel, server, s1)
	outside := t.TempDir()
	concepts := filepath.Join(env.HubDir, "wiki", "concepts")
	if err := os.RemoveAll(concepts); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(concepts), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, concepts); err != nil {
		t.Fatal(err)
	}
	before := sr14ResolveTree(t, env.HubDir)

	doc, err := sr14ResolveRun(env, contract.ResolveTheirs, "")
	if doc != nil {
		t.Errorf("document although the write was refused: %+v", doc)
	}
	sr14ResolveCode(t, err, contract.CodeInvalidPath)
	ents, err := os.ReadDir(outside)
	if err != nil || len(ents) != 0 {
		t.Errorf("files written outside the hub: %v (%v)", ents, err)
	}
	sr14ResolveSameTree(t, before, sr14ResolveTree(t, env.HubDir))
	sr14ResolveNoBackup(t, env)
}

// Local raw bytes already equal to C: no backup and no write, but the base
// and page_shas move to the server version.
func TestResolveTheirs_EqualRawBytesNoWrite(t *testing.T) {
	env, fake := sr14ResolveEnv(t)
	sr14ResolvePageSHA(t, env, sr14ResolveRel, fake.Seed(sr14ResolveRel, sr14ResolvePage("Old.")))
	server := sr14ResolvePage("The current server version.")
	s2 := fake.Seed(sr14ResolveRel, server)
	sr14ResolveWrite(t, env, sr14ResolveRel, server)

	doc, err := sr14ResolveRun(env, contract.ResolveTheirs, "")
	if err != nil {
		t.Fatalf("Resolve --theirs: %v", err)
	}
	sr14ResolveAssertTheirs(t, env, doc, server, s2)
	if doc.PreviousState != contract.StateUnverified || doc.FileWritten || doc.BackupFile != "" {
		t.Errorf("previous_state %q, file_written %v, backup_file %q; want unverified, false, none",
			doc.PreviousState, doc.FileWritten, doc.BackupFile)
	}
	sr14ResolveNoBackup(t, env)
}

// A failed index regeneration is a reindex_failed warning, not a failure.
func TestResolveTheirs_ReindexFailureWarns(t *testing.T) {
	x := sr14ResolveConflict(t)
	// A directory where index.md belongs makes indexer.Generate fail.
	if err := os.Mkdir(filepath.Join(x.env.HubDir, "index.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc, err := sr14ResolveRun(x.env, contract.ResolveTheirs, "")
	if err != nil {
		t.Fatalf("Resolve --theirs: %v", err)
	}
	sr14ResolveAssertTheirs(t, x.env, doc, sr14ResolveSanitize(x.server), x.s2)
	if len(doc.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want one reindex_failed", doc.Warnings)
	}
	if w := doc.Warnings[0]; w.Code != contract.WarnReindexFailed || w.Message == "" ||
		!reflect.DeepEqual(w.Paths, []string{sr14ResolveRel}) {
		t.Errorf("warning = %#v, want reindex_failed for [%s]", w, sr14ResolveRel)
	}
}
