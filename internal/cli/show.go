package cli

import (
	"errors"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops"
)

// newShowCmd is the hidden wave-0 stub of `ctx show <relpath> --at
// base|server|<sha>`. Owner: WP-1.4, which replaces RunE and unhides it.
//
// show is always machine mode (E20/E21): its hidden --json flag reads true
// and cannot be turned off, so ui.DetectMode and ui.EnvelopeMode read it like
// any --json flag and internal/ui/mode.go needs no edit.
func newShowCmd() *cobra.Command {
	var at string
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
	js := cmd.Flags().VarPF(&showJSONFlag{}, "json", "", "machine mode (always on)")
	js.NoOptDefVal = "true"
	_ = cmd.Flags().MarkHidden("json")
	return cmd
}

// showJSONFlag is show's hidden --json value: a bool that is always true.
// A plain BoolVar could be set false with --json=false, which would take show
// out of machine mode (human rendering, exit 1, the update nudge) although
// E21 makes every show failure an envelope. A bare --json or any true value
// is accepted as a no-op; a false or malformed value fails flag parsing,
// which is a usage error rendered as an envelope because the flag still
// reads true. Type() is "bool", so pflag's GetBool and help treat it as one.
type showJSONFlag struct{}

func (*showJSONFlag) String() string   { return "true" }
func (*showJSONFlag) Type() string     { return "bool" }
func (*showJSONFlag) IsBoolFlag() bool { return true }

func (*showJSONFlag) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	if !v {
		return errors.New("ctx show always writes machine output; --json cannot be false")
	}
	return nil
}
