package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/store"
)

func newContextCmd() *cobra.Command {
	var (
		feature  string
		log      int
		metadata bool
		full     bool
	)

	cmd := &cobra.Command{
		Use:   "context",
		Short: "Show multi-resolution context",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			db, err := openDB(root)
			if err != nil {
				return err
			}
			defer db.Close()

			ctx := context.Background()

			if metadata {
				cfg, err := config.Load(root)
				if err != nil {
					return err
				}
				data, _ := json.MarshalIndent(cfg, "", "  ")
				fmt.Fprintf(cmd.OutOrStdout(), "Configuration:\n%s\n", string(data))

				state, err := loadCaptureState(config.CtxDirPath(root))
				if err == nil {
					stateData, _ := json.MarshalIndent(state, "", "  ")
					fmt.Fprintf(cmd.OutOrStdout(), "\nCapture Status:\n%s\n", string(stateData))
				}
				return nil
			}

			if feature != "" {
				// Show feature overview
				overview, err := db.GetOverview(ctx, "", feature)
				if err != nil {
					return err
				}

				if overview != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Feature: %s\n", overview.Feature)
					fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", overview.Status)
					fmt.Fprintf(cmd.OutOrStdout(), "Summary: %s\n", overview.Summary)
					fmt.Fprintf(cmd.OutOrStdout(), "Commits: %d\n\n", len(overview.CommitIDs))
				}

				// Show recent commits for this feature
				commits, err := db.ListCommits(ctx, store.CommitFilter{Feature: feature, Limit: 10})
				if err != nil {
					return err
				}

				if len(commits) > 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "Recent Commits:")
					for _, c := range commits {
						fmt.Fprintf(cmd.OutOrStdout(), "  %s %s (%s)\n",
							shortID(c.CommitID), c.Title, c.CreatedAt.Format("2006-01-02"))
					}
				}
			}

			if log > 0 && feature != "" {
				entries, err := db.ListActivity(ctx, "", feature, log)
				if err != nil {
					return err
				}

				if len(entries) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "\nActivity Log (last %d):\n", log)
					for _, e := range entries {
						fmt.Fprintf(cmd.OutOrStdout(), "  [%s] %s: %s (%s)\n",
							e.Ts.Format("2006-01-02 15:04"), e.Type, e.Summary, e.Actor)
					}
				}
			}

			if full && feature != "" {
				// Full output: overview + commits + activity
				overview, _ := db.GetOverview(ctx, "", feature)
				if overview != nil {
					data, _ := json.MarshalIndent(overview, "", "  ")
					fmt.Fprintf(cmd.OutOrStdout(), "\nFull Overview:\n%s\n", string(data))
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&feature, "feature", "", "feature name")
	cmd.Flags().IntVar(&log, "log", 0, "show last N activity entries")
	cmd.Flags().BoolVar(&metadata, "metadata", false, "show repo config + capture status")
	cmd.Flags().BoolVar(&full, "full", false, "show full overview JSON")

	return cmd
}
