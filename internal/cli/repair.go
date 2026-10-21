package cli

import (
	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops"
)

// newRepairCmd is the hidden wave-0 stub of `ctx repair [--dry-run] [--json]`.
// Owner: WP-1.5, which replaces RunE, unhides it and registers "repair".
func newRepairCmd() *cobra.Command {
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:    "repair",
		Short:  "Backfill and repair base snapshots",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := syncops.RequireHub(GetRootDir()); err != nil {
				return err
			}
			return syncops.NotImplemented("repair")
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be repaired; write nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit a JSON document")
	return cmd
}
