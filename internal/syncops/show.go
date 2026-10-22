package syncops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"unicode"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// Owner: WP-1.4. Fields may be added, never renamed or removed.

// ShowOptions configures Show. At is "base", "server" or a hex sha.
type ShowOptions struct {
	Path, At string
}

// Show returns the raw bytes `ctx show` writes to stdout: for "base" the
// verified base snapshot verbatim (offline, CRLF and BOM kept), for "server"
// and a sha the server's bytes through schema.SanitizeContent, exactly what
// pull would write (E6). The argument rules come first, then the hub. Show
// takes no lock and writes nothing, so it is safe beside a running push or
// pull and never creates .contexo/.sync.
func Show(ctx context.Context, env Env, o ShowOptions) ([]byte, error) {
	rel, online, err := ShowPreflight(env, o)
	if err != nil {
		return nil, err
	}
	if err := showRequireHub(env); err != nil {
		return nil, err
	}
	if !online {
		return showBase(env, rel)
	}
	if err := showRequireRemote(env, "show"); err != nil {
		return nil, err
	}
	// The Remote methods take no context: a caller that already gave up
	// (an MCP request cancelled) must not still cost a request.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("show %s --at %s: %w", rel, o.At, err)
	}
	if o.At == "server" {
		return showServer(env, rel)
	}
	return showAt(env, rel, o.At)
}

// showBase returns rel's base bytes when the base is valid for the last
// synced sha (bases.json sha == page_shas[rel]) and still hashes to its
// recorded hash. A corrupt state.json or bases.json reads as empty, so it
// is base_unavailable like a missing record: the extension then diffs
// against the server or an empty side, and ctx repair rebuilds the base.
func showBase(env Env, rel string) ([]byte, error) {
	op := "show " + rel + " --at base"
	st, _, err := sync.LoadStateOrEmpty(env.HubDir)
	if err != nil {
		return nil, Wrap(err, contract.CodeInternal, op)
	}
	bases, err := syncstate.OpenBases(env.HubDir)
	if err != nil {
		return nil, Wrap(err, contract.CodeInternal, op)
	}
	if !bases.Valid(rel, st.PageSHAs[rel]) {
		return nil, showBaseUnavailable(op, fmt.Errorf(
			"%w: %s has no base snapshot for its last synced sha", syncstate.ErrBaseUnavailable, rel))
	}
	data, err := bases.ReadVerified(rel)
	if err != nil {
		// A *PathError cannot occur for a path that passed showValidatePath;
		// it is checked first anyway, so a bad path never reads as a
		// missing base.
		var pe *syncstate.PathError
		if errors.As(err, &pe) {
			return nil, Wrap(err, contract.CodeInvalidPath, op)
		}
		if errors.Is(err, syncstate.ErrBaseUnavailable) {
			return nil, showBaseUnavailable(op, err)
		}
		return nil, Wrap(err, contract.CodeInternal, op)
	}
	return data, nil
}

// showBaseUnavailable is base_unavailable (exit 8) for op, wrapping cause
// (which wraps syncstate.ErrBaseUnavailable). ctx repair is the way to a
// valid base again.
func showBaseUnavailable(op string, cause error) error {
	return Wrap(cause, contract.CodeBaseUnavailable, op, "ctx repair")
}

// showServer returns the server's HEAD bytes of rel, sanitized. Its errors
// pass through %w-wrapped: the *sync.HTTPError already carries the E14 code
// (page_not_found, repo_not_found from the body code or "repo not found",
// server_unsupported for a missing route, auth_rejected ...) and its status,
// and a *sync.NetworkError reads as network.
func showServer(env Env, rel string) ([]byte, error) {
	b, _, err := env.Remote.ReadPage(env.RepoID, rel)
	if err != nil {
		return nil, fmt.Errorf("show %s --at server: %w", rel, err)
	}
	return []byte(schema.SanitizeContent(string(b))), nil
}

// showAt returns rel as of commit sha, sanitized. The client trusts only
// the X-Contexo-At echo (an old server ignores ?at and serves HEAD with
// 200), so ErrServerUnsupported never returns HEAD as history. The codes are
// checked in E14's order: repo_not_found is never masked as sha_not_found.
func showAt(env Env, rel, sha string) ([]byte, error) {
	b, _, err := env.Remote.ReadPageAt(env.RepoID, rel, sha)
	if err == nil {
		return []byte(schema.SanitizeContent(string(b))), nil
	}
	op := "show " + rel + " --at " + sha
	var e *contract.Error
	switch {
	case errors.Is(err, sync.ErrRepoNotFound):
		e = Wrap(err, contract.CodeRepoNotFound, op)
	case errors.Is(err, sync.ErrServerUnsupported):
		e = Wrap(err, contract.CodeServerUnsupported, op)
		e.Details = contract.ServerUnsupportedDetails{
			Missing: []contract.ServerCapability{contract.SrvPagesAt},
		}
	case errors.Is(err, sync.ErrSHANotFound):
		e = Wrap(err, contract.CodeSHANotFound, op)
	default:
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	e.HTTPStatus = showHTTPStatus(err)
	return nil, e
}

// showHTTPStatus is the status of the HTTP error response behind err, or 0.
// An old server's ?at answer is a 200 without the echo: no error response
// caused that failure, so the envelope carries no http_status for it.
func showHTTPStatus(err error) int {
	var he *sync.HTTPError
	if errors.As(err, &he) && he.Status >= 400 {
		return he.Status
	}
	return 0
}

// ShowPreflight applies show's argument rules without any I/O: the path
// rule first (invalid_path), then At (usage). It returns the contract path
// and whether At needs the server ("server" or a sha). The CLI calls it
// before it opens the remote, so a bad argument is reported as such even
// without credentials; Show calls it again for MCP callers.
func ShowPreflight(env Env, o ShowOptions) (rel string, online bool, err error) {
	rel, err = showValidatePath(env, o.Path)
	if err != nil {
		return "", false, err
	}
	switch {
	case o.At == "base":
		return rel, false, nil
	case o.At == "server", showIsSHA(o.At):
		return rel, true, nil
	}
	return "", false, New(contract.CodeUsage,
		fmt.Sprintf("show: --at must be base, server or a 7-64 character hex sha, got %q", o.At))
}

// showValidatePath is the page-path rule of show and resolve. arg may be a
// contract path, a ".contexo/"-prefixed path or an absolute path inside the
// hub (syncstate.NormalizeRelPath, which also applies ValidateRelPath). On
// top of that it rejects every Unicode control character (ValidateRelPath
// stops at C0 and DEL) and the root index.md and tags.md, which ctx
// generates and never syncs. A rejected path is invalid_path and wraps the
// *syncstate.PathError, so .sync/*, credentials.json and config.json can
// never be named.
func showValidatePath(env Env, arg string) (string, error) {
	rel, err := syncstate.NormalizeRelPath(env.Root, env.HubDir, arg)
	if err == nil {
		err = showPathExtra(rel)
	}
	if err != nil {
		return "", Wrap(err, contract.CodeInvalidPath, "")
	}
	return rel, nil
}

// showPathExtra applies the rules ValidateRelPath leaves out to a path that
// passed it.
func showPathExtra(rel string) error {
	for _, r := range rel {
		if unicode.IsControl(r) {
			return &syncstate.PathError{Path: rel, Reason: "contains a control character"}
		}
	}
	if rel == "index.md" || rel == "tags.md" {
		return &syncstate.PathError{Path: rel, Reason: "the root index.md and tags.md are generated, not pages"}
	}
	return nil
}

// showIsSHA reports whether s can name a commit for `ctx show --at` and
// `ctx resolve --if-server-sha`: 7 to 64 hex digits, either case.
func showIsSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case '0' <= c && c <= '9', 'a' <= c && c <= 'f', 'A' <= c && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// showRequireHub fails with not_initialized (RequireHub's error) unless
// env.HubDir is a directory. Engines check it before their first write:
// Lock would otherwise create a missing .contexo/ on the way to .sync/lock.
func showRequireHub(env Env) error {
	fi, err := os.Stat(env.HubDir)
	if err == nil && fi.IsDir() {
		return nil
	}
	if err == nil {
		err = errors.New("not a directory")
	}
	e := New(contract.CodeNotInitialized, fmt.Sprintf("no .contexo/ directory in %s", env.Root), "ctx init")
	e.Err = err
	return e
}

// showRequireRemote fails when env cannot reach the server for op: no
// Remote is not_authenticated and no repo id is repo_not_configured, with
// the advice config.ResolveRemote gives for the same conditions.
func showRequireRemote(env Env, op string) error {
	if !env.Online() {
		return New(contract.CodeNotAuthenticated, op+": not authenticated", "ctx auth login")
	}
	if env.RepoID == "" {
		return New(contract.CodeRepoNotConfigured, op+": no repo_id configured", "ctx remote set-repo <id>")
	}
	return nil
}
