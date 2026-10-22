package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/syncops"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/ui"
)

func init() {
	registerCapability(contract.CapStatusJSON)
	registerCapability(contract.CapStatusOffline)
}

func newStatusCmd() *cobra.Command {
	var jsonOut, offline, noDrift bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show each page's local, base and server state",
		Long: "status compares every page's working file, its last-synced base and the server's " +
			"listing (one request), and lists what to push, pull, resolve or fix. --offline makes " +
			"no request: server knowledge then comes only from recorded conflicts.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return statusRun(cmd, jsonOut, offline || noDrift)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the ctx.status.v1 document")
	cmd.Flags().BoolVar(&offline, "offline", false, "make no network request")
	// --no-drift predates --offline and stays as a hidden alias of it.
	cmd.Flags().BoolVar(&noDrift, "no-drift", false, "alias of --offline")
	_ = cmd.Flags().MarkHidden("no-drift")
	return cmd
}

// statusRun builds the status document and prints it as JSON or for a human.
// Only a missing .contexo/ (not_initialized) or an engine failure is an error;
// an unusable remote is reported inside the document (E22).
func statusRun(cmd *cobra.Command, jsonOut, offline bool) error {
	root, err := filepath.Abs(GetRootDir())
	if err != nil {
		return syncops.Wrap(err, contract.CodeInternal, "status: resolve root")
	}
	if _, err := syncops.RequireHub(root); err != nil {
		if jsonOut {
			return fmt.Errorf("status: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Not initialized (run 'ctx init')")
		return nil
	}

	// rr is nil only for a corrupt config or credentials file; status still
	// renders then, with remote.error from rerr (R10).
	rr, client, rerr := openRemote(root, "status", true)
	var remote syncops.Remote
	if !offline && rerr == nil {
		remote = client
	}
	env := syncops.NewEnv(root, "", "", syncops.Author{}, remote)
	if rr != nil {
		env = syncops.NewEnv(root, rr.RepoID, rr.ServerURL, statusAuthor(rr), remote)
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	doc, err := syncops.Status(ctx, env, syncops.StatusOptions{Offline: offline})
	if err != nil {
		return err
	}
	// --offline asked for no server, so an unusable remote is no error then.
	var unusable error
	if !offline {
		unusable = rerr
	}
	syncops.FillStatusIdentity(doc, rr, unusable)

	if jsonOut {
		return contract.WriteDoc(cmd.OutOrStdout(), doc)
	}
	statusRenderHuman(cmd, doc, rr)
	return nil
}

// statusAuthor is the commit identity from the resolved credentials.
func statusAuthor(rr *config.Remote) syncops.Author {
	if rr.Creds == nil {
		return syncops.Author{}
	}
	return syncops.Author{Name: rr.Creds.UserName, Email: rr.Creds.UserEmail}
}

// statusRenderHuman prints doc for a person. rr (nil for a corrupt config)
// supplies the identity block; the token itself is never printed.
func statusRenderHuman(cmd *cobra.Command, doc *contract.StatusDoc, rr *config.Remote) {
	u := ui.From(cmd)
	out := cmd.OutOrStdout()
	if rr == nil {
		rr = &config.Remote{}
	}

	authed := rr.Token != ""
	state := "connected"
	switch {
	case !authed:
		state = "not authenticated"
	case rr.ServerURL == "":
		state = "server unresolved"
	case doc.Offline:
		state = "offline"
	}
	u.Brand(state, authed && rr.ServerURL != "")
	fmt.Fprintln(out)

	u.Eyebrow("REMOTE")
	if rr.ServerURL != "" {
		u.Field("server", rr.ServerURL)
	} else {
		u.Field("server", "(unresolved — run 'ctx auth login --server <url>')")
	}
	if rr.Mismatch {
		u.Warn("config server_url %s differs from your credentials' server; ctx uses %s. Only if you trust it, log in with its own dashboard: 'ctx auth login --server %s --dashboard <its dashboard URL>' (this replaces your global credentials)",
			rr.ConfiguredServerURL, rr.ServerURL, rr.ConfiguredServerURL)
	}
	if rr.RepoID != "" {
		u.Field("repo", rr.RepoID)
	} else {
		u.Field("repo", "(none — run 'ctx remote set-repo <id>')")
	}
}
