package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

const (
	wp03HostA = "https://a.example.test"
	wp03HostB = "https://b.example.test"
)

// wp03Project returns a fresh project root with HOME pointed at a temp dir,
// writing config.json only when cfg is non-nil.
func wp03Project(t *testing.T, cfg *Config) string {
	t.Helper()
	withHomeDir(t)
	root := t.TempDir()
	if cfg != nil {
		if err := Save(root, cfg); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func wp03Resolve(t *testing.T, root string) *Remote {
	t.Helper()
	r, err := ResolveRemote(root)
	if err != nil {
		t.Fatalf("ResolveRemote: %v", err)
	}
	return r
}

func wp03Code(t *testing.T, err error) string {
	t.Helper()
	var re *RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("error %v (%T) is not a *RemoteError", err, err)
	}
	return re.ErrorCode()
}

func TestResolveRemoteGlobalCredsNeverUseWorkspaceURL(t *testing.T) {
	root := wp03Project(t, &Config{Version: 1, ServerURL: wp03HostB, RepoID: "r1"})
	if err := SaveGlobalCredentials(&Credentials{Token: "ctxp_tok", ServerURL: wp03HostA}); err != nil {
		t.Fatal(err)
	}

	r := wp03Resolve(t, root)
	if r.ServerURL != wp03HostA {
		t.Errorf("ServerURL = %q, want the credentials' server %q", r.ServerURL, wp03HostA)
	}
	if r.URLSource != "credentials" {
		t.Errorf("URLSource = %q, want credentials", r.URLSource)
	}
	if !r.Mismatch {
		t.Error("Mismatch = false, want true (config points at another server)")
	}
	if r.CredsSource != "global" {
		t.Errorf("CredsSource = %q, want global", r.CredsSource)
	}
	if r.ConfiguredServerURL != wp03HostB {
		t.Errorf("ConfiguredServerURL = %q, want %q", r.ConfiguredServerURL, wp03HostB)
	}
	if r.Token != "ctxp_tok" || r.RepoID != "r1" {
		t.Errorf("Token/RepoID = %q/%q", r.Token, r.RepoID)
	}
	if err := r.RequireAuth(); err != nil {
		t.Errorf("RequireAuth: %v", err)
	}
	if err := r.RequireRepo(); err != nil {
		t.Errorf("RequireRepo: %v", err)
	}
}

func TestResolveRemoteProjectCredsWithoutServerFallsBackToConfig(t *testing.T) {
	root := wp03Project(t, &Config{Version: 1, ServerURL: wp03HostB})
	if err := SaveCredentials(root, &Credentials{Token: "ctxp_tok"}); err != nil {
		t.Fatal(err)
	}

	r := wp03Resolve(t, root)
	if r.ServerURL != wp03HostB || r.URLSource != "config" {
		t.Errorf("ServerURL/URLSource = %q/%q, want %q/config", r.ServerURL, r.URLSource, wp03HostB)
	}
	if r.CredsSource != "project" || r.Mismatch {
		t.Errorf("CredsSource/Mismatch = %q/%v, want project/false", r.CredsSource, r.Mismatch)
	}
	if err := r.RequireAuth(); err != nil {
		t.Errorf("RequireAuth: %v", err)
	}
}

func TestResolveRemoteProjectCredsNoServerNoConfigIsUnresolved(t *testing.T) {
	root := wp03Project(t, nil)
	if err := SaveCredentials(root, &Credentials{Token: "ctxp_tok"}); err != nil {
		t.Fatal(err)
	}

	r := wp03Resolve(t, root)
	if r.ServerURL != "" || r.URLSource != "" {
		t.Errorf("ServerURL/URLSource = %q/%q, want unresolved", r.ServerURL, r.URLSource)
	}
	if got := wp03Code(t, r.RequireAuth()); got != "server_unresolved" {
		t.Errorf("RequireAuth code = %q, want server_unresolved", got)
	}
}

func TestResolveRemoteGlobalCredsWithoutServerIsUnresolved(t *testing.T) {
	root := wp03Project(t, &Config{Version: 1, ServerURL: wp03HostB, RepoID: "r1"})
	if err := SaveGlobalCredentials(&Credentials{Token: "ctxp_tok"}); err != nil {
		t.Fatal(err)
	}

	r := wp03Resolve(t, root)
	if r.ServerURL != "" {
		t.Fatalf("ServerURL = %q: a global token must never be sent to the workspace URL", r.ServerURL)
	}
	if r.URLSource != "" || r.CredsSource != "global" {
		t.Errorf("URLSource/CredsSource = %q/%q, want \"\"/global", r.URLSource, r.CredsSource)
	}
	if r.Mismatch {
		t.Error("Mismatch = true; an unresolved server is reported by RequireAuth, not as a mismatch")
	}
	err := r.RequireAuth()
	if got := wp03Code(t, err); got != "server_unresolved" {
		t.Errorf("RequireAuth code = %q, want server_unresolved", got)
	}
	var re *RemoteError
	errors.As(err, &re)
	if want := []string{"ctx auth login --server <url>"}; !reflect.DeepEqual(re.ErrorAdvice(), want) {
		t.Errorf("advice = %q, want %q", re.ErrorAdvice(), want)
	}
}

func TestResolveRemoteNoCreds(t *testing.T) {
	t.Run("config URL", func(t *testing.T) {
		root := wp03Project(t, &Config{Version: 1, ServerURL: wp03HostB, RepoID: "r1"})
		r := wp03Resolve(t, root)
		if r.ServerURL != wp03HostB || r.URLSource != "config" {
			t.Errorf("ServerURL/URLSource = %q/%q, want %q/config", r.ServerURL, r.URLSource, wp03HostB)
		}
		if r.Token != "" || r.Creds != nil || r.CredsSource != "" {
			t.Errorf("Token/Creds/CredsSource = %q/%v/%q, want empty", r.Token, r.Creds, r.CredsSource)
		}
		if got := wp03Code(t, r.RequireAuth()); got != "not_authenticated" {
			t.Errorf("RequireAuth code = %q, want not_authenticated", got)
		}
	})
	t.Run("default URL", func(t *testing.T) {
		root := wp03Project(t, nil)
		r := wp03Resolve(t, root)
		if r.ServerURL != DefaultServerURL || r.URLSource != "default" {
			t.Errorf("ServerURL/URLSource = %q/%q, want %q/default", r.ServerURL, r.URLSource, DefaultServerURL)
		}
		if got := wp03Code(t, r.RequireAuth()); got != "not_authenticated" {
			t.Errorf("RequireAuth code = %q, want not_authenticated", got)
		}
	})
}

// An empty-token project file shadows a valid global file (LoadCredentials
// keeps its project-first behaviour) and reads as not authenticated.
func TestResolveRemoteEmptyTokenIsUnauthenticated(t *testing.T) {
	root := wp03Project(t, &Config{Version: 1, ServerURL: wp03HostB})
	if err := SaveGlobalCredentials(&Credentials{Token: "ctxp_global", ServerURL: wp03HostA}); err != nil {
		t.Fatal(err)
	}
	if err := SaveCredentials(root, &Credentials{ServerURL: wp03HostA}); err != nil {
		t.Fatal(err)
	}

	r := wp03Resolve(t, root)
	if r.Token != "" {
		t.Errorf("Token = %q, want empty", r.Token)
	}
	if r.ServerURL != wp03HostB || r.URLSource != "config" {
		t.Errorf("ServerURL/URLSource = %q/%q, want %q/config (rule 5)", r.ServerURL, r.URLSource, wp03HostB)
	}
	if r.CredsSource != "project" || r.Creds == nil {
		t.Errorf("CredsSource = %q (creds %v), want project", r.CredsSource, r.Creds)
	}
	if r.TokenKind() != "" {
		t.Errorf("TokenKind = %q, want empty", r.TokenKind())
	}
	if got := wp03Code(t, r.RequireAuth()); got != "not_authenticated" {
		t.Errorf("RequireAuth code = %q, want not_authenticated", got)
	}
}

func TestRequireAuthPrecedence(t *testing.T) {
	// No token wins over an unresolved URL (E22).
	r := &Remote{}
	if got := wp03Code(t, r.RequireAuth()); got != "not_authenticated" {
		t.Errorf("no token + no URL: code = %q, want not_authenticated", got)
	}
	// Resolved, but no repo: auth passes, repo fails.
	r = &Remote{ServerURL: wp03HostA, Token: "ctxp_tok"}
	if err := r.RequireAuth(); err != nil {
		t.Errorf("RequireAuth: %v", err)
	}
	if got := wp03Code(t, r.RequireRepo()); got != "repo_not_configured" {
		t.Errorf("RequireRepo code = %q, want repo_not_configured", got)
	}
	r.RepoID = "r1"
	if err := r.RequireRepo(); err != nil {
		t.Errorf("RequireRepo with a repo: %v", err)
	}
}

func TestSameServerNormalisation(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"https://api.contexo.live", "https://api.contexo.live", true},
		{"https://API.Contexo.Live", "https://api.contexo.live/", true},
		{"HTTPS://api.contexo.live//", "https://api.contexo.live", true},
		{"https://api.contexo.live", "https://api.contexo.live:8443", false},
		{"https://api.contexo.live:443", "https://api.contexo.live", false},
		{"http://api.contexo.live", "https://api.contexo.live", false},
		{"https://a.example.test", "https://b.example.test", false},
		{"https://h.example.test/ctx", "https://h.example.test/other", false},
		{"https://h.example.test/ctx/", "https://h.example.test/ctx", true},
		{"", "", false},
		{"https://api.contexo.live", "", false},
	}
	for _, c := range cases {
		if got := SameServer(c.a, c.b); got != c.want {
			t.Errorf("SameServer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestDashboardURLRules(t *testing.T) {
	withCreds := func(t *testing.T, cfg *Config, server string) *Remote {
		t.Helper()
		root := wp03Project(t, cfg)
		if err := SaveGlobalCredentials(&Credentials{Token: "ctxp_tok", ServerURL: server}); err != nil {
			t.Fatal(err)
		}
		return wp03Resolve(t, root)
	}
	t.Run("config value", func(t *testing.T) {
		r := withCreds(t, &Config{Version: 1, DashboardURL: "https://dash.example.test/"}, wp03HostA)
		if r.DashboardURL != "https://dash.example.test" {
			t.Errorf("DashboardURL = %q, want the config value without the trailing slash", r.DashboardURL)
		}
	})
	t.Run("hosted default", func(t *testing.T) {
		r := withCreds(t, nil, "https://API.contexo.live/")
		if r.DashboardURL != DefaultDashboardURL {
			t.Errorf("DashboardURL = %q, want %q", r.DashboardURL, DefaultDashboardURL)
		}
	})
	t.Run("self-hosted without config", func(t *testing.T) {
		r := withCreds(t, nil, wp03HostA)
		if r.DashboardURL != "" {
			t.Errorf("DashboardURL = %q, want \"\" (E23)", r.DashboardURL)
		}
	})
}

func TestTokenKind(t *testing.T) {
	cases := map[string]string{
		"ctxp_abc":  "pat",
		"aaa.bbb.c": "session",
		"ctxi_abc":  "invite_key",
		"legacy123": "legacy",
		"":          "",
	}
	for tok, want := range cases {
		r := &Remote{Token: tok, Creds: &Credentials{Token: tok}}
		if got := r.TokenKind(); got != want {
			t.Errorf("TokenKind(%q) = %q, want %q", tok, got, want)
		}
	}
	if got := (&Remote{}).TokenKind(); got != "" {
		t.Errorf("TokenKind without creds = %q, want empty", got)
	}
	// Kind() keeps its display strings.
	if got := (&Credentials{Token: "ctxp_abc"}).Kind(); got != "personal access token" {
		t.Errorf("Kind() = %q", got)
	}
}

func TestRemoteErrorCarriesCodeAndAdvice(t *testing.T) {
	cases := []struct {
		err    error
		code   string
		advice []string
	}{
		{(&Remote{}).RequireAuth(), "not_authenticated", []string{"ctx auth login"}},
		{(&Remote{Token: "t"}).RequireAuth(), "server_unresolved", []string{"ctx auth login --server <url>"}},
		{(&Remote{Token: "t", ServerURL: wp03HostA}).RequireRepo(), "repo_not_configured", []string{"ctx remote set-repo <id>"}},
	}
	for _, c := range cases {
		wrapped := fmt.Errorf("push: %w", c.err)
		var re *RemoteError
		if !errors.As(wrapped, &re) {
			t.Fatalf("errors.As(%v) found no *RemoteError", wrapped)
		}
		if re.ErrorCode() != c.code || re.Code != c.code {
			t.Errorf("code = %q/%q, want %q", re.ErrorCode(), re.Code, c.code)
		}
		if !reflect.DeepEqual(re.ErrorAdvice(), c.advice) {
			t.Errorf("%s advice = %q, want %q", c.code, re.ErrorAdvice(), c.advice)
		}
		if re.Error() == "" || re.Error() != re.Msg {
			t.Errorf("%s Error() = %q, Msg = %q", c.code, re.Error(), re.Msg)
		}
		// Duck-typed interop with ui.ToContractError (WP-0.1).
		var coded interface {
			ErrorCode() string
			ErrorAdvice() []string
		}
		if !errors.As(wrapped, &coded) || coded.ErrorCode() != c.code {
			t.Errorf("%s: duck-typed ErrorCode not reachable through %%w", c.code)
		}
	}
	if (&RemoteError{Code: "x"}).ErrorAdvice() == nil {
		t.Error("ErrorAdvice() must never be nil")
	}
}

func TestResolveRemoteCorruptFilesError(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		root := wp03Project(t, nil)
		if err := os.MkdirAll(ContexoDirPath(root), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ContexoConfigPath(root), []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		if r, err := ResolveRemote(root); err == nil {
			t.Fatalf("want an error for a corrupt config, got %+v", r)
		}
	})
	t.Run("credentials", func(t *testing.T) {
		root := wp03Project(t, nil)
		if err := os.MkdirAll(ContexoDirPath(root), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(CredentialsPath(root), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if r, err := ResolveRemote(root); err == nil {
			t.Fatalf("want an error for corrupt credentials, got %+v", r)
		}
	})
}

func TestLoadCredentialsWithSource(t *testing.T) {
	root := wp03Project(t, nil)
	if c, src, err := LoadCredentialsWithSource(root); err != nil || c != nil || src != "" {
		t.Fatalf("none: got %v %q %v", c, src, err)
	}
	if err := SaveGlobalCredentials(&Credentials{Token: "ctxp_g"}); err != nil {
		t.Fatal(err)
	}
	if c, src, err := LoadCredentialsWithSource(root); err != nil || c.Bearer() != "ctxp_g" || src != "global" {
		t.Fatalf("global: got %v %q %v", c, src, err)
	}
	if err := SaveCredentials(root, &Credentials{Token: "ctxp_p"}); err != nil {
		t.Fatal(err)
	}
	if c, src, err := LoadCredentialsWithSource(root); err != nil || c.Bearer() != "ctxp_p" || src != "project" {
		t.Fatalf("project: got %v %q %v", c, src, err)
	}
}
