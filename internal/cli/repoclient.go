package cli

import (
	"fmt"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
)

// repoClient is the shared preflight for repo-scoped ctx subcommands (members,
// activity, …): load config + credentials, verify the project is wired up, and
// return a sync client plus the current repo id. name prefixes the errors so
// each command's messages read naturally.
func repoClient(name string) (*sync.Client, string, error) {
	root := GetRootDir()
	cfg, _ := config.Load(root)
	creds, _ := config.LoadCredentials(root)
	if creds == nil || creds.Bearer() == "" {
		return nil, "", fmt.Errorf("%s: not authenticated — run 'ctx login' first", name)
	}
	if cfg.ServerURL == "" {
		return nil, "", fmt.Errorf("%s: server URL not set — run 'ctx remote set <url>' first", name)
	}
	if cfg.RepoID == "" {
		return nil, "", fmt.Errorf("%s: repo id not set — run 'ctx remote set-repo <id>' first", name)
	}
	return sync.NewClient(cfg.ServerURL, creds.Bearer()), cfg.RepoID, nil
}
