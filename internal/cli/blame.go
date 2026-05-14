package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/symbols"
)

func newBlameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "blame <file#symbol>",
		Short: "Show context history for a symbol",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			db, err := openDB(root)
			if err != nil {
				return err
			}
			defer db.Close()

			file, symbol := symbols.ParseBlameArg(args[0])
			symbolKey := symbols.EncodeSymbolKey(file, symbol)

			commits, err := db.GetBySymbol(context.Background(), symbolKey)
			if err != nil {
				return err
			}

			if len(commits) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No context found for %s\n", args[0])
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Context blame for %s:\n\n", args[0])
			for _, c := range commits {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s %s (%s)\n", shortID(c.CommitID), c.Title,
					c.CreatedAt.Format("2006-01-02"))
				if len(c.Evidence) > 0 {
					for _, e := range c.Evidence {
						fmt.Fprintf(cmd.OutOrStdout(), "    Evidence: session %s turns %d-%d\n",
							e.SessionID, e.FromTurn, e.ToTurn)
					}
				}
			}
			return nil
		},
	}
}
