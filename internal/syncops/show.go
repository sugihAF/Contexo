package syncops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"unicode"

	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// Owner: WP-1.4. Fields may be added, never renamed or removed.

// ShowOptions configures Show. At is "base", "server" or a hex sha.
type ShowOptions struct {
	Path, At string
}

// Show returns the raw bytes `ctx show` writes to stdout.
func Show(ctx context.Context, env Env, o ShowOptions) ([]byte, error) {
	return nil, NotImplemented("show")
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
