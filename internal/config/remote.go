package config

import (
	"net/url"
	"strings"
)

// Remote is the resolved answer to "which server, with which token, for
// which repo". ResolveRemote is the ONLY source of a server URL and token for
// the CLI and the MCP tools: nothing else may hand cfg.ServerURL and a token
// to sync.NewClient.
type Remote struct {
	// ServerURL is where requests go. It is "" when unresolved: credentials
	// with a token but no server_url that cannot borrow config.server_url.
	ServerURL string
	// URLSource is "credentials", "config" or "default", or "" when
	// ServerURL is unresolved.
	URLSource string
	// ConfiguredServerURL is .contexo/config.json server_url, as written.
	ConfiguredServerURL string
	// Mismatch is true when config.server_url is set and names another
	// server than the one requests go to. ctx warns and keeps using
	// ServerURL; it never sends the credentials to the config URL.
	Mismatch bool
	RepoID   string
	// DashboardURL is config.dashboard_url, else the hosted dashboard when
	// ServerURL is the hosted API, else "" (self-hosted with none configured).
	DashboardURL string
	// Token is Creds.Bearer(), or "" when there is nothing to send.
	Token string
	Creds *Credentials
	// CredsSource is "project", "global", or "" when no credentials file
	// exists.
	CredsSource string
}

// RemoteError is a remote-resolution failure with a stable code. config does
// not import the contract package, so the code travels as a plain string
// through ErrorCode(), which ui.ToContractError reads by duck typing.
type RemoteError struct {
	Code   string
	Msg    string
	Advice []string
}

func (e *RemoteError) Error() string     { return e.Msg }
func (e *RemoteError) ErrorCode() string { return e.Code }

// ErrorAdvice returns the commands that fix the failure; never nil.
func (e *RemoteError) ErrorAdvice() []string {
	if e.Advice == nil {
		return []string{}
	}
	return e.Advice
}

const (
	codeNotAuthenticated  = "not_authenticated"
	codeServerUnresolved  = "server_unresolved"
	codeRepoNotConfigured = "repo_not_configured"
)

// ResolveRemote applies the server-URL rule (contract §5, D5):
//  1. Credentials come from the project file, else the global one.
//  2. Credentials with a server_url use it (url_source credentials).
//  3. Project credentials without one use config.server_url (config), or
//     stay unresolved when there is none.
//  4. Global credentials without one stay unresolved. A global token is
//     never sent to a URL taken from the workspace.
//  5. No credentials or an empty token: report config.server_url, else
//     DefaultServerURL (config or default), and send nothing.
//
// The error is only for an unreadable or corrupt config or credentials file.
// Missing auth, server or repo is reported by RequireAuth and RequireRepo.
func ResolveRemote(root string) (*Remote, error) {
	cfg, err := Load(root)
	if err != nil {
		return nil, err
	}
	creds, source, err := LoadCredentialsWithSource(root)
	if err != nil {
		return nil, err
	}

	r := &Remote{
		ConfiguredServerURL: cfg.ServerURL,
		RepoID:              cfg.RepoID,
		Token:               creds.Bearer(),
		Creds:               creds,
		CredsSource:         source,
	}
	switch {
	case r.Token == "":
		if cfg.ServerURL != "" {
			r.ServerURL, r.URLSource = cfg.ServerURL, "config"
		} else {
			r.ServerURL, r.URLSource = DefaultServerURL, "default"
		}
	case creds.ServerURL != "":
		r.ServerURL, r.URLSource = creds.ServerURL, "credentials"
	case source == "project" && cfg.ServerURL != "":
		r.ServerURL, r.URLSource = cfg.ServerURL, "config"
	}
	r.Mismatch = cfg.ServerURL != "" && r.ServerURL != "" && !SameServer(cfg.ServerURL, r.ServerURL)

	switch {
	case cfg.DashboardURL != "":
		r.DashboardURL = strings.TrimRight(cfg.DashboardURL, "/")
	case SameServer(r.ServerURL, DefaultServerURL):
		r.DashboardURL = DefaultDashboardURL
	}
	return r, nil
}

// RequireAuth reports why nothing can be sent, in E22 order: no token first,
// then an unresolved server.
func (r *Remote) RequireAuth() error {
	if r.Token == "" {
		return &RemoteError{Code: codeNotAuthenticated, Msg: "not authenticated",
			Advice: []string{"ctx auth login"}}
	}
	if r.ServerURL == "" {
		return &RemoteError{Code: codeServerUnresolved,
			Msg:    "server URL unresolved: the credentials have no server_url",
			Advice: []string{"ctx auth login --server <url>"}}
	}
	return nil
}

// RequireRepo reports a missing repo_id.
func (r *Remote) RequireRepo() error {
	if r.RepoID == "" {
		return &RemoteError{Code: codeRepoNotConfigured, Msg: "repo not configured",
			Advice: []string{"ctx remote set-repo <id>"}}
	}
	return nil
}

// TokenKind is the token's shape: pat, session, legacy or invite_key, or ""
// when there is no token.
func (r *Remote) TokenKind() string {
	return tokenKind(r.Creds.Bearer())
}

// SameServer reports whether a and b name the same server: scheme and host
// (port included) compared case-insensitively, trailing '/' ignored, path
// compared exactly. An empty side is never the same server.
func SameServer(a, b string) bool {
	a = strings.TrimRight(strings.TrimSpace(a), "/")
	b = strings.TrimRight(strings.TrimSpace(b), "/")
	if a == "" || b == "" {
		return false
	}
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil || ua.Scheme == "" || ua.Host == "" || ub.Scheme == "" || ub.Host == "" {
		return strings.EqualFold(a, b)
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) &&
		strings.EqualFold(ua.Host, ub.Host) &&
		strings.TrimRight(ua.Path, "/") == strings.TrimRight(ub.Path, "/")
}
