package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
)

func newLogCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show the server's commit timeline (who changed what when)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			creds, err := config.LoadCredentials(root)
			if err != nil || creds == nil {
				return fmt.Errorf("log: not authenticated (run 'ctx auth login')")
			}
			serverURL := chooseServerURL(creds, cfg)
			if serverURL == "" || cfg.RepoID == "" {
				return fmt.Errorf("log: server URL or repo_id not configured")
			}

			client := sync.NewClient(serverURL, creds.Bearer())
			commits, err := client.Timeline(cfg.RepoID, limit)
			if err != nil {
				return err
			}
			if len(commits) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No commits yet")
				return nil
			}
			for _, c := range commits {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s — %s\n",
					shortSHA(c.SHA),
					c.Time.Format("2006-01-02 15:04"),
					c.Author,
					c.Message,
				)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "max commits to show")
	return cmd
}
