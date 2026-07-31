package cli

import (
	"fmt"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/ui"
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
		return nil, "", ui.Suggest(fmt.Errorf("%s: not authenticated", name), "ctx auth login")
	}
	if cfg.ServerURL == "" {
		return nil, "", ui.Suggest(fmt.Errorf("%s: server URL not set", name), "ctx remote set <url>")
	}
	if cfg.RepoID == "" {
		return nil, "", ui.Suggest(fmt.Errorf("%s: repo id not set", name), "ctx remote set-repo <id>")
	}
	return sync.NewClient(cfg.ServerURL, creds.Bearer()), cfg.RepoID, nil
}
