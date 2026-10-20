package cli

import (
	"errors"
	"fmt"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/ui"
)

// openRemote is the ONLY place in package cli that builds a sync client. The
// server URL and token come from config.ResolveRemote, so a global token is
// never sent to a URL taken from the workspace config. name prefixes the
// errors so each command's messages read naturally.
//
// The returned remote is nil only when ResolveRemote itself failed (an
// unreadable or corrupt config or credentials file). When RequireAuth or
// RequireRepo fails, it is returned with the error and a nil client, so
// callers can still report what was resolved. Those errors carry their advice
// through ui.Suggest and keep the *config.RemoteError reachable via Unwrap.
func openRemote(root, name string, needRepo bool) (*config.Remote, *sync.Client, error) {
	rr, err := config.ResolveRemote(root)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	if err := rr.RequireAuth(); err != nil {
		return rr, nil, suggestRemote(name, err)
	}
	if needRepo {
		if err := rr.RequireRepo(); err != nil {
			return rr, nil, suggestRemote(name, err)
		}
	}
	return rr, sync.NewClient(rr.ServerURL, rr.Token), nil
}

func suggestRemote(name string, err error) error {
	wrapped := fmt.Errorf("%s: %w", name, err)
	var re *config.RemoteError
	if errors.As(err, &re) {
		return ui.Suggest(wrapped, re.ErrorAdvice()...)
	}
	return wrapped
}

// repoClient is the shared preflight for repo-scoped ctx subcommands (members,
// activity, …): resolve the remote, verify the project is wired up, and
// return a sync client plus the current repo id.
func repoClient(name string) (*sync.Client, string, error) {
	rr, client, err := openRemote(GetRootDir(), name, true)
	if err != nil {
		return nil, "", err
	}
	return client, rr.RepoID, nil
}
