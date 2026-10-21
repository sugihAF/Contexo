package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/ui"
	"github.com/sugihAF/contexo/internal/version"
)

var rootDir string

// NewRootCmd creates the root ctx command.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ctx",
		Short: "Contexo CLI — share AI agent knowledge across a team",
		Long:  "ctx manages a per-project knowledge base of distilled AI sessions and syncs it with a Contexo server so teammates' agents start from the same context.",
		// SilenceErrors hands rendering to Execute, which prints the failure
		// plus its structured advice (or the one-line JSON envelope) instead
		// of cobra's bare "Error: <msg>". Without it every error would print
		// twice.
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
		// Runs only after a subcommand's RunE succeeds; emits the (cached,
		// opt-out, TTY-gated) "update available" nudge. Never errors, and
		// never in machine mode: a --json document (or show's raw bytes)
		// must be the only output, even on a TTY.
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			if ui.DetectMode(cmd, cmd.ErrOrStderr()).Machine {
				return
			}
			maybeNudge(cmd)
		},
	}

	cmd.PersistentFlags().StringVar(&rootDir, "root", "", "project root directory (default: current directory)")

	// Flag parse failures (unknown flag, missing or invalid value) are usage
	// errors: exit 3 with the envelope in --json mode. Subcommands inherit
	// this hook.
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return ui.Coded(err, contract.CodeUsage)
	})

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
	cmd.AddCommand(newMigrateCmd())
	cmd.AddCommand(newGenerateCmd())
	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newUpdateCmd())
	cmd.AddCommand(newShowCmd())
	cmd.AddCommand(newResolveCmd())
	cmd.AddCommand(newRepairCmd())

	codeArgsErrorsAsUsage(cmd)

	return cmd
}

// codeArgsErrorsAsUsage walks the whole tree and wraps every non-nil Args
// validator so a positional-argument error is coded usage. A validator that
// already returns a *contract.Error (say invalid_path) keeps its code.
// Commands with a nil Args keep it nil: cobra then applies legacyArgs, whose
// "unknown command" text ui.ToContractError already maps to usage.
func codeArgsErrorsAsUsage(c *cobra.Command) {
	if orig := c.Args; orig != nil {
		c.Args = func(cmd *cobra.Command, args []string) error {
			err := orig(cmd, args)
			if err == nil {
				return nil
			}
			var coded *contract.Error
			if errors.As(err, &coded) {
				return err
			}
			return ui.Coded(err, contract.CodeUsage)
		}
	}
	for _, sub := range c.Commands() {
		codeArgsErrorsAsUsage(sub)
	}
}

// Execute runs ctx with args, writing to stdout and stderr, and returns the
// process exit code. cmd/ctx/main.go is just os.Exit(Execute(...)).
//
// In envelope mode (ui.EnvelopeMode: the executed command's --json is true,
// it is `ctx show`, or the raw args carry --json) a failure is exactly one
// ctx.error.v1 line on stderr and the exit code comes from the contract
// table. Otherwise the failure is rendered for a human and the exit code is
// 1, as before.
func Execute(args []string, stdout, stderr io.Writer) int {
	// cobra falls back to os.Args[1:] when SetArgs gets nil.
	if args == nil {
		args = []string{}
	}
	root := newExecRoot(args, stdout, stderr)

	// ExecuteC returns the command that actually ran, not the root. That
	// distinction is load-bearing: mode detection keys off the resolved
	// command path and the invoked command's flags, so passing the root here
	// would classify `ctx diff --json` and `ctx version --short` as human
	// surfaces and decorate their failures with chrome a parser cannot read.
	executed, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	if executed == nil {
		executed = root
	}
	// The root sets SilenceErrors, so cobra's bare "Error: <msg>" is
	// replaced by the envelope or a rendered failure plus the commands that
	// fix it. Chrome goes to stderr; stdout stays data.
	if ui.EnvelopeMode(executed, args) {
		ui.ForStderr(executed).RenderEnvelope(err)
		return ui.ExitCode(err, true)
	}
	ui.ForStderr(executed).RenderError(err)
	return 1
}

// newExecRoot builds the root command for one Execute call, with args set
// and its writers wired to stdout and stderr.
//
// A writer is installed only when it is not the process's own stream. cobra's
// OutOrStderr() (and Print*/Usage, which use it) returns the SetOut writer
// whenever one is set and falls back to os.Stderr only when none is, so
// SetOut(os.Stdout) would move every OutOrStderr diagnostic (push's conflict
// list, pull's reindex warning, cobra's "Unknown help topic" and usage text)
// onto stdout: a human-output change, and a corrupt document in --json mode.
// Leaving the real streams unset keeps the binary's routing identical to a
// bare cobra root. Substitute writers (in-process tests) are installed as
// before; OutOrStderr text then lands in the stdout writer, as cobra defines.
func newExecRoot(args []string, stdout, stderr io.Writer) *cobra.Command {
	root := NewRootCmd()
	root.SetArgs(args)
	if stdout != io.Writer(os.Stdout) {
		root.SetOut(stdout)
	}
	if stderr != io.Writer(os.Stderr) {
		root.SetErr(stderr)
	}
	return root
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
