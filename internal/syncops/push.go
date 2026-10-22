package syncops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
	"github.com/sugihAF/contexo/internal/ui"
)

// Owner: WP-1.2. Fields may be added, never renamed or removed.

// PushOptions configures Push. Resolved lists paths to resolve with --ours
// semantics before pushing (MCP ctx_push resolved=[...]).
type PushOptions struct {
	Paths     []string
	Message   string
	DryRun    bool
	NoDistill bool
	Resolved  []string
	// Feature, Tag and Type narrow the batch built without Paths to pages
	// whose local frontmatter has the tag (Feature wins over Tag) and the
	// type, compared case-insensitively. They cannot be combined with Paths.
	Feature string
	Tag     string
	Type    string
	// Confirm, when set, is called under the lock with the files about to be
	// sent, after the distill guard passed and before anything is sent (also
	// on a dry run). Returning false stops the push with ErrPushDeclined, an
	// error stops it with that error; either way nothing is sent or written.
	// Human `ctx push` uses it for its preview and prompt; --json and MCP
	// leave it nil.
	Confirm func(plan []PushPlanFile) (bool, error)
}

// PushPlanFile is one file Push is about to send: the request file and
// whether it creates the page on the server or updates it.
type PushPlanFile struct {
	Kind contract.WriteKind
	File sync.PushFile
}

// ErrPushDeclined is returned when PushOptions.Confirm declines the batch.
var ErrPushDeclined = errors.New("push: declined")

// Test seams. pushResolveFn runs the Resolved pre-step outside the lock;
// pushEnsureBase repairs bases under the lock (E5).
var (
	pushResolveFn  = Resolve
	pushEnsureBase = EnsureBase
)

// pushEntry is one classified view path. overlay is true when the E9
// sidecar overlay turned a modified result into a conflict.
type pushEntry struct {
	facts   *syncstate.PathFacts
	state   contract.PageState
	overlay bool
}

// pushItem is one file of the batch: the request file, its write kind and
// the exact bytes sent (they become the base on success, E6).
type pushItem struct {
	kind contract.WriteKind
	file sync.PushFile
	sent []byte
}

// pushFilter is the lower-cased, trimmed --feature/--tag (tag) and --type
// (typ) filter; empty fields do not filter.
type pushFilter struct {
	tag, typ string
}

// Push sends the changed pages and builds the `ctx push --json` document.
//
// Order (E5, binding): validate the options and paths -> the Resolved
// pre-step (outside the lock) -> Lock -> ListPages -> EnsureBase(Locked) ->
// LoadView + classify (E9 overlay) -> batch -> CheckGuard -> Confirm ->
// PushPages -> writes. Nothing is sent before the guard passes and Confirm
// agrees; a dry run, a refusal and a failed send write nothing. A 409 for a
// path becomes a push sidecar and never moves page_shas or the base. When
// conflicts remain the document is returned together with a conflict error.
func Push(ctx context.Context, env Env, o PushOptions) (*contract.PushDoc, error) {
	if _, err := RequireHub(env.Root); err != nil {
		return nil, err
	}
	filter := pushFilterOf(o)
	if len(o.Paths) > 0 && filter.active() {
		return nil, New(contract.CodeUsage,
			"push: --feature, --tag and --type cannot be combined with named paths")
	}
	paths, err := pushNormalizePaths(env, o.Paths)
	if err != nil {
		return nil, err
	}
	resolved, err := pushNormalizePaths(env, o.Resolved)
	if err != nil {
		return nil, err
	}
	if !env.Online() {
		return nil, New(contract.CodeInternal, "push: no server configured")
	}
	now := time.Now
	if env.Now != nil {
		now = env.Now
	}

	// The Resolved pre-step takes its own lock, so it runs before ours.
	for _, p := range resolved {
		_, err := pushResolveFn(ctx, env, ResolveOptions{Path: p, Strategy: contract.ResolveOurs})
		if err = pushResolvedError(env, p, err); err != nil {
			return nil, err
		}
	}

	unlock, err := syncstate.Lock(env.HubDir, "push", syncstate.DefaultLockWait())
	if err != nil {
		var busy *syncstate.BusyError
		if errors.As(err, &busy) {
			return nil, pushBusyError(busy, err)
		}
		return nil, Wrap(err, contract.CodeInternal, "push")
	}
	defer unlock()

	listing, err := pushListing(env)
	if err != nil {
		return nil, err
	}
	doc := &contract.PushDoc{
		Schema:    contract.SchemaPush,
		DryRun:    o.DryRun,
		ServerURL: env.ServerURL,
		RepoID:    env.RepoID,
		Pushed:    []contract.PushedPage{},
		Conflicts: []contract.PushConflict{},
		Skipped:   []contract.SkippedPage{},
		Warnings:  []contract.Warning{},
	}
	if !o.DryRun {
		// E8: a dry run writes nothing, so it skips the base repair.
		res, err := pushEnsureBase(ctx, env, EnsureBaseOptions{Listing: listing, Locked: true})
		switch {
		case err != nil && ctx.Err() != nil:
			return nil, ctx.Err()
		case err != nil:
			doc.Warnings = append(doc.Warnings, contract.Warning{
				Code:    contract.WarnBaseRepairSkipped,
				Message: "push: base repair skipped: " + err.Error(),
				Paths:   []string{},
			})
		case res != nil:
			doc.Warnings = append(doc.Warnings, pushWarnings(res.Warnings)...)
		}
	}

	remote := make(map[string]string, len(listing))
	for _, p := range listing {
		remote[p.Path] = p.SHA
	}
	view, err := syncstate.LoadView(env.HubDir, remote)
	if err != nil {
		return nil, Wrap(err, contract.CodeInternal, "push")
	}
	if len(view.Corrupt) > 0 {
		doc.Warnings = append(doc.Warnings, contract.Warning{
			Code: contract.WarnStateUnreadable,
			Message: "push: " + strings.Join(view.Corrupt, ", ") +
				" could not be read and was treated as empty; pages without a base are sent as new",
			Paths: []string{},
		})
	}
	entries, err := pushClassify(view)
	if err != nil {
		return nil, err
	}
	var batch []*pushItem
	if len(paths) > 0 {
		batch, doc.Skipped = pushBatchNamed(entries, paths)
	} else {
		batch, doc.Skipped = pushBatchAll(view.Paths, entries, filter)
	}

	// The guard always runs, also for an empty batch (R10).
	batchPaths := make([]string, len(batch))
	for i, it := range batch {
		batchPaths[i] = it.file.Path
	}
	g, err := CheckGuard(env, batchPaths)
	if err != nil {
		return nil, err
	}
	doc.Distill = contract.DistillCheck{Required: g.Required, Bypassed: g.Bypassed || o.NoDistill}
	if g.Required && !g.Bypassed && !o.NoDistill {
		return nil, pushDistillError(g)
	}

	doc.Message = o.Message
	if doc.Message == "" {
		doc.Message = fmt.Sprintf("ctx push (%d pages)", len(batch))
	}
	if len(batch) == 0 {
		return doc, nil
	}
	if o.Confirm != nil {
		plan := make([]PushPlanFile, len(batch))
		for i, it := range batch {
			plan[i] = PushPlanFile{Kind: it.kind, File: it.file}
		}
		ok, err := o.Confirm(plan)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrPushDeclined
		}
	}
	if o.DryRun {
		for _, it := range batch {
			doc.Pushed = append(doc.Pushed, contract.PushedPage{Path: it.file.Path, Kind: it.kind})
		}
		return doc, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	files := make([]sync.PushFile, len(batch))
	sent := make(map[string]*pushItem, len(batch))
	for i, it := range batch {
		files[i] = it.file
		sent[it.file.Path] = it
	}
	resp, err := env.Remote.PushPages(env.RepoID, &sync.PushRequest{
		AuthorName:  env.Author.Name,
		AuthorEmail: env.Author.Email,
		Message:     doc.Message,
		Files:       files,
	})
	if err != nil {
		return nil, ui.ToContractError(fmt.Errorf("push: %w", err))
	}
	if resp == nil {
		return nil, New(contract.CodeInternal, "push: the server returned no push result")
	}
	pushed, conflicts, err := pushApply(env, view, sent, resp, now())
	if err != nil {
		return nil, Wrap(err, contract.CodeInternal,
			"push: the server accepted the push but the local sync state could not be fully saved")
	}
	doc.NewHead = resp.NewHead
	doc.Pushed = pushed
	doc.Conflicts = conflicts
	if len(conflicts) == 0 {
		return doc, nil
	}
	return doc, pushConflictError(conflicts)
}

// pushNormalizePaths turns user path arguments into contract paths (R3),
// dropping duplicates and keeping first-seen order. A path escaping the hub
// or not naming a .md page is invalid_path.
func pushNormalizePaths(env Env, args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	seen := make(map[string]bool, len(args))
	for _, a := range args {
		rel, err := syncstate.NormalizeRelPath(env.Root, env.HubDir, a)
		if err != nil {
			return nil, Wrap(err, contract.CodeInvalidPath, "push")
		}
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	return out, nil
}

// pushResolvedError decides whether a Resolved pre-step error aborts the
// push; nil means continue. Tolerated: not_resolvable for a path already
// modified, new or unchanged (nothing left to resolve), and page_not_found
// for a genuinely new local page (a valid working file and no base), which
// the push then sends with expect_absent (WP-1.4: Resolve reads the server
// page first). A busy error without a contract error in the chain is push's
// own busy. Every other error is returned as is.
func pushResolvedError(env Env, rel string, err error) error {
	if err == nil {
		return nil
	}
	var ce *contract.Error
	if errors.As(err, &ce) {
		switch ce.Code {
		case contract.CodeNotResolvable:
			switch pushNotResolvableState(ce.Details) {
			case contract.StateModified, contract.StateNew, contract.StateUnchanged:
				return nil
			}
		case contract.CodePageNotFound:
			if pushLocalNewPage(env.HubDir, rel) {
				return nil
			}
		}
		return err
	}
	var busy *syncstate.BusyError
	if errors.As(err, &busy) {
		return pushBusyError(busy, err)
	}
	return err
}

// pushNotResolvableState returns the state of not_resolvable details given
// as a value or a pointer, or "".
func pushNotResolvableState(details any) contract.PageState {
	switch d := details.(type) {
	case contract.NotResolvableDetails:
		return d.State
	case *contract.NotResolvableDetails:
		if d != nil {
			return d.State
		}
	}
	return ""
}

// pushLocalNewPage reports whether rel is a page the project has never
// synced: state.json has no page_shas entry for it and its working file is
// a valid page at its own path (the ScanLocal rules). It only reads.
func pushLocalNewPage(hub, rel string) bool {
	st, _, err := sync.LoadStateOrEmpty(hub)
	if err != nil || st.PageSHAs[rel] != "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(hub, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	p, err := schema.ParsePage(syncstate.StripBOM(data))
	if err != nil {
		return false
	}
	return p.Frontmatter.Validate() == nil && p.Frontmatter.RelPath() == rel
}

// pushBusyError is the busy contract error (exit 12) for a lock holder.
func pushBusyError(busy *syncstate.BusyError, err error) *contract.Error {
	return &contract.Error{
		Code:    contract.CodeBusy,
		Message: "push: " + busy.Error(),
		Details: contract.BusyDetails{
			PID:       busy.Holder.PID,
			Host:      busy.Holder.Host,
			Command:   busy.Holder.Command,
			StartedAt: contract.TimeOf(busy.Holder.StartedAt),
		},
		Err: err,
	}
}

// pushListing fetches the live listing. A repo the server does not have
// yet answers 404 repo_not_found (legacy) or 403 forbidden (hosted, a
// signed-in user's listing of a missing repo): the first push creates it,
// so both give an empty listing (R1). Never nil on success.
func pushListing(env Env) ([]sync.PageMeta, error) {
	listing, err := env.Remote.ListPages(env.RepoID)
	if err != nil {
		switch ui.ToContractError(err).Code {
		case contract.CodeRepoNotFound, contract.CodeForbidden:
			return []sync.PageMeta{}, nil
		}
		return nil, ui.ToContractError(fmt.Errorf("push: %w", err))
	}
	if listing == nil {
		listing = []sync.PageMeta{}
	}
	return listing, nil
}

// pushWarnings returns ws with every nil Paths made empty.
func pushWarnings(ws []contract.Warning) []contract.Warning {
	out := make([]contract.Warning, len(ws))
	for i, w := range ws {
		if w.Paths == nil {
			w.Paths = []string{}
		}
		out[i] = w
	}
	return out
}

// pushClassify classifies every view path: contract.ClassifySides, then the
// E9 sidecar overlay.
func pushClassify(view *syncstate.View) (map[string]pushEntry, error) {
	out := make(map[string]pushEntry, len(view.Paths))
	for _, rel := range view.Paths {
		f := view.Facts[rel]
		raw, err := contract.ClassifySides(contract.Side(f.Local), contract.Side(f.Base), contract.Side(f.Remote))
		if err != nil {
			return nil, Wrap(err, contract.CodeInternal, "push: classify "+rel)
		}
		var sc *SidecarFact
		if f.Conflict != nil {
			sc = &SidecarFact{ServerSHA: f.Conflict.ServerSHA, Kind: contract.ConflictKind(f.Conflict.Kind)}
		}
		res := ApplySidecar(raw, f.Base.SHA, sc)
		out[rel] = pushEntry{facts: f, state: res.State, overlay: res.State != raw.State}
	}
	return out, nil
}

// pushBatchAll builds the batch without named paths: every modified or new
// page matching the filter. skipped lists only the blocking states
// (conflict, invalid, deleted_local, unverified) that match the filter,
// plus sendable pages whose Normalize fails (R6). Both are sorted by path.
func pushBatchAll(paths []string, entries map[string]pushEntry, filter pushFilter) ([]*pushItem, []contract.SkippedPage) {
	batch := []*pushItem{}
	skipped := []contract.SkippedPage{}
	for _, rel := range paths {
		e := entries[rel]
		switch e.state {
		case contract.StateModified, contract.StateNew:
			if !pushMatchFilter(e.facts.File, filter) {
				continue
			}
			if it, ok := pushItemOf(rel, e); ok {
				batch = append(batch, it)
			} else {
				skipped = append(skipped, contract.SkippedPage{Path: rel, State: e.state, Reason: contract.SkipInvalid})
			}
		case contract.StateConflict, contract.StateInvalid, contract.StateDeletedLocal, contract.StateUnverified:
			if pushMatchFilter(e.facts.File, filter) {
				skipped = append(skipped, pushSkip(rel, e))
			}
		}
	}
	pushSortBatch(batch, skipped)
	return batch, skipped
}

// pushBatchNamed builds the batch for named paths: modified and new paths
// are sent; every other path is skipped with its state and reason, and a
// path unknown locally and remotely is skipped not_found. Both lists are
// sorted by path.
func pushBatchNamed(entries map[string]pushEntry, paths []string) ([]*pushItem, []contract.SkippedPage) {
	batch := []*pushItem{}
	skipped := []contract.SkippedPage{}
	for _, rel := range paths {
		e, ok := entries[rel]
		switch {
		case !ok || e.state == "":
			skipped = append(skipped, contract.SkippedPage{Path: rel, Reason: contract.SkipNotFound})
		case e.state == contract.StateModified || e.state == contract.StateNew:
			if it, ok := pushItemOf(rel, e); ok {
				batch = append(batch, it)
			} else {
				skipped = append(skipped, contract.SkippedPage{Path: rel, State: e.state, Reason: contract.SkipInvalid})
			}
		default:
			skipped = append(skipped, pushSkip(rel, e))
		}
	}
	pushSortBatch(batch, skipped)
	return batch, skipped
}

// pushItemOf builds the request file of a modified or new entry (E6, E7):
// content Normalize(local), parent_sha the base sha ("" for none),
// expect_absent iff there is no base. ok is false when the page does not
// normalize (a raw-sha256 hash, R6) and cannot be sent.
func pushItemOf(rel string, e pushEntry) (*pushItem, bool) {
	f := e.facts
	if f.File == nil {
		return nil, false
	}
	norm, err := syncstate.Normalize(f.File.Data)
	if err != nil {
		return nil, false
	}
	kind := contract.WriteUpdate
	if e.state == contract.StateNew {
		kind = contract.WriteCreate
	}
	return &pushItem{
		kind: kind,
		file: sync.PushFile{
			Path:         rel,
			Content:      string(norm),
			ParentSHA:    f.Base.SHA,
			ExpectAbsent: f.Base.Kind == syncstate.BaseNone,
		},
		sent: norm,
	}, true
}

// pushSkip is the skipped entry of a path that is not sent. ServerSHA (R7):
// a conflict carries the sidecar's server sha when the E9 overlay made it
// one, otherwise the live remote sha; incoming and incoming_new carry the
// live remote sha; other reasons have none.
func pushSkip(rel string, e pushEntry) contract.SkippedPage {
	s := contract.SkippedPage{Path: rel, State: e.state}
	live := ""
	if e.facts.Remote.Kind == syncstate.RemotePresent {
		live = e.facts.Remote.SHA
	}
	switch e.state {
	case contract.StateUnchanged:
		s.Reason = contract.SkipUnchanged
	case contract.StateIncoming, contract.StateIncomingNew:
		// R5: incoming_new has no reason of its own.
		s.Reason, s.ServerSHA = contract.SkipIncoming, live
	case contract.StateConflict:
		s.Reason, s.ServerSHA = contract.SkipConflict, live
		if e.overlay && e.facts.Conflict != nil {
			s.ServerSHA = e.facts.Conflict.ServerSHA
		}
	case contract.StateInvalid:
		s.Reason = contract.SkipInvalid
	case contract.StateDeletedLocal:
		s.Reason = contract.SkipDeletedLocal
	case contract.StateUnverified:
		s.Reason = contract.SkipUnverified
	default:
		s.Reason = contract.SkipNotFound
	}
	return s
}

func pushSortBatch(batch []*pushItem, skipped []contract.SkippedPage) {
	sort.Slice(batch, func(i, j int) bool { return batch[i].file.Path < batch[j].file.Path })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Path < skipped[j].Path })
}

// pushFilterOf returns the batch filter of o: Feature wins over Tag.
func pushFilterOf(o PushOptions) pushFilter {
	tag := strings.ToLower(strings.TrimSpace(o.Feature))
	if tag == "" {
		tag = strings.ToLower(strings.TrimSpace(o.Tag))
	}
	return pushFilter{tag: tag, typ: strings.ToLower(strings.TrimSpace(o.Type))}
}

func (f pushFilter) active() bool { return f.tag != "" || f.typ != "" }

// pushMatchFilter reports whether the working file matches the filter, on
// its local frontmatter (the former cli applyFilters). Without a filter
// everything matches; with one, a path without parsed local frontmatter
// (parse-invalid, deleted_local, incoming_new) never does (R4).
func pushMatchFilter(lf *syncstate.LocalFile, f pushFilter) bool {
	if !f.active() {
		return true
	}
	if lf == nil || lf.Page == nil {
		return false
	}
	fm := lf.Page.Frontmatter
	if f.typ != "" && strings.ToLower(string(fm.Type)) != f.typ {
		return false
	}
	if f.tag == "" {
		return true
	}
	for _, t := range fm.Tags {
		if strings.ToLower(t) == f.tag {
			return true
		}
	}
	return false
}

// pushDistillError is the D4 refusal (exit 9). Its envelope advice stays
// empty; the human hints are the CLI's.
func pushDistillError(g *GuardResult) *contract.Error {
	n := len(g.Details.Sessions)
	pending := fmt.Sprintf("%d un-distilled agent sessions are pending", n)
	if n == 1 {
		pending = "1 un-distilled agent session is pending"
	}
	return &contract.Error{
		Code: contract.CodeDistillRequired,
		Message: "push: " + pending +
			"; share these pages through your agent so the reasoning trail is captured",
		Details: g.Details,
	}
}

// pushConflictError is the exit-6 error returned with the document when
// conflicts remain; conflicts is sorted by path.
func pushConflictError(conflicts []contract.PushConflict) *contract.Error {
	paths := make([]string, len(conflicts))
	advice := make([]string, 0, 2*len(conflicts))
	for i, c := range conflicts {
		paths[i] = c.Path
		advice = append(advice, "ctx resolve "+c.Path+" --ours", "ctx resolve "+c.Path+" --theirs")
	}
	return &contract.Error{
		Code:       contract.CodeConflict,
		Message:    fmt.Sprintf("push: %d conflict(s) remain; other pages were pushed", len(conflicts)),
		Advice:     advice,
		HTTPStatus: 409,
		Details:    contract.ConflictDetails{Paths: paths},
	}
}

// pushApply records the server's answer (E8, E10) in the write order base
// files, conflicts/<path> files, bases.json, conflicts.json, state.json,
// each index written at most once and only when it changed. Only paths
// that were sent count. A pushed path with a sha gets base = the exact sent
// bytes, page_shas = sha and its sidecar cleared; a 409 gets a push sidecar
// with the sanitized server content and never touches page_shas or the
// base. A write failure does not stop the remaining writes; the first one
// is returned. pushed and conflicts are sorted by path.
func pushApply(env Env, view *syncstate.View, sent map[string]*pushItem, resp *sync.PushResponse, now time.Time) ([]contract.PushedPage, []contract.PushConflict, error) {
	pushed := []contract.PushedPage{}
	conflicts := []contract.PushConflict{}
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	seen := map[string]bool{}
	recorded := map[string]string{}
	var recordedPaths []string
	basesChanged, conflictsChanged := false, false

	for _, p := range resp.Pushed {
		it, ok := sent[p.Path]
		if !ok || seen[p.Path] {
			continue
		}
		seen[p.Path] = true
		pushed = append(pushed, contract.PushedPage{Path: p.Path, Kind: it.kind, SHA: p.SHA})
		if p.SHA == "" {
			continue
		}
		recorded[p.Path] = p.SHA
		recordedPaths = append(recordedPaths, p.Path)
		if err := view.Bases.Put(p.Path, it.sent, p.SHA, now); err != nil {
			keep(fmt.Errorf("base of %s: %w", p.Path, err))
			continue
		}
		basesChanged = true
	}

	for _, c := range resp.Conflicts {
		it, ok := sent[c.Path]
		if !ok || seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		pc := contract.PushConflict{Path: c.Path, Kind: contract.ConflictBothModified,
			BaseSHA: it.file.ParentSHA, ServerSHA: c.CurrentSHA}
		if it.file.ExpectAbsent {
			pc.Kind, pc.BaseSHA = contract.ConflictBothAdded, ""
		}
		conflicts = append(conflicts, pc)
		rec := syncstate.ConflictRecord{
			Path:       c.Path,
			ServerSHA:  c.CurrentSHA,
			BaseSHA:    pc.BaseSHA,
			Kind:       string(pc.Kind),
			Source:     syncstate.SourcePush,
			DetectedAt: contract.TimeOf(now),
		}
		if err := view.Conflicts.Put(rec, []byte(schema.SanitizeContent(string(c.CurrentContent)))); err != nil {
			keep(fmt.Errorf("conflict sidecar of %s: %w", c.Path, err))
			continue
		}
		conflictsChanged = true
	}
	for _, rel := range recordedPaths {
		if _, ok := view.Conflicts.Get(rel); !ok {
			continue
		}
		if err := view.Conflicts.Clear(rel); err != nil {
			keep(fmt.Errorf("clear conflict sidecar of %s: %w", rel, err))
			continue
		}
		conflictsChanged = true
	}

	if basesChanged {
		keep(view.Bases.Save())
	}
	if conflictsChanged {
		keep(view.Conflicts.Save())
	}
	if len(recorded) > 0 {
		// A path whose base write failed still moves to the new sha: its
		// base is then stale, and EnsureBase re-fetches it.
		for rel, sha := range recorded {
			view.State.PageSHAs[rel] = sha
		}
		keep(sync.SaveState(env.HubDir, view.State))
	}

	sort.Slice(pushed, func(i, j int) bool { return pushed[i].Path < pushed[j].Path })
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Path < conflicts[j].Path })
	return pushed, conflicts, firstErr
}
