package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
)

func newEvolutionCmd() *cobra.Command {
	var typ string
	var limit int
	var asJSON, showDiff bool
	cmd := &cobra.Command{
		Use:   "evolution <slug>",
		Short: "Show the full evolution of a page: each commit + what it changed",
		Long: "Combines `ctx history` and a per-commit `ctx diff` into a single round-trip.\n" +
			"Useful when a new dev wants the trajectory of a page in one shot — every commit\n" +
			"in order, each annotated with what that specific commit changed.\n\n" +
			"By default prints a one-line summary per commit. Pass --show-diff to inline the\n" +
			"full per-section diff under each commit. Pass --json for the raw structured form.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]
			root := GetRootDir()
			if typ != "" && !contains(validTypes(), typ) {
				return fmt.Errorf("invalid --type %q (want one of %s)", typ, strings.Join(validTypes(), "|"))
			}
			path, err := resolveSlugPath(root, slug, typ)
			if err != nil {
				return err
			}
			cfg, err := config.Load(root)
			if err != nil {
				return err
			}
			creds, err := config.LoadCredentials(root)
			if err != nil || creds == nil {
				return fmt.Errorf("evolution: not authenticated (run 'ctx auth login')")
			}
			serverURL := chooseServerURL(creds, cfg)
			if serverURL == "" || cfg.RepoID == "" {
				return fmt.Errorf("evolution: server URL or repo_id not configured")
			}
			client := sync.NewClient(serverURL, creds.Bearer())
			entries, err := client.PageEvolution(cfg.RepoID, path, limit)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No evolution found for %s\n", path)
				return nil
			}
			if asJSON {
				return printEvolutionJSON(cmd.OutOrStdout(), entries)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Evolution of %s (%d commit%s, newest first):\n\n",
				path, len(entries), pluralSuffix(len(entries)))
			for _, e := range entries {
				fmt.Fprintf(out, "%s  %s  %s  %s\n",
					shortSHA(e.Commit.SHA),
					e.Commit.Time.Format("2006-01-02"),
					padAuthor(e.Commit.Author),
					e.Commit.Message,
				)
				if showDiff {
					text := e.Diff.ToText("")
					for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
						fmt.Fprintln(out, "    "+line)
					}
					fmt.Fprintln(out)
				} else if summary := summarizeDiff(&e.Diff); summary != "" {
					fmt.Fprintf(out, "    %s\n", summary)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "page type (concept|entity|analysis|source); only needed when the slug is ambiguous")
	cmd.Flags().IntVar(&limit, "limit", 20, "max commits in the evolution")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit raw JSON instead of formatted text")
	cmd.Flags().BoolVar(&showDiff, "show-diff", false, "include the full per-section diff under each commit")
	return cmd
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func printEvolutionJSON(out io.Writer, entries []sync.EvolutionEntry) error {
	js, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(js))
	return err
}
