package syncops

// Owner: WP-1.4. Show engine tests, plus the path rule and engine guards
// that Show and Resolve share (show.go). Tests here may use
// syncopstest.FakeRemote together with unexported seams (syncopstest never
// imports syncops).

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncops/syncopstest"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/testkit"
	"github.com/sugihAF/contexo/internal/ui"
)

// sr14ShowCode asserts err is a *contract.Error with code and returns it.
func sr14ShowCode(t *testing.T, err error, code contract.ErrorCode) *contract.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("got a nil error, want %s", code)
	}
	ce := wp01ContractError(t, err)
	if ce.Code != code {
		t.Fatalf("code = %q, want %q (message %q)", ce.Code, code, ce.Message)
	}
	return ce
}

func TestShowValidatePath(t *testing.T) {
	env := wp01StubEnv(t)
	for _, c := range []struct{ arg, want string }{
		{"wiki/concepts/a.md", "wiki/concepts/a.md"},
		{".contexo/wiki/concepts/a.md", "wiki/concepts/a.md"},
		{filepath.Join(env.HubDir, "wiki", "concepts", "a.md"), "wiki/concepts/a.md"},
		{"raw/sessions/s-1.md", "raw/sessions/s-1.md"},
	} {
		got, err := showValidatePath(env, c.arg)
		if err != nil || got != c.want {
			t.Errorf("showValidatePath(%q) = %q, %v; want %q", c.arg, got, err, c.want)
		}
	}
	for _, arg := range []string{
		"../x.md", ".sync/state.json", ".sync/base/wiki/concepts/a.md", "credentials.json",
		"config.json", "wiki/.hidden.md", "wiki/-x.md", "wiki/a.txt", "wiki//x.md",
		"/etc/passwd.md", "", "index.md", "tags.md", ".contexo/index.md",
		"wiki/a\u0085b.md", "wiki/a\x1bb.md", "wiki/a\x7fb.md", "wiki\\..\\..\\x.md",
	} {
		got, err := showValidatePath(env, arg)
		if got != "" {
			t.Errorf("showValidatePath(%q) returned path %q with an error", arg, got)
		}
		ce := sr14ShowCode(t, err, contract.CodeInvalidPath)
		if len(ce.Advice) != 0 {
			t.Errorf("%q: advice = %#v, want none", arg, ce.Advice)
		}
		var pe *syncstate.PathError
		if !errors.As(err, &pe) {
			t.Errorf("%q: error %v does not wrap a *syncstate.PathError", arg, err)
		}
	}
}

func TestShowIsSHA(t *testing.T) {
	for s, want := range map[string]bool{
		"abcdef1": true,
		"abcdef":  false,
		"0123456789abcdef0123456789abcdef01234567":                          true,
		"0123456789ABCDEF0123456789ABCDEF01234567":                          true,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef":  true,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0": false,
		"":        false,
		"base":    false,
		"BASE":    false,
		"server":  false,
		"abcdefg": false,
		"HEAD~1":  false,
	} {
		if got := showIsSHA(s); got != want {
			t.Errorf("showIsSHA(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestShowGuards(t *testing.T) {
	missing := NewEnv(t.TempDir(), "acme-kb", "https://kb.acme.dev", Author{}, nil)
	ce := sr14ShowCode(t, showRequireHub(missing), contract.CodeNotInitialized)
	if !reflect.DeepEqual(ce.Advice, []string{"ctx init"}) {
		t.Errorf("not_initialized advice = %#v", ce.Advice)
	}
	if _, err := os.Stat(missing.HubDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("showRequireHub created the hub: %v", err)
	}
	if err := showRequireHub(wp01StubEnv(t)); err != nil {
		t.Errorf("showRequireHub on a hub: %v", err)
	}

	offline := wp01StubEnv(t)
	ce = sr14ShowCode(t, showRequireRemote(offline, "show"), contract.CodeNotAuthenticated)
	if !reflect.DeepEqual(ce.Advice, []string{"ctx auth login"}) {
		t.Errorf("not_authenticated advice = %#v", ce.Advice)
	}

	noRepo := wp01StubEnv(t)
	noRepo.Remote = syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	noRepo.RepoID = ""
	ce = sr14ShowCode(t, showRequireRemote(noRepo, "resolve"), contract.CodeRepoNotConfigured)
	if !reflect.DeepEqual(ce.Advice, []string{"ctx remote set-repo <id>"}) {
		t.Errorf("repo_not_configured advice = %#v", ce.Advice)
	}

	online := wp01StubEnv(t)
	online.Remote = syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	if err := showRequireRemote(online, "show"); err != nil {
		t.Errorf("showRequireRemote online: %v", err)
	}
}

// sr14ShowRel is the page every Show test reads.
const sr14ShowRel = "wiki/concepts/a.md"

// sr14ShowUnknownSHA is a full sha the fake never commits (its shas are
// sha1("fake:<n>")).
const sr14ShowUnknownSHA = "0123456789abcdef0123456789abcdef01234567"

// sr14ShowEnv is an online Env over an initialised temp hub, wired to a
// fake server with options o. HOME and USERPROFILE are isolated so nothing
// can reach the developer's real credentials.
func sr14ShowEnv(t *testing.T, o syncopstest.FakeOptions) (Env, *syncopstest.FakeRemote) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	env := wp01StubEnv(t)
	fake := syncopstest.NewFakeRemote(o)
	env.Remote = fake
	return env, fake
}

// sr14ShowPage is a valid concept page with slug "a" and the given body.
func sr14ShowPage(body string) []byte {
	return testkit.PageBytes(testkit.PageSpec{Slug: "a", Body: body})
}

// sr14ShowPutBase stores content as the base of rel for baseSHA and records
// stateSHA as rel's page_shas entry ("" leaves state.json alone), as a push
// or pull would.
func sr14ShowPutBase(t *testing.T, env Env, rel string, content []byte, baseSHA, stateSHA string) {
	t.Helper()
	b, err := syncstate.OpenBases(env.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Put(rel, content, baseSHA, env.Now()); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if stateSHA != "" {
		sr14ShowSetPageSHA(t, env, rel, stateSHA)
	}
}

// sr14ShowSetPageSHA sets rel's state.json page_shas entry to sha.
func sr14ShowSetPageSHA(t *testing.T, env Env, rel, sha string) {
	t.Helper()
	st, _, err := sync.LoadStateOrEmpty(env.HubDir)
	if err != nil {
		t.Fatal(err)
	}
	if st.PageSHAs == nil {
		st.PageSHAs = map[string]string{}
	}
	st.PageSHAs[rel] = sha
	if err := sync.SaveState(env.HubDir, st); err != nil {
		t.Fatal(err)
	}
}

// sr14ShowContract is ui.ToContractError(err), the error ctx reports, which
// must carry code. It also covers the errors Show passes through %w-wrapped
// (a *sync.HTTPError or *sync.NetworkError rather than a *contract.Error).
func sr14ShowContract(t *testing.T, err error, code contract.ErrorCode) *contract.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("got a nil error, want %s", code)
	}
	ce := ui.ToContractError(err)
	if ce.Code != code {
		t.Fatalf("code = %q, want %q (message %q)", ce.Code, code, ce.Message)
	}
	return ce
}

// sr14ShowTree snapshots everything under dir: per slash path its mode,
// mtime and, for a regular file, its bytes. Any write, create, remove or
// atomic rewrite (also of a lock file that is removed again: its directory's
// mtime moves) changes the snapshot.
func sr14ShowTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := info.Mode().String() + " " + info.ModTime().String()
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			entry += " " + string(b)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// sr14ShowAssertRequests asserts the fake saw exactly want (method -> calls).
func sr14ShowAssertRequests(t *testing.T, fake *syncopstest.FakeRemote, want map[string]int) {
	t.Helper()
	if got := fake.Requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestShow_Base_Server_Sha(t *testing.T) {
	env, fake := sr14ShowEnv(t, syncopstest.FakeOptions{})
	ctx := context.Background()

	old := sr14ShowPage("Old body\u200b with a zero-width space.")
	s1 := fake.Seed(sr14ShowRel, old)
	head := sr14ShowPage("New body \x1b[31mred\x1b[0m and \u202egnirts\u202c.")
	s2 := fake.Seed(sr14ShowRel, head)

	// The base is stored CRLF with a BOM and must come back byte for byte.
	base := append([]byte("\xEF\xBB\xBF"), bytes.ReplaceAll(old, []byte("\n"), []byte("\r\n"))...)
	sr14ShowPutBase(t, env, sr14ShowRel, base, s1, s1)

	got, err := Show(ctx, env, ShowOptions{Path: sr14ShowRel, At: "base"})
	if err != nil {
		t.Fatalf("show --at base: %v", err)
	}
	if !bytes.Equal(got, base) {
		t.Errorf("--at base = %q, want the base bytes verbatim %q", got, base)
	}
	sr14ShowAssertRequests(t, fake, map[string]int{})

	// A path argument is normalised like every page argument.
	got, err = Show(ctx, env, ShowOptions{Path: ".contexo/" + sr14ShowRel, At: "base"})
	if err != nil || !bytes.Equal(got, base) {
		t.Errorf("--at base via .contexo/ = %q, %v", got, err)
	}

	wantHead := []byte(schema.SanitizeContent(string(head)))
	if bytes.Equal(wantHead, head) {
		t.Fatal("fixture: HEAD has nothing to sanitize")
	}
	got, err = Show(ctx, env, ShowOptions{Path: sr14ShowRel, At: "server"})
	if err != nil {
		t.Fatalf("show --at server: %v", err)
	}
	if !bytes.Equal(got, wantHead) {
		t.Errorf("--at server = %q, want SanitizeContent(HEAD) %q", got, wantHead)
	}
	if bytes.ContainsRune(got, '\x1b') || bytes.ContainsRune(got, '\u202e') {
		t.Errorf("--at server kept an ESC or a bidi override: %q", got)
	}
	sr14ShowAssertRequests(t, fake, map[string]int{"ReadPage": 1})

	wantOld := []byte(schema.SanitizeContent(string(old)))
	if bytes.Equal(wantOld, old) {
		t.Fatal("fixture: the old version has nothing to sanitize")
	}
	for _, c := range []struct {
		name, at string
		want     []byte
	}{
		{"first commit", s1, wantOld},
		{"head commit", s2, wantHead},
		{"abbreviated", s1[:7], wantOld},
		{"upper case", strings.ToUpper(s1), wantOld},
	} {
		got, err := Show(ctx, env, ShowOptions{Path: sr14ShowRel, At: c.at})
		if err != nil {
			t.Errorf("%s: show --at %s: %v", c.name, c.at, err)
			continue
		}
		if !bytes.Equal(got, c.want) {
			t.Errorf("%s: --at %s = %q, want %q", c.name, c.at, got, c.want)
		}
	}
	sr14ShowAssertRequests(t, fake, map[string]int{"ReadPage": 1, "ReadPageAt": 4})
}

func TestShow_NoBaseIsBaseUnavailable(t *testing.T) {
	const sha = "1111111111111111111111111111111111111111"
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, env Env)
	}{
		{"empty hub", func(*testing.T, Env) {}},
		{"synced sha without a base record", func(t *testing.T, env Env) {
			sr14ShowSetPageSHA(t, env, sr14ShowRel, sha)
		}},
		{"base record without a synced sha", func(t *testing.T, env Env) {
			sr14ShowPutBase(t, env, sr14ShowRel, sr14ShowPage("x"), sha, "")
		}},
		{"base of another page only", func(t *testing.T, env Env) {
			sr14ShowPutBase(t, env, "wiki/concepts/b.md", sr14ShowPage("x"), sha, sha)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, fake := sr14ShowEnv(t, syncopstest.FakeOptions{})
			c.setup(t, env)
			b, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: "base"})
			if b != nil {
				t.Errorf("returned %q with an error", b)
			}
			ce := sr14ShowCode(t, err, contract.CodeBaseUnavailable)
			if got := contract.ExitCodeFor(ce.Code); got != contract.ExitNotFound {
				t.Errorf("exit = %d, want 8", got)
			}
			if !reflect.DeepEqual(ce.Advice, []string{"ctx repair"}) {
				t.Errorf("advice = %#v, want [ctx repair]", ce.Advice)
			}
			if !strings.Contains(ce.Message, sr14ShowRel) {
				t.Errorf("message %q does not name the path", ce.Message)
			}
			if !errors.Is(err, syncstate.ErrBaseUnavailable) {
				t.Errorf("error %v does not wrap syncstate.ErrBaseUnavailable", err)
			}
			sr14ShowAssertRequests(t, fake, map[string]int{})
		})
	}
}

func TestShow_StaleBaseIsBaseUnavailable(t *testing.T) {
	const s1 = "1111111111111111111111111111111111111111"
	const s2 = "2222222222222222222222222222222222222222"
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, env Env)
	}{
		{"page_shas names another sha", func(t *testing.T, env Env) {
			sr14ShowPutBase(t, env, sr14ShowRel, sr14ShowPage("base"), s1, s2)
		}},
		{"base bytes tampered", func(t *testing.T, env Env) {
			sr14ShowPutBase(t, env, sr14ShowRel, sr14ShowPage("base"), s1, s1)
			if err := os.WriteFile(syncstate.BaseFile(env.HubDir, sr14ShowRel), sr14ShowPage("tampered"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"base file missing", func(t *testing.T, env Env) {
			sr14ShowPutBase(t, env, sr14ShowRel, sr14ShowPage("base"), s1, s1)
			if err := os.Remove(syncstate.BaseFile(env.HubDir, sr14ShowRel)); err != nil {
				t.Fatal(err)
			}
		}},
		{"corrupt bases.json", func(t *testing.T, env Env) {
			sr14ShowPutBase(t, env, sr14ShowRel, sr14ShowPage("base"), s1, s1)
			if err := os.WriteFile(syncstate.BasesPath(env.HubDir), []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"corrupt state.json", func(t *testing.T, env Env) {
			sr14ShowPutBase(t, env, sr14ShowRel, sr14ShowPage("base"), s1, s1)
			if err := os.WriteFile(sync.StatePath(env.HubDir), []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, _ := sr14ShowEnv(t, syncopstest.FakeOptions{})
			c.setup(t, env)
			b, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: "base"})
			if b != nil {
				t.Errorf("returned %q with an error", b)
			}
			ce := sr14ShowCode(t, err, contract.CodeBaseUnavailable)
			if !reflect.DeepEqual(ce.Advice, []string{"ctx repair"}) {
				t.Errorf("advice = %#v, want [ctx repair]", ce.Advice)
			}
			if got := contract.ExitCodeFor(ce.Code); got != contract.ExitNotFound {
				t.Errorf("exit = %d, want 8", got)
			}
			if !errors.Is(err, syncstate.ErrBaseUnavailable) {
				t.Errorf("error %v does not wrap syncstate.ErrBaseUnavailable", err)
			}
		})
	}
}

func TestShow_OldServerIsServerUnsupported(t *testing.T) {
	env, fake := sr14ShowEnv(t, syncopstest.FakeOptions{OldServer: true})
	s1 := fake.Seed(sr14ShowRel, sr14ShowPage("v1"))
	fake.Seed(sr14ShowRel, sr14ShowPage("v2 at HEAD"))

	b, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: s1})
	if b != nil {
		t.Errorf("an old server's HEAD came back as history: %q", b)
	}
	ce := sr14ShowCode(t, err, contract.CodeServerUnsupported)
	if got := contract.ExitCodeFor(ce.Code); got != contract.ExitUnsupported {
		t.Errorf("exit = %d, want 10", got)
	}
	want := contract.ServerUnsupportedDetails{Missing: []contract.ServerCapability{contract.SrvPagesAt}}
	if !reflect.DeepEqual(ce.Details, want) {
		t.Errorf("details = %#v, want %#v", ce.Details, want)
	}
	// The old server answered 200 without the X-Contexo-At echo: no HTTP
	// error status caused this, so the envelope carries no http_status.
	if ce.HTTPStatus != 0 {
		t.Errorf("http_status = %d, want none", ce.HTTPStatus)
	}
	if !errors.Is(err, sync.ErrServerUnsupported) {
		t.Errorf("error %v does not match sync.ErrServerUnsupported", err)
	}
	if got := ui.ToContractError(err); got.Code != contract.CodeServerUnsupported || !reflect.DeepEqual(got.Details, want) {
		t.Errorf("ToContractError = %+v", got)
	}

	// HEAD reads still work against the old server.
	if b, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: "server"}); err != nil || len(b) == 0 {
		t.Errorf("--at server on an old server = %q, %v", b, err)
	}
}

func TestShow_UnknownShaIsShaNotFound(t *testing.T) {
	env, fake := sr14ShowEnv(t, syncopstest.FakeOptions{})
	sOther := fake.Seed("wiki/concepts/b.md", sr14ShowPage("b"))
	fake.Seed(sr14ShowRel, sr14ShowPage("a"))

	for _, c := range []struct{ name, at string }{
		{"never committed", sr14ShowUnknownSHA},
		{"path absent at that commit", sOther},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: c.at})
			if b != nil {
				t.Errorf("returned %q with an error", b)
			}
			ce := sr14ShowCode(t, err, contract.CodeSHANotFound)
			if got := contract.ExitCodeFor(ce.Code); got != contract.ExitNotFound {
				t.Errorf("exit = %d, want 8", got)
			}
			if ce.HTTPStatus != 404 {
				t.Errorf("http_status = %d, want 404", ce.HTTPStatus)
			}
			if !errors.Is(err, sync.ErrSHANotFound) {
				t.Errorf("error %v does not match sync.ErrSHANotFound", err)
			}
			if !strings.Contains(ce.Message, c.at) || !strings.Contains(ce.Message, sr14ShowRel) {
				t.Errorf("message %q does not name the path and the sha", ce.Message)
			}
		})
	}
}

func TestShow_RepoNotFoundNotMaskedAsSha(t *testing.T) {
	for _, body := range []string{
		`{"error":"repo not found"}`,
		`{"code":"repo_not_found","error":"repo not found"}`,
	} {
		t.Run(body, func(t *testing.T) {
			env, fake := sr14ShowEnv(t, syncopstest.FakeOptions{})
			fake.FailNext("ReadPageAt", sync.NewHTTPError(404, []byte(body), "x", nil, true))
			_, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: sr14ShowUnknownSHA})
			ce := sr14ShowCode(t, err, contract.CodeRepoNotFound)
			if got := contract.ExitCodeFor(ce.Code); got != contract.ExitNotFound {
				t.Errorf("exit = %d, want 8", got)
			}
			if ce.HTTPStatus != 404 {
				t.Errorf("http_status = %d, want 404", ce.HTTPStatus)
			}
			if errors.Is(err, sync.ErrSHANotFound) {
				t.Error("repo_not_found was masked as sha_not_found")
			}
			if c := ui.ToContractError(err).Code; c != contract.CodeRepoNotFound {
				t.Errorf("ToContractError code = %q", c)
			}

			fake.FailNext("ReadPage", sync.NewHTTPError(404, []byte(body), sync.ErrPageNotFound.Error(), sync.ErrPageNotFound, false))
			_, err = Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: "server"})
			ce = sr14ShowContract(t, err, contract.CodeRepoNotFound)
			if ce.HTTPStatus != 404 {
				t.Errorf("--at server http_status = %d, want 404", ce.HTTPStatus)
			}
		})
	}
}

func TestShow_ServerPageNotFound(t *testing.T) {
	env, fake := sr14ShowEnv(t, syncopstest.FakeOptions{})
	fake.Seed("wiki/concepts/b.md", sr14ShowPage("b"))
	b, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: "server"})
	if b != nil {
		t.Errorf("returned %q with an error", b)
	}
	ce := sr14ShowContract(t, err, contract.CodePageNotFound)
	if got := contract.ExitCodeFor(ce.Code); got != contract.ExitNotFound {
		t.Errorf("exit = %d, want 8", got)
	}
	if ce.HTTPStatus != 404 {
		t.Errorf("http_status = %d, want 404", ce.HTTPStatus)
	}
	if !errors.Is(err, sync.ErrPageNotFound) {
		t.Errorf("error %v does not match sync.ErrPageNotFound", err)
	}
	if !strings.Contains(ce.Message, sr14ShowRel) {
		t.Errorf("message %q does not name the path", ce.Message)
	}
}

func TestShow_BadAtIsUsage(t *testing.T) {
	env, fake := sr14ShowEnv(t, syncopstest.FakeOptions{})
	for _, at := range []string{
		"HEAD", "abc123", "", "Base", "SERVER", " base", "abcdefg", "HEAD~1",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0",
	} {
		b, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: at})
		if b != nil {
			t.Errorf("--at %q returned %q with an error", at, b)
		}
		ce := sr14ShowCode(t, err, contract.CodeUsage)
		if !strings.Contains(ce.Message, "--at must be base, server or a 7-64 character hex sha") {
			t.Errorf("--at %q: message %q", at, ce.Message)
		}
		if _, _, err := ShowPreflight(env, ShowOptions{Path: sr14ShowRel, At: at}); err == nil {
			t.Errorf("ShowPreflight accepted --at %q", at)
		}
	}

	// The path is checked first: a bad path wins over a bad --at.
	_, err := Show(context.Background(), env, ShowOptions{Path: "../x.md", At: "HEAD"})
	sr14ShowCode(t, err, contract.CodeInvalidPath)
	_, _, err = ShowPreflight(env, ShowOptions{Path: "../x.md", At: "HEAD"})
	sr14ShowCode(t, err, contract.CodeInvalidPath)
	sr14ShowAssertRequests(t, fake, map[string]int{})
}

func TestShowPreflight(t *testing.T) {
	env := wp01StubEnv(t) // offline: the preflight does no I/O and needs no Remote
	for _, c := range []struct {
		path, at, rel string
		online        bool
	}{
		{sr14ShowRel, "base", sr14ShowRel, false},
		{".contexo/" + sr14ShowRel, "server", sr14ShowRel, true},
		{filepath.Join(env.HubDir, "wiki", "concepts", "a.md"), "abcdef1", sr14ShowRel, true},
		{sr14ShowRel, "0123456789ABCDEF0123456789ABCDEF01234567", sr14ShowRel, true},
	} {
		rel, online, err := ShowPreflight(env, ShowOptions{Path: c.path, At: c.at})
		if err != nil || rel != c.rel || online != c.online {
			t.Errorf("ShowPreflight(%q, %q) = %q, %v, %v; want %q, %v", c.path, c.at, rel, online, err, c.rel, c.online)
		}
	}
	rel, online, err := ShowPreflight(env, ShowOptions{Path: "credentials.json", At: "server"})
	if rel != "" || online {
		t.Errorf("a rejected path returned %q, online %v", rel, online)
	}
	sr14ShowCode(t, err, contract.CodeInvalidPath)
}

func TestShow_NetworkErrorIsNetwork(t *testing.T) {
	for _, c := range []struct{ method, at string }{
		{"ReadPage", "server"},
		{"ReadPageAt", sr14ShowUnknownSHA},
	} {
		t.Run(c.method, func(t *testing.T) {
			env, fake := sr14ShowEnv(t, syncopstest.FakeOptions{})
			fake.FailNext(c.method, &sync.NetworkError{Op: "read page", Err: errors.New("dial")})
			b, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: c.at})
			if b != nil {
				t.Errorf("returned %q with an error", b)
			}
			ce := sr14ShowContract(t, err, contract.CodeNetwork)
			if got := contract.ExitCodeFor(ce.Code); got != contract.ExitNetwork {
				t.Errorf("exit = %d, want 7", got)
			}
			var ne *sync.NetworkError
			if !errors.As(err, &ne) {
				t.Errorf("error %v does not wrap the *sync.NetworkError", err)
			}
		})
	}
}

func TestShow_WritesNothing(t *testing.T) {
	env, fake := sr14ShowEnv(t, syncopstest.FakeOptions{})
	s1 := fake.Seed(sr14ShowRel, sr14ShowPage("v1"))
	s2 := fake.Seed(sr14ShowRel, sr14ShowPage("v2"))
	sr14ShowPutBase(t, env, sr14ShowRel, sr14ShowPage("v2"), s2, s2)
	local := filepath.Join(env.HubDir, filepath.FromSlash(sr14ShowRel))
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, sr14ShowPage("local edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := sr14ShowTree(t, env.HubDir)

	for _, c := range []struct {
		path, at string
		ok       bool
	}{
		{sr14ShowRel, "base", true},
		{sr14ShowRel, "server", true},
		{sr14ShowRel, s1, true},
		{"wiki/concepts/missing.md", "base", false},
		{"wiki/concepts/missing.md", "server", false},
		{sr14ShowRel, sr14ShowUnknownSHA, false},
	} {
		_, err := Show(context.Background(), env, ShowOptions{Path: c.path, At: c.at})
		if (err == nil) != c.ok {
			t.Errorf("show %s --at %s: err = %v, want ok %v", c.path, c.at, err, c.ok)
		}
	}
	if after := sr14ShowTree(t, env.HubDir); !reflect.DeepEqual(after, before) {
		t.Errorf("show changed the hub:\nbefore %v\nafter  %v", before, after)
	}
	if _, err := os.Lstat(syncstate.LockPath(env.HubDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("show left a lock file: %v", err)
	}
	sr14ShowAssertRequests(t, fake, map[string]int{"ReadPage": 2, "ReadPageAt": 2})

	// A hub that never synced keeps having no .sync directory.
	fresh, freshFake := sr14ShowEnv(t, syncopstest.FakeOptions{})
	s := freshFake.Seed(sr14ShowRel, sr14ShowPage("v1"))
	for _, at := range []string{"base", "server", s} {
		_, _ = Show(context.Background(), fresh, ShowOptions{Path: sr14ShowRel, At: at})
	}
	if _, err := os.Lstat(syncstate.SyncDir(fresh.HubDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("show created %s: %v", syncstate.SyncDir(fresh.HubDir), err)
	}
	sr14ShowAssertRequests(t, freshFake, map[string]int{"ReadPage": 1, "ReadPageAt": 1})
}

func TestShow_BaseNeedsNoRemote(t *testing.T) {
	env := wp01StubEnv(t)
	env.RepoID = ""
	base := sr14ShowPage("base")
	const sha = "1111111111111111111111111111111111111111"
	sr14ShowPutBase(t, env, sr14ShowRel, base, sha, sha)
	got, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: "base"})
	if err != nil || !bytes.Equal(got, base) {
		t.Fatalf("--at base offline = %q, %v; want the base", got, err)
	}

	// The network variants need a Remote, then a repo id.
	for _, at := range []string{"server", sha} {
		_, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: at})
		ce := sr14ShowCode(t, err, contract.CodeNotAuthenticated)
		if !reflect.DeepEqual(ce.Advice, []string{"ctx auth login"}) {
			t.Errorf("--at %s: advice = %#v", at, ce.Advice)
		}
	}
	fake := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env.Remote = fake
	for _, at := range []string{"server", sha} {
		_, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: at})
		sr14ShowCode(t, err, contract.CodeRepoNotConfigured)
	}
	sr14ShowAssertRequests(t, fake, map[string]int{})
}

// A cancelled context stops the network variants before any request (the
// Remote methods take no context); the offline base read still answers.
func TestShow_CancelledContextMakesNoRequest(t *testing.T) {
	env, fake := sr14ShowEnv(t, syncopstest.FakeOptions{})
	s1 := fake.Seed(sr14ShowRel, sr14ShowPage("v1"))
	sr14ShowPutBase(t, env, sr14ShowRel, sr14ShowPage("v1"), s1, s1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, at := range []string{"server", s1} {
		b, err := Show(ctx, env, ShowOptions{Path: sr14ShowRel, At: at})
		if b != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("--at %s with a cancelled context = %q, %v; want context.Canceled", at, b, err)
		}
	}
	sr14ShowAssertRequests(t, fake, map[string]int{})
	if _, err := Show(ctx, env, ShowOptions{Path: sr14ShowRel, At: "base"}); err != nil {
		t.Errorf("--at base with a cancelled context: %v", err)
	}
}

func TestShow_MissingHubIsNotInitialized(t *testing.T) {
	fake := syncopstest.NewFakeRemote(syncopstest.FakeOptions{})
	env := NewEnv(t.TempDir(), "acme-kb", "https://kb.acme.dev", Author{}, fake)
	for _, at := range []string{"base", "server", sr14ShowUnknownSHA} {
		_, err := Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: at})
		ce := sr14ShowCode(t, err, contract.CodeNotInitialized)
		if !reflect.DeepEqual(ce.Advice, []string{"ctx init"}) {
			t.Errorf("--at %s: advice = %#v", at, ce.Advice)
		}
	}
	// The argument rules come first, also without a hub.
	_, err := Show(context.Background(), env, ShowOptions{Path: "../x.md", At: "base"})
	sr14ShowCode(t, err, contract.CodeInvalidPath)
	_, err = Show(context.Background(), env, ShowOptions{Path: sr14ShowRel, At: "HEAD"})
	sr14ShowCode(t, err, contract.CodeUsage)

	if _, err := os.Lstat(env.HubDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("show created the hub: %v", err)
	}
	sr14ShowAssertRequests(t, fake, map[string]int{})
}
