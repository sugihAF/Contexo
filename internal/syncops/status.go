package syncops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/ui"
)

// Owner: WP-1.1. Fields may be added, never renamed or removed.

// StatusOptions configures Status.
type StatusOptions struct {
	Offline bool
}

// statusEnsureBase is the EnsureBase seam: tests override it to observe the
// options status passes and to simulate a repair.
var statusEnsureBase = EnsureBase

// statusCheckGuard is the CheckGuard seam behind captures.blocks_push.
var statusCheckGuard = CheckGuard

// Status builds the `ctx status --json` document. Offline (o.Offline or no
// Remote) it reads only local files and writes nothing; remote.source is
// none and R follows the E9 sidecar rule.
//
// It fails only when the project is not initialized (not_initialized) or the
// local state cannot be read (internal).
func Status(ctx context.Context, env Env, o StatusOptions) (*contract.StatusDoc, error) {
	hub, err := RequireHub(env.Root)
	if err != nil {
		return nil, Wrap(err, contract.CodeNotInitialized, "status", "ctx init")
	}
	now := time.Now()
	if env.Now != nil {
		now = env.Now()
	}
	doc := statusNewDoc(env, now, o.Offline)

	var live statusLive // zero: no live listing (E9 offline rule)
	if !o.Offline && env.Online() {
		live = statusOnline(ctx, env, doc, now)
	}
	if w, ok := statusServerOutdated(env.ServerURL, doc.Server); ok {
		doc.Warnings = append(doc.Warnings, w)
	}
	doc.Warnings = append(doc.Warnings, live.warnings...)

	// Read the local state only now: EnsureBase may have rewritten
	// state.json, bases.json and conflicts.json (E5 step 4).
	view, err := syncstate.LoadView(hub, live.remote)
	if err != nil {
		return nil, Wrap(err, contract.CodeInternal, "status")
	}
	doc.Project.LastPullSHA = view.State.LastPullSHA

	unverified := []string{}
	for _, rel := range view.Paths {
		f := view.Facts[rel]
		res, err := contract.ClassifySides(contract.Side(f.Local), contract.Side(f.Base), contract.Side(f.Remote))
		if err != nil {
			return nil, Wrap(err, contract.CodeInternal, "status")
		}
		if f.Conflict != nil {
			res = ApplySidecar(res, f.Base.SHA, &SidecarFact{
				ServerSHA: f.Conflict.ServerSHA, Kind: contract.ConflictKind(f.Conflict.Kind)})
		}
		if res.State == "" {
			continue
		}
		if res.State == contract.StateUnverified {
			unverified = append(unverified, rel)
		}
		doc.Pages = append(doc.Pages, statusEntry(f, res, live.meta))
		statusCount(&doc.Counts, res.State)
	}
	doc.Counts.Ahead = doc.Counts.Modified + doc.Counts.New
	doc.Counts.Behind = doc.Counts.Incoming + doc.Counts.IncomingNew

	// Deviation 13: online, every page left unverified is named, also when
	// EnsureBase failed or said nothing. Merging below keeps EnsureBase's
	// own message.
	if doc.Remote.Source == contract.RemoteLive && (len(unverified) > 0 || live.ensureErr != nil) {
		doc.Warnings = append(doc.Warnings, contract.Warning{
			Code:    contract.WarnBaseRepairSkipped,
			Message: statusRepairMessage(len(unverified), live.ensureErr),
			Paths:   unverified,
		})
	}
	doc.Warnings = statusMergeWarnings(doc.Warnings)
	return doc, nil
}

// statusNewDoc is the document skeleton: every array non-nil, the remote
// unknown (source none) until a live listing says otherwise. auth.state
// stays "" while no request is made; FillStatusIdentity decides it then.
func statusNewDoc(env Env, now time.Time, offline bool) *contract.StatusDoc {
	root, err := filepath.Abs(env.Root)
	if err != nil {
		root = env.Root
	}
	return &contract.StatusDoc{
		Schema:      contract.SchemaStatus,
		GeneratedAt: contract.TimeOf(now),
		Offline:     offline,
		Root:        root,
		ContexoDir:  filepath.Join(root, hubDirName),
		Project:     contract.ProjectInfo{RepoID: env.RepoID},
		Server: contract.ServerInfo{
			URL:          env.ServerURL,
			Capabilities: []contract.ServerCapability{},
		},
		Remote:   contract.RemoteInfo{Source: contract.RemoteNone},
		Pages:    []contract.PageEntry{},
		Captures: contract.CaptureInfo{Sessions: []contract.CaptureSession{}},
		Warnings: []contract.Warning{},
	}
}

// statusLive is what the live listing contributed to a status run.
type statusLive struct {
	remote    map[string]string // path -> sha; nil without a live listing
	meta      map[string]sync.PageMeta
	warnings  []contract.Warning // EnsureBase's
	ensureErr error              // EnsureBase's failure (base_repair_skipped)
}

// statusOnline makes status's one ListPages call and reads ServerInfo right
// after it, before EnsureBase's requests can change what it reports
// (deviation 14). On success it fills server, auth and remote and runs
// EnsureBase over the live listing (E5 step 3). A failed listing never
// fails status: statusRemoteFailure reports it.
func statusOnline(ctx context.Context, env Env, doc *contract.StatusDoc, now time.Time) statusLive {
	listing, lerr := env.Remote.ListPages(env.RepoID)
	ver, caps, known := env.Remote.ServerInfo()
	doc.Server.Version = ver
	doc.Server.CapabilitiesKnown = known
	doc.Server.Capabilities = append([]contract.ServerCapability{}, caps...)
	if lerr != nil {
		return statusRemoteFailure(doc, lerr, now)
	}

	if listing == nil {
		listing = []sync.PageMeta{}
	}
	live := statusLive{
		remote: make(map[string]string, len(listing)),
		meta:   make(map[string]sync.PageMeta, len(listing)),
	}
	for _, m := range listing {
		live.remote[m.Path] = m.SHA
		live.meta[m.Path] = m
	}
	exists := true
	doc.Auth.State = contract.AuthAuthenticated
	doc.Remote = contract.RemoteInfo{
		Source:     contract.RemoteLive,
		FetchedAt:  contract.TimePtr(now),
		RepoExists: &exists,
		Head:       statusHead(listing),
		PageCount:  len(listing),
	}
	res, err := statusEnsureBase(ctx, env, EnsureBaseOptions{Listing: listing, Locked: false})
	if res != nil {
		live.warnings = res.Warnings
	}
	live.ensureErr = err
	return live
}

// statusRemoteFailure records a failed listing and returns the remote view
// status continues with. A missing repo is a live answer (R2): every path
// is absent and, with nothing on the server to repair from, EnsureBase is
// not called. Any other failure becomes remote.error with the E9 offline
// rule and no EnsureBase: a 401 rejects the credentials (R4), a server_error
// may be a proxy page without Contexo headers, so the capabilities become
// unknown (R3), and the rest leave auth unchecked (R5).
func statusRemoteFailure(doc *contract.StatusDoc, lerr error, now time.Time) statusLive {
	if errors.Is(lerr, sync.ErrRepoNotFound) {
		exists := false
		doc.Auth.State = contract.AuthAuthenticated
		doc.Remote = contract.RemoteInfo{
			Source:     contract.RemoteLive,
			FetchedAt:  contract.TimePtr(now),
			RepoExists: &exists,
		}
		return statusLive{remote: map[string]string{}, meta: map[string]sync.PageMeta{}}
	}

	ce := ui.ToContractError(lerr)
	doc.Auth.State = contract.AuthUnchecked
	switch ce.Code {
	case contract.CodeAuthRejected:
		doc.Auth.State = contract.AuthRejected
		ce.Advice = []string{"ctx auth login"}
	case contract.CodeServerError:
		doc.Server.Version = ""
		doc.Server.CapabilitiesKnown = false
		doc.Server.Capabilities = []contract.ServerCapability{}
	}
	body := ce.Body()
	doc.Remote.Error = &body
	return statusLive{}
}

// statusServerOutdated is E13's server_outdated: the server's capabilities
// are known and lack one a v1 client relies on.
func statusServerOutdated(serverURL string, s contract.ServerInfo) (contract.Warning, bool) {
	if !s.CapabilitiesKnown {
		return contract.Warning{}, false
	}
	var missing []string
	for _, c := range []contract.ServerCapability{contract.SrvPagesAt, contract.SrvExpectAbsent, contract.SrvWritePathSHA} {
		if !slices.Contains(s.Capabilities, c) {
			missing = append(missing, string(c))
		}
	}
	if len(missing) == 0 {
		return contract.Warning{}, false
	}
	if serverURL == "" {
		serverURL = "the server"
	}
	return contract.Warning{
		Code:    contract.WarnServerOutdated,
		Message: serverURL + " does not advertise " + strings.Join(missing, ", ") + "; update the Contexo server",
		Paths:   []string{},
	}, true
}

// statusRepairMessage is status's own base_repair_skipped message, for n
// unverified pages and EnsureBase's error (nil when it did not fail).
func statusRepairMessage(n int, err error) string {
	msg := fmt.Sprintf("%d pages could not be verified against the server", n)
	if n == 1 {
		msg = "1 page could not be verified against the server"
	}
	switch {
	case err == nil:
		return msg
	case n == 0:
		return "base repair failed: " + err.Error()
	}
	return msg + ": base repair failed: " + err.Error()
}

// statusMergeWarnings merges warnings by code (R11): the first warning of a
// code keeps its position and message and gets the sorted union of every
// same-code warning's paths (never nil).
func statusMergeWarnings(ws []contract.Warning) []contract.Warning {
	out := make([]contract.Warning, 0, len(ws))
	at := map[contract.WarningCode]int{}
	for _, w := range ws {
		i, seen := at[w.Code]
		if !seen {
			at[w.Code] = len(out)
			w.Paths = append([]string{}, w.Paths...)
			out = append(out, w)
			continue
		}
		out[i].Paths = append(out[i].Paths, w.Paths...)
	}
	for i := range out {
		slices.Sort(out[i].Paths)
		out[i].Paths = slices.Compact(out[i].Paths)
	}
	return out
}

// statusHead is remote.head (R1): the sha of the listing entry with the
// newest time. ListPages carries no repo HEAD, so it is "" when the listing
// is empty or the newest time is shared by different shas.
func statusHead(listing []sync.PageMeta) string {
	head, ambiguous := "", false
	var newest time.Time
	for i, m := range listing {
		switch {
		case i == 0 || m.Time.After(newest):
			head, newest, ambiguous = m.SHA, m.Time, false
		case m.Time.Equal(newest) && m.SHA != head:
			ambiguous = true
		}
	}
	if ambiguous {
		return ""
	}
	return head
}

// statusEntry builds the PageEntry of one classified path. Frontmatter
// fields come only from a valid working file; a remote side from a sidecar
// (or without a listing entry) carries only its sha (E9).
func statusEntry(f *syncstate.PathFacts, res contract.ClassifyResult, meta map[string]sync.PageMeta) contract.PageEntry {
	e := contract.PageEntry{
		Path:         f.Path,
		State:        res.State,
		ConflictKind: res.ConflictKind,
		Tags:         []string{},
		Notes:        res.Notes,
	}
	if e.Notes == nil {
		e.Notes = []contract.NoteCode{}
	}
	if lf := f.File; lf != nil {
		e.Local = &contract.LocalSide{File: lf.Path, Hash: lf.Hash}
		if inv := lf.Invalid; inv != nil {
			e.Invalid = &contract.InvalidInfo{
				Stage:        contract.InvalidStage(inv.Stage),
				Message:      inv.Message,
				ExpectedPath: inv.ExpectedPath,
			}
		} else if lf.Page != nil {
			fm := lf.Page.Frontmatter
			e.Type = string(fm.Type)
			e.Slug = fm.Slug
			e.Title = statusTitle(lf.Page.Body)
			e.Tags = append(e.Tags, fm.Tags...)
			e.Author = fm.Author
			e.Agent = fm.Agent
			if !fm.Updated.IsZero() {
				e.Updated = contract.TimePtr(fm.Updated)
			}
			e.ReasoningSummary = fm.ReasoningSummary
		}
	}
	switch f.Base.Kind {
	case syncstate.BaseValid:
		e.Base = &contract.BaseSide{SHA: f.Base.SHA, Hash: f.Base.Hash}
		if f.BaseRecord != nil && !f.BaseRecord.SyncedAt.IsZero() {
			e.Base.SyncedAt = contract.TimePtr(f.BaseRecord.SyncedAt)
		}
	case syncstate.BaseStale:
		e.Base = &contract.BaseSide{SHA: f.Base.SHA, Stale: true}
	}
	if f.Remote.Kind == syncstate.RemotePresent {
		e.Remote = &contract.RemoteSide{SHA: f.Remote.SHA}
		if m, ok := meta[f.Path]; ok && !f.RemoteFromSidecar {
			e.Remote.Author, e.Remote.Email, e.Remote.Message = m.Author, m.Email, m.Message
			if !m.Time.IsZero() {
				e.Remote.Time = contract.TimePtr(m.Time)
			}
		}
	}
	return e
}

// statusTitle is the text of the body's first "# " line, or "".
func statusTitle(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if t, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

// statusCount adds one page of state s to c.
func statusCount(c *contract.StateCounts, s contract.PageState) {
	switch s {
	case contract.StateUnchanged:
		c.Unchanged++
	case contract.StateModified:
		c.Modified++
	case contract.StateNew:
		c.New++
	case contract.StateIncoming:
		c.Incoming++
	case contract.StateIncomingNew:
		c.IncomingNew++
	case contract.StateConflict:
		c.Conflict++
	case contract.StateDeletedLocal:
		c.DeletedLocal++
	case contract.StateInvalid:
		c.Invalid++
	case contract.StateUnverified:
		c.Unverified++
	}
}

// FillStatusIdentity fills the server, project and auth identity of doc from
// the resolved remote r, and remote.error from unusable (E22).
func FillStatusIdentity(doc *contract.StatusDoc, r *config.Remote, unusable error) {}

// statusBlocksPush is E24's captures.blocks_push: the guard requires a
// distill and CONTEXO_DISTILL_DISABLE does not bypass it. It reads the env
// var itself until WP-1.2's GuardResult.Bypassed lands.
func statusBlocksPush(g *GuardResult) bool {
	return g != nil && g.Required && os.Getenv("CONTEXO_DISTILL_DISABLE") != "1"
}
