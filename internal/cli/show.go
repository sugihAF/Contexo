package cli

import (
	"errors"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

func init() {
	registerCapability(contract.CapShowBase)
	registerCapability(contract.CapShowServer)
	registerCapability(contract.CapShowSHA)
}

// newShowCmd is `ctx show <relpath> --at base|server|<sha>` (WP-1.4), the
// content source of the extension's read-only contexo: documents. Success
// writes only the page bytes to stdout (no added newline, CRLF and BOM
// kept) and nothing to stderr.
//
// show is always machine mode (E20/E21): its hidden --json flag reads true
// and cannot be turned off, so ui.DetectMode and ui.EnvelopeMode read it like
// any --json flag and internal/ui/mode.go needs no edit.
//
// The checks run cheapest and most specific first: cobra's argument and
// flag rules, then the path and --at rules (no I/O), then the hub, and only
// for the network variants the credentials and repo id. A bad path is
// therefore invalid_path even in an uninitialised or unauthenticated
// project, and --at base never needs credentials.
func newShowCmd() *cobra.Command {
	var at string
	cmd := &cobra.Command{
		Use:   "show <relpath> --at base|server|<sha>",
		Short: "Print a page's base, server or historical bytes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			env := syncops.NewEnv(root, "", "", syncops.Author{}, nil)
			rel, online, err := syncops.ShowPreflight(env, syncops.ShowOptions{Path: args[0], At: at})
			if err != nil {
				return err
			}
			if _, err := syncops.RequireHub(root); err != nil {
				return err
			}
			if online {
				rr, client, err := openRemote(root, "show", true)
				if err != nil {
					return err
				}
				env = syncops.NewEnv(root, rr.RepoID, rr.ServerURL, syncops.Author{}, client)
			}
			b, err := syncops.Show(cmd.Context(), env, syncops.ShowOptions{Path: rel, At: at})
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
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
