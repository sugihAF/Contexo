package syncops

// Owner: WP-1.4. Show engine tests, plus the path rule and engine guards
// that Show and Resolve share (show.go). Tests here may use
// syncopstest.FakeRemote together with unexported seams (syncopstest never
// imports syncops).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncops/syncopstest"
	"github.com/sugihAF/contexo/internal/syncstate"
)

func TestShowStubNotImplemented(t *testing.T) {
	env := wp01StubEnv(t)
	for _, at := range []string{"base", "server", "2222222222222222222222222222222222222222"} {
		b, err := Show(context.Background(), env, ShowOptions{Path: "wiki/concepts/a.md", At: at})
		wp01AssertNotImplemented(t, err, "show")
		if b != nil {
			t.Fatalf("Show(--at %s) returned %d bytes with the stub error", at, len(b))
		}
	}
}

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
