package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/ui"
)

// newResolveCmd is the hidden wave-0 stub of `ctx resolve <relpath>
// (--ours|--theirs) [--if-server-sha <sha>] [--json]`. Owner: WP-1.4, which
// replaces RunE and unhides it.
func newResolveCmd() *cobra.Command {
	var (
		ours, theirs bool
		ifServerSHA  string
		asJSON       bool
	)
	cmd := &cobra.Command{
		Use:    "resolve <relpath> (--ours|--theirs)",
		Short:  "Resolve a conflict by keeping the local or the server version",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Checked here, not with cobra flag groups: group errors bypass
			// the flag-error hook and would not be coded as usage.
			if ours == theirs {
				return ui.Coded(errors.New("resolve: pass exactly one of --ours or --theirs"), contract.CodeUsage)
			}
			if _, err := syncops.RequireHub(GetRootDir()); err != nil {
				return err
			}
			return syncops.NotImplemented("resolve")
		},
	}
	cmd.Flags().BoolVar(&ours, "ours", false, "keep the local content, re-based onto the server version")
	cmd.Flags().BoolVar(&theirs, "theirs", false, "replace the local file with the server version")
	cmd.Flags().StringVar(&ifServerSHA, "if-server-sha", "", "fail with stale unless the server is still at this sha")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit a JSON document")
	return cmd
}
