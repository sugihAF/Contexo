package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/updater"
	"github.com/sugihAF/contexo/internal/version"
)

func newUpdateCmd() *cobra.Command {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update ctx to the latest release",
		Long: "Checks GitHub for a newer ctx release and, unless --check is given, " +
			"downloads the verified binary for your platform and replaces ctx in place.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			if version.IsDevBuild() {
				fmt.Fprintln(out, "This is a dev build (no version stamp), so ctx can't self-update.")
				fmt.Fprintln(out, "Reinstall with the install script, or:")
				fmt.Fprintln(out, "  go install github.com/sugihAF/contexo/cmd/ctx@latest")
				return nil
			}

			// Package-manager installs update through their manager so its
			// metadata stays consistent. (Homebrew/Scoop are a future channel;
			// the redirect is harmless until then.)
			switch updater.CurrentInstallMethod() {
			case updater.InstallHomebrew:
				fmt.Fprintln(out, "ctx was installed via Homebrew. Update with:")
				fmt.Fprintln(out, "  brew upgrade contexo")
				return nil
			case updater.InstallScoop:
				fmt.Fprintln(out, "ctx was installed via Scoop. Update with:")
				fmt.Fprintln(out, "  scoop update ctx")
				return nil
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 90*time.Second)
			defer cancel()

			fmt.Fprintf(out, "Current version: %s\n", version.Version)
			fmt.Fprintln(out, "Checking for updates…")
			rel, err := updater.LatestRelease(ctx)
			if err != nil {
				return fmt.Errorf("check for updates: %w", err)
			}

			if !updater.IsNewer(rel.Version(), version.Version) {
				fmt.Fprintf(out, "You're already on the latest version (%s).\n", version.Version)
				return nil
			}
			fmt.Fprintf(out, "Latest is %s.\n", rel.Version())

			if checkOnly {
				fmt.Fprintln(out, "Run `ctx update` to install it.")
				return nil
			}

			fmt.Fprintln(out, "Downloading…")
			bin, err := updater.FetchVerifiedBinary(ctx, rel)
			if err != nil {
				return fmt.Errorf("download update: %w", err)
			}
			fmt.Fprintln(out, "Verified checksum. Installing…")
			if err := updater.Apply(bin); err != nil {
				return fmt.Errorf("install update: %w", err)
			}
			fmt.Fprintf(out, "Updated ctx %s → %s 🎉\n", version.Version, rel.Version())
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "only check for a newer version; don't install")
	return cmd
}
