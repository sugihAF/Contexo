package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <commit-id>",
		Short: "Show context commit detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			db, err := openDB(root)
			if err != nil {
				return err
			}
			defer db.Close()

			commit, err := db.GetCommit(context.Background(), args[0])
			if err != nil {
				return err
			}
			if commit == nil {
				return fmt.Errorf("commit not found: %s", args[0])
			}

			data, err := json.MarshalIndent(commit, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		},
	}
}
