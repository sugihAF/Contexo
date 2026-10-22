package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

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

// statusRenderHuman prints doc for a person. rr (nil for a corrupt config or
// credentials file) supplies the identity block; the token itself is never
// printed.
func statusRenderHuman(cmd *cobra.Command, doc *contract.StatusDoc, rr *config.Remote) {
	u := ui.From(cmd)
	out := cmd.OutOrStdout()

	u.Brand(statusBrand(doc, rr))
	fmt.Fprintln(out)

	u.Eyebrow("REMOTE")
	if rr != nil {
		statusRenderIdentity(u, doc, rr)
	} else {
		// Login or set-repo hints would not help: the file needs fixing, and
		// remote.error (online) names it.
		u.Field("config", "unreadable (.contexo/config.json or the credentials file)")
	}
	if e := doc.Remote.Error; e != nil {
		u.Warn("server: %s", e.Message)
		for _, a := range e.Advice {
			u.Hint("try", a)
		}
	}
	fmt.Fprintln(out)

	u.Eyebrow("KNOWLEDGE")
	u.Field("pages", statusCountsLine(doc))
	if doc.Project.LastPullSHA != "" {
		u.Field("last pull", shortSHA(doc.Project.LastPullSHA))
	} else {
		u.Field("last pull", "(never)")
	}
	if c := doc.Captures; c.Pending > 0 {
		u.Field("captures", fmt.Sprintf("%d pending (%d fresh)", c.Pending, c.Fresh))
	} else {
		u.Field("captures", "none pending")
	}
	if doc.Captures.BlocksPush {
		u.Warn("fresh captures block push: share via your agent (it distills them), then push")
	}
	for _, w := range doc.Warnings {
		if w.Code == contract.WarnServerURLMismatch {
			continue
		}
		u.Warn("%s", w.Message)
		// base_repair_skipped names the pages the UNVERIFIED list shows.
		if w.Code != contract.WarnBaseRepairSkipped {
			for _, p := range w.Paths {
				fmt.Fprintf(out, "    %s\n", p)
			}
		}
	}

	if !statusRenderSections(u, out, doc) {
		fmt.Fprintln(out)
		if doc.Remote.Source == contract.RemoteLive {
			u.Success("in sync with the server")
		} else {
			u.Success("no local changes (server not checked)")
		}
	}
}

// statusRenderIdentity prints the REMOTE fields of the resolved remote: the
// server and where its URL came from, the mismatch warning, the repo, the
// user and the auth state.
func statusRenderIdentity(u *ui.UI, doc *contract.StatusDoc, rr *config.Remote) {
	if rr.ServerURL != "" {
		server := rr.ServerURL
		if rr.URLSource != "" {
			server += "  (from " + rr.URLSource + ")"
		}
		u.Field("server", server)
	} else {
		u.Field("server", "(unresolved — run 'ctx auth login --server <url>')")
	}
	// The REMOTE block owns the mismatch text, so the document's
	// server_url_mismatch warning is skipped later.
	if rr.Mismatch {
		u.Warn("config server_url %s differs from your credentials' server; ctx uses %s. Only if you trust it, log in with its own dashboard: 'ctx auth login --server %s --dashboard <its dashboard URL>' (this replaces your global credentials)",
			rr.ConfiguredServerURL, rr.ServerURL, rr.ConfiguredServerURL)
	}
	if rr.RepoID != "" {
		u.Field("repo", rr.RepoID)
	} else {
		u.Field("repo", "(none — run 'ctx remote set-repo <id>')")
	}
	if rr.Token != "" && rr.Creds != nil && rr.Creds.UserName != "" {
		u.Field("user", fmt.Sprintf("%s <%s>", rr.Creds.UserName, rr.Creds.UserEmail))
	}
	u.Field("auth", statusAuthLine(doc, rr))
}

// statusBrand is the brand line's state and whether its dot is healthy.
func statusBrand(doc *contract.StatusDoc, rr *config.Remote) (string, bool) {
	switch {
	case rr == nil:
		return "config unreadable", false
	case rr.Token == "":
		return "not authenticated", false
	case rr.ServerURL == "":
		return "server unresolved", false
	case doc.Offline:
		return "offline", false
	case rr.RepoID == "":
		return "no repo configured", false
	case doc.Auth.State == contract.AuthRejected:
		return "token rejected", false
	case doc.Remote.Error != nil && doc.Remote.Error.Code == contract.CodeNetwork:
		return "server unreachable", false
	case doc.Remote.Error != nil:
		return "server error", false
	}
	return "connected", true
}

// statusAuthLine is the auth field: the auth state and the token's kind.
func statusAuthLine(doc *contract.StatusDoc, rr *config.Remote) string {
	if rr.Token == "" {
		return "no (run 'ctx auth login')"
	}
	state := doc.Auth.State
	if state == "" {
		state = contract.AuthUnchecked
	}
	return fmt.Sprintf("%s (%s)", strings.ReplaceAll(string(state), "_", " "), rr.TokenKind())
}

// statusCountsLine is "<total> · <n> <state> · ..." over the non-zero states,
// most urgent first; incoming and incoming_new count together.
func statusCountsLine(doc *contract.StatusDoc) string {
	c := doc.Counts
	parts := []string{fmt.Sprintf("%d", len(doc.Pages))}
	for _, s := range []struct {
		n     int
		label string
	}{
		{c.Conflict, "conflict"},
		{c.Modified, "modified"},
		{c.New, "new"},
		{c.Incoming + c.IncomingNew, "incoming"},
		{c.Invalid, "invalid"},
		{c.Unverified, "unverified"},
		{c.DeletedLocal, "deleted locally"},
		{c.Unchanged, "unchanged"},
	} {
		if s.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", s.n, s.label))
		}
	}
	return strings.Join(parts, " · ")
}

// statusDetails are the per-state notes printed beside a listed path.
var statusDetails = map[contract.PageState]string{
	contract.StateModified:     "modified",
	contract.StateNew:          "new",
	contract.StateIncoming:     "changed on server",
	contract.StateIncomingNew:  "new on server",
	contract.StateUnverified:   "stale base",
	contract.StateDeletedLocal: "deleted locally",
}

// statusRenderSections lists the pages that need an action, each group
// followed by the command that acts on it. It reports whether it printed
// anything.
func statusRenderSections(u *ui.UI, out io.Writer, doc *contract.StatusDoc) bool {
	by := map[contract.PageState][]contract.PageEntry{}
	for _, p := range doc.Pages {
		by[p.State] = append(by[p.State], p)
	}
	group := func(states ...contract.PageState) []contract.PageEntry {
		var g []contract.PageEntry
		for _, s := range states {
			g = append(g, by[s]...)
		}
		return g
	}
	// target names the one page of a group, or a placeholder for several.
	target := func(g []contract.PageEntry) string {
		if len(g) == 1 {
			return g[0].Path
		}
		return "<path>"
	}

	printed := false
	section := func(title string, g []contract.PageEntry, detail func(contract.PageEntry) string) bool {
		if len(g) == 0 {
			return false
		}
		printed = true
		fmt.Fprintln(out)
		u.Eyebrow(title)
		width := 0
		for _, p := range g {
			width = max(width, len(p.Path))
		}
		for _, p := range g {
			if d := detail(p); d != "" {
				fmt.Fprintf(out, "    %-*s   %s\n", width, p.Path, d)
			} else {
				fmt.Fprintf(out, "    %s\n", p.Path)
			}
		}
		return true
	}
	byState := func(p contract.PageEntry) string { return statusDetails[p.State] }

	if g := group(contract.StateConflict); section("CONFLICTS", g,
		func(p contract.PageEntry) string { return string(p.ConflictKind) }) {
		u.Hint("keep yours ", "ctx resolve "+target(g)+" --ours")
		u.Hint("take theirs", "ctx resolve "+target(g)+" --theirs")
	}
	if section("TO PUSH", group(contract.StateModified, contract.StateNew), byState) {
		u.Hint("run", "ctx push")
	}
	if section("TO PULL", group(contract.StateIncoming, contract.StateIncomingNew), byState) {
		u.Hint("run", "ctx pull")
	}
	if section("INVALID (never pushed)", group(contract.StateInvalid), statusInvalidDetail) {
		u.Step("fix the file(s) above, then run 'ctx status' again")
	}
	if section("UNVERIFIED", group(contract.StateUnverified), byState) {
		u.Hint("run", "ctx repair")
	}
	if g := group(contract.StateDeletedLocal); section("DELETED LOCALLY (deletes are not synced)", g, byState) {
		u.Hint("restore", "ctx resolve "+target(g)+" --theirs")
	}
	return printed
}

// statusInvalidDetail is "<stage>: <message>", plus the expected path of a
// path_mismatch when the message does not name it already.
func statusInvalidDetail(p contract.PageEntry) string {
	if p.Invalid == nil {
		return ""
	}
	d := fmt.Sprintf("%s: %s", p.Invalid.Stage, p.Invalid.Message)
	if p.Invalid.ExpectedPath != "" && !strings.Contains(p.Invalid.Message, p.Invalid.ExpectedPath) {
		d += " (expected " + p.Invalid.ExpectedPath + ")"
	}
	return d
}
