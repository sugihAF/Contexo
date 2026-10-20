package cli

import (
	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops"
)

// newShowCmd is the hidden wave-0 stub of `ctx show <relpath> --at
// base|server|<sha>`. Owner: WP-1.4, which replaces RunE and unhides it.
//
// show is always machine mode (E20): its hidden --json flag defaults to true,
// so ui.DetectMode and ui.EnvelopeMode read it like any --json flag and
// internal/ui/mode.go needs no edit.
func newShowCmd() *cobra.Command {
	var (
		at     string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:    "show <relpath> --at base|server|<sha>",
		Short:  "Print a page's base, server or historical bytes",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := syncops.RequireHub(GetRootDir()); err != nil {
				return err
			}
			return syncops.NotImplemented("show")
		},
	}
	cmd.Flags().StringVar(&at, "at", "", "base, server, or a commit sha")
	_ = cmd.MarkFlagRequired("at")
	cmd.Flags().BoolVar(&asJSON, "json", true, "machine mode (always on)")
	_ = cmd.Flags().MarkHidden("json")
	return cmd
}
