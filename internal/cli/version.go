package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/version"
)

func newVersionCmd() *cobra.Command {
	var short, asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the ctx version",
		RunE: func(cmd *cobra.Command, args []string) error {
			// --json wins over --short: it is the extension's handshake.
			if asJSON {
				return contract.WriteDoc(cmd.OutOrStdout(), versionDoc())
			}
			if short {
				fmt.Fprintln(cmd.OutOrStdout(), version.Version)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), version.Full())
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print just the version number")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the ctx.version.v1 handshake document (contract version and capabilities)")
	return cmd
}

// versionDoc is the `ctx version --json` handshake: build identity plus the
// capabilities this build registered, in contract order (E19).
func versionDoc() contract.VersionDoc {
	return contract.VersionDoc{
		Schema:          contract.SchemaVersion,
		ContractVersion: contract.ContractVersion,
		Version:         version.Version,
		Commit:          version.Commit,
		Date:            version.Date,
		DevBuild:        version.IsDevBuild(),
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		Capabilities:    advertisedCapabilities(),
	}
}
