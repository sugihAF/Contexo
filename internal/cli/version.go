package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/version"
)

func newVersionCmd() *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the ctx version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if short {
				fmt.Fprintln(cmd.OutOrStdout(), version.Version)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), version.Full())
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print just the version number")
	return cmd
}
