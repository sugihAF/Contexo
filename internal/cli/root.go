package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/version"
)

var rootDir string

// NewRootCmd creates the root ctx command.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "ctx",
		Short:        "Contexo CLI — share AI agent knowledge across a team",
		Long:         "ctx manages a per-project knowledge base of distilled AI sessions and syncs it with a Contexo server so teammates' agents start from the same context.",
		SilenceUsage: true,
		Version:      version.Version,
		// Runs only after a subcommand's RunE succeeds; emits the (cached,
		// opt-out, TTY-gated) "update available" nudge. Never errors.
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			maybeNudge(cmd)
		},
	}

	cmd.PersistentFlags().StringVar(&rootDir, "root", "", "project root directory (default: current directory)")

	cmd.AddCommand(NewInitCmd())
	cmd.AddCommand(newDetachCmd())
	cmd.AddCommand(newPushCmd())
	cmd.AddCommand(newPullCmd())
	cmd.AddCommand(newMCPCmd())
	cmd.AddCommand(newRemoteCmd())
	cmd.AddCommand(newAuthCmd())
	cmd.AddCommand(newLoginCmd())
	cmd.AddCommand(newJoinCmd())
	cmd.AddCommand(newInviteCmd())
	cmd.AddCommand(newMembersCmd())
	cmd.AddCommand(newActivityCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newLogCmd())
	cmd.AddCommand(newHistoryCmd())
	cmd.AddCommand(newDiffCmd())
	cmd.AddCommand(newEvolutionCmd())
	cmd.AddCommand(newCaptureCmd())
	cmd.AddCommand(newHooksCmd())
	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newUpdateCmd())

	return cmd
}

// GetRootDir returns the project root directory.
func GetRootDir() string {
	if rootDir != "" {
		return rootDir
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot get working directory: %v\n", err)
		return "."
	}
	return dir
}
