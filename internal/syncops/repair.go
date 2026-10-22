package syncops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// Owner: WP-1.5. Fields may be added, never renamed or removed.

// Base repair limits (E12).
const (
	repairDefaultBudget  = 50 // ReadPageAt calls started per invocation (phase 2 only)
	repairMaxConcurrency = 4  // requests in flight at once, in every fan-out
	repairPullThreshold  = 8  // more HEAD-equal stale paths than this: one PullPages
)

// RepairedPage.Reason values.
const (
	// The base is the server page at the path's own last commit, which
	// replaces the page_shas value (the old repo-HEAD gitstore bug).
	repairReasonPathSHAFixed = "path_sha_fixed"
	// E11 (3): an old binary bumped page_shas past a still valid base; the
	// base and its sha are kept.
	repairReasonKeptPreviousBase = "kept_previous_base"
)

// RepairSkipped.Reason values besides the contract error code of a failed
// request.
const (
	// No pages.at capability, or a ?at answer without X-Page-SHA.
	repairSkipServerUnsupported = string(contract.CodeServerUnsupported)
	// The request budget or the status time budget ran out first.
	repairSkipBudgetExhausted = "budget_exhausted"
	// page_shas changed between the load and the write (an old binary
	// takes no lock).
	repairSkipStateChanged = "state_changed"
	// EnsureBase only: another process holds .sync/lock.
	repairSkipLockBusy = "lock_busy"
)

// RepairOptions configures Repair. Budget caps the ReadPageAt calls (<= 0
// means 50) and Concurrency the requests in flight (<= 0 or > 4 means 4).
type RepairOptions struct {
	DryRun      bool
	Budget      int
	Concurrency int
}

// RepairResult is stdout of `ctx repair --json` (schema ctx.repair.v1). Keys
// are in E18 order; arrays are never null (build it with NewRepairResult).
type RepairResult struct {
	Schema             string                      `json:"schema"`
	DryRun             bool                        `json:"dry_run"`
	ServerCapabilities []contract.ServerCapability `json:"server_capabilities"`
	Backfilled         []RepairBackfilled          `json:"backfilled"`
	Repaired           []RepairedPage              `json:"repaired"`
	Skipped            []RepairSkipped             `json:"skipped"`
	Unverified         []string                    `json:"unverified"`
	Warnings           []contract.Warning          `json:"warnings"`
}

// RepairBackfilled is a path whose missing base was written.
type RepairBackfilled struct {
	Path string `json:"path"`
	SHA  string `json:"sha"`
}

// RepairedPage is a path whose state sha was rewritten.
type RepairedPage struct {
	Path   string `json:"path"`
	OldSHA string `json:"old_sha"`
	NewSHA string `json:"new_sha"`
	Reason string `json:"reason"`
}

// RepairSkipped is a path repair left alone.
type RepairSkipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// NewRepairResult returns an empty result with every array non-nil.
func NewRepairResult(dryRun bool) *RepairResult {
	return &RepairResult{
		Schema:             contract.SchemaRepair,
		DryRun:             dryRun,
		ServerCapabilities: []contract.ServerCapability{},
		Backfilled:         []RepairBackfilled{},
		Repaired:           []RepairedPage{},
		Skipped:            []RepairSkipped{},
		Unverified:         []string{},
		Warnings:           []contract.Warning{},
	}
}

// Repair runs E11/E12 base repair over every stale path: it holds
// .sync/lock (busy after CONTEXO_LOCK_TIMEOUT), lists the repo, fetches what
// the stale bases need and, unless o.DryRun, writes bases, sidecars and
// state. A failed request skips paths (reported, with a base_repair_skipped
// warning) and is not an error; errors are the lock, the listing, local
// I/O and ctx. It never writes a working file.
func Repair(ctx context.Context, env Env, o RepairOptions) (*RepairResult, error) {
	if env.Remote == nil {
		return nil, contract.New(contract.CodeNotAuthenticated, "repair: not logged in", "ctx auth login")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	unlock, err := syncstate.Lock(env.HubDir, "repair", syncstate.DefaultLockWait())
	if err != nil {
		return nil, err
	}
	defer unlock()

	listing, err := env.Remote.ListPages(env.RepoID)
	if err != nil {
		return nil, fmt.Errorf("repair: list pages: %w", err)
	}
	// Read once, right after the listing answered: a later answer without
	// the capability headers (a proxy error) must not change the decision.
	_, caps, known := env.Remote.ServerInfo()
	v, stale, err := repairLoad(env.HubDir, nil)
	if err != nil {
		return nil, err
	}
	plan := ensureBasePlan(ctx, env, v, listing, stale, repairBudgetOf(o), caps, known)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !o.DryRun {
		if err := repairApply(env, v, plan); err != nil {
			return nil, err
		}
	}

	res := NewRepairResult(o.DryRun)
	if known {
		res.ServerCapabilities = append(res.ServerCapabilities, caps...)
	}
	for _, a := range plan.actions {
		if a.reason == "" {
			res.Backfilled = append(res.Backfilled, RepairBackfilled{Path: a.path, SHA: a.oldSHA})
			continue
		}
		res.Repaired = append(res.Repaired, RepairedPage{
			Path: a.path, OldSHA: a.oldSHA, NewSHA: a.newSHA, Reason: a.reason,
		})
	}
	for _, p := range repairSortedKeys(plan.skipped) {
		res.Skipped = append(res.Skipped, RepairSkipped{Path: p, Reason: plan.skipped[p]})
		res.Unverified = append(res.Unverified, p)
	}
	if len(plan.skipped) > 0 {
		res.Warnings = append(res.Warnings, repairWarning(plan.skipped))
	}
	return res, nil
}

// repairBudget bounds the network phase: at most max ReadPageAt calls are
// started and at most conc requests are in flight.
type repairBudget struct{ max, conc int }

// repairBudgetOf applies the RepairOptions defaults.
func repairBudgetOf(o RepairOptions) repairBudget {
	b := repairBudget{max: o.Budget, conc: o.Concurrency}
	if b.max <= 0 {
		b.max = repairDefaultBudget
	}
	if b.conc <= 0 || b.conc > repairMaxConcurrency {
		b.conc = repairMaxConcurrency
	}
	return b
}

// repairLoad loads the view of hub (local only, no requests) and returns
// it with its stale paths, sorted: every entry with a page_shas value, in
// filter when filter is non-empty, whose base is not valid or no longer
// verifies (base file missing or edited). An error is an I/O error.
func repairLoad(hub string, filter []string) (*syncstate.View, []string, error) {
	v, err := syncstate.LoadView(hub, nil)
	if err != nil {
		return nil, nil, err
	}
	var only map[string]bool
	if len(filter) > 0 {
		only = make(map[string]bool, len(filter))
		for _, p := range filter {
			only[p] = true
		}
	}
	stale := []string{}
	for _, p := range v.Paths {
		f := v.Facts[p]
		if f == nil || v.State.PageSHAs[p] == "" || (only != nil && !only[p]) {
			continue
		}
		switch f.Base.Kind {
		case syncstate.BaseStale:
		case syncstate.BaseValid:
			if _, err := v.Bases.ReadVerified(p); err == nil {
				continue
			}
		default:
			continue
		}
		stale = append(stale, p)
	}
	return v, stale, nil
}

// repairAction is the E11 decision for one stale path: what apply writes.
type repairAction struct {
	path   string
	oldSHA string // page_shas[path] when the view was loaded (S)
	newSHA string // page_shas[path] after apply
	reason string // "" for a backfill (newSHA == oldSHA), else a RepairedPage reason

	writeBase bool // store base as the base of path at newSHA
	base      []byte

	sidecar        *syncstate.ConflictRecord // E11 (3) over a local edit
	sidecarContent []byte
	clearSidecar   bool // an adoption of a path that has a sidecar
}

// repairPlan is the outcome of the network phase: one action per repaired
// path, sorted by path, and the reason every other stale path is skipped.
type repairPlan struct {
	actions []repairAction
	skipped map[string]string
}

// repairPlanner is the state of one ensureBasePlan run. Only the calling
// goroutine touches it; request goroutines only call the Remote.
type repairPlanner struct {
	ctx  context.Context
	env  Env
	v    *syncstate.View
	now  time.Time
	conc int
	plan *repairPlan
	stop string // the code of the first failure that is not path-specific
}

// ensureBasePlan decides how the base of every path of stale (sorted) is
// repaired, fetching what it needs from env.Remote, and writes nothing.
// caps and known are the server facts the caller read before any request
// of the plan.
//
// Phase 1 (E12) reads the paths whose listing sha equals page_shas: one
// PullPages when there are more than repairPullThreshold of them, else a
// ReadPage each. Phase 2 reads every other path, and every phase-1 path the
// server moved since the listing, with ReadPageAt at its page_shas sha: at
// most b.max calls, and none without pages.at. The first failure that is
// not path-specific stops every request not yet started. When ctx is done
// the plan stops launching and waiting: the paths left are budget_exhausted
// and replies arriving later are dropped.
func ensureBasePlan(ctx context.Context, env Env, v *syncstate.View, listing []sync.PageMeta,
	stale []string, b repairBudget, caps []contract.ServerCapability, known bool) *repairPlan {
	pl := &repairPlanner{
		ctx:  ctx,
		env:  env,
		v:    v,
		now:  repairNow(env),
		conc: max(1, min(b.conc, repairMaxConcurrency)),
		plan: &repairPlan{skipped: map[string]string{}},
	}
	listed := make(map[string]string, len(listing))
	for _, m := range listing {
		listed[m.Path] = m.SHA
	}
	var headEqual, rest []string
	for _, p := range stale {
		if listed[p] == v.State.PageSHAs[p] {
			headEqual = append(headEqual, p)
		} else {
			rest = append(rest, p)
		}
	}
	rest = append(rest, pl.phase1(headEqual)...)
	sort.Strings(rest)
	pl.phase2(rest, b.max, caps, known)
	sort.Slice(pl.plan.actions, func(i, j int) bool { return pl.plan.actions[i].path < pl.plan.actions[j].path })
	return pl.plan
}

// phase1 decides the HEAD-equal paths the server still has at their
// page_shas sha and returns the others (moved since the listing, or gone
// at HEAD) for phase 2.
func (pl *repairPlanner) phase1(paths []string) (moved []string) {
	if len(paths) == 0 {
		return nil
	}
	if len(paths) > repairPullThreshold {
		return pl.pullAll(paths)
	}
	fetch := func(p string) repairReply {
		body, sha, err := pl.env.Remote.ReadPage(pl.env.RepoID, p)
		return repairReply{path: p, body: body, sha: sha, err: err}
	}
	handle := func(r repairReply) {
		s := pl.v.State.PageSHAs[r.path]
		switch code := repairErrCode(r.err); {
		case r.err == nil && r.sha == s:
			pl.decide(r.path, s, r.body, s)
		case r.err == nil, code == string(contract.CodePageNotFound):
			// Moved since the listing, or gone at HEAD: ?at reads it at S.
			moved = append(moved, r.path)
		default:
			pl.fail(r.path, code)
		}
	}
	pl.fanOut(paths, fetch, handle)
	return moved
}

// pullAll is phase 1 for many paths: one PullPages(repoID, "") answers them
// all. Its failure is never path-specific, so it stops the repair.
func (pl *repairPlanner) pullAll(paths []string) (moved []string) {
	if pl.ctx.Err() != nil {
		pl.skipAll(paths, repairSkipBudgetExhausted)
		return nil
	}
	type pulled struct {
		resp *sync.PullResponse
		err  error
	}
	replies := make(chan pulled, 1) // never blocks: a late reply is dropped
	go func() {
		resp, err := pl.env.Remote.PullPages(pl.env.RepoID, "")
		replies <- pulled{resp, err}
	}()
	var r pulled
	select {
	case r = <-replies:
	case <-pl.ctx.Done():
		pl.skipAll(paths, repairSkipBudgetExhausted)
		return nil
	}
	if r.err != nil {
		code := repairErrCode(r.err)
		pl.skipAll(paths, code)
		pl.stop = code
		return nil
	}
	files := map[string]sync.PullFile{}
	if r.resp != nil {
		for _, f := range r.resp.Files {
			files[f.Path] = f
		}
	}
	for _, p := range paths {
		s := pl.v.State.PageSHAs[p]
		if f, ok := files[p]; ok && f.SHA == s {
			pl.decide(p, s, []byte(f.Content), s)
		} else {
			moved = append(moved, p)
		}
	}
	return moved
}

// phase2 reads paths (sorted) at their page_shas sha with ReadPageAt, at
// most limit calls.
func (pl *repairPlanner) phase2(paths []string, limit int, caps []contract.ServerCapability, known bool) {
	if len(paths) == 0 {
		return
	}
	// Checked before the stop flag: without pages.at these paths never
	// needed a request, and a retry would not help them.
	if !known || !slices.Contains(caps, contract.SrvPagesAt) {
		pl.skipAll(paths, repairSkipServerUnsupported)
		return
	}
	n := min(len(paths), limit)
	pl.skipAll(paths[n:], repairSkipBudgetExhausted)
	fetch := func(p string) repairReply {
		body, sha, err := pl.env.Remote.ReadPageAt(pl.env.RepoID, p, pl.v.State.PageSHAs[p])
		return repairReply{path: p, body: body, sha: sha, err: err}
	}
	handle := func(r repairReply) {
		switch {
		case r.err != nil:
			pl.fail(r.path, repairErrCode(r.err))
		case r.sha == "":
			// Without X-Page-SHA the answer does not say which commit it is.
			pl.fail(r.path, repairSkipServerUnsupported)
		default:
			pl.decide(r.path, pl.v.State.PageSHAs[r.path], r.body, r.sha)
		}
	}
	pl.fanOut(paths[:n], fetch, handle)
}

// repairReply is the answer to one page read.
type repairReply struct {
	path string
	body []byte
	sha  string
	err  error
}

// fanOut reads paths in order with fetch, at most pl.conc at a time, and
// hands every reply to handle on the calling goroutine. It launches nothing
// once a failure stopped the repair or ctx is done; when ctx is done it
// also stops waiting. Each fetch runs on its own goroutine and sends on a
// channel buffered for every path, so it never blocks and a reply arriving
// after fanOut returned is dropped. The paths never launched get the stop
// code (budget_exhausted when ctx ended), those left unanswered
// budget_exhausted.
func (pl *repairPlanner) fanOut(paths []string, fetch func(string) repairReply, handle func(repairReply)) {
	replies := make(chan repairReply, len(paths))
	waiting := map[string]bool{}
	next := 0
	for {
		for next < len(paths) && len(waiting) < pl.conc && pl.stop == "" && pl.ctx.Err() == nil {
			p := paths[next]
			next++
			waiting[p] = true
			go func() { replies <- fetch(p) }()
		}
		if len(waiting) == 0 {
			break
		}
		select {
		case r := <-replies:
			delete(waiting, r.path)
			handle(r)
		case <-pl.ctx.Done():
			pl.skipAll(repairSortedKeys(waiting), repairSkipBudgetExhausted)
			pl.skipAll(paths[next:], repairSkipBudgetExhausted)
			return
		}
	}
	reason := pl.stop
	if reason == "" {
		reason = repairSkipBudgetExhausted // ctx ended between two launches
	}
	pl.skipAll(paths[next:], reason)
}

// decide applies E11 to path, whose page_shas value is s, given raw, the
// server content at sha sNew (taken only from X-Page-SHA, PullFile.SHA or
// the ReadPage sha, never inferred from content).
func (pl *repairPlanner) decide(path, s string, raw []byte, sNew string) {
	c := []byte(schema.SanitizeContent(string(raw)))
	f := pl.v.Facts[path]
	rec, hasRec := pl.v.Bases.Get(path)
	a := repairAction{path: path, oldSHA: s}
	switch {
	case f.Local.Kind == syncstate.LocalValid && f.Local.Hash == syncstate.Hash(c):
		// (2) The working file is that server version: adopt it.
		a.newSHA, a.writeBase, a.base = sNew, true, c
		a.clearSidecar = f.Conflict != nil
	case hasRec && repairVerified(pl.v.Bases, path):
		// (3) An old binary bumped page_shas past a valid base (A.SHA != s,
		// or the path would not be stale): keep A. A local edit of A
		// conflicts with the server version, unless that version is A
		// itself (sNew == A.SHA: page_shas was repo HEAD and nothing touched
		// the path since A), where the edit is a plain modification.
		a.newSHA, a.reason = rec.SHA, repairReasonKeptPreviousBase
		if f.Local.Kind == syncstate.LocalValid && f.Local.Hash != rec.Hash && sNew != rec.SHA {
			a.sidecar = &syncstate.ConflictRecord{
				Path: path, ServerSHA: sNew, BaseSHA: rec.SHA, Kind: syncstate.KindBothModified,
				Source: syncstate.SourceRepair, DetectedAt: pl.now,
			}
			a.sidecarContent = c
		}
	default:
		// (4) Nothing better is known: the server version is the base.
		a.newSHA, a.writeBase, a.base = sNew, true, c
	}
	if a.reason == "" && a.newSHA != s {
		a.reason = repairReasonPathSHAFixed
	}
	pl.plan.actions = append(pl.plan.actions, a)
}

// fail records the failed request of path. A path-specific code skips only
// path; any other also stops every request not yet started.
func (pl *repairPlanner) fail(path, code string) {
	pl.plan.skipped[path] = code
	if pl.stop == "" && !repairPathSpecific(code) {
		pl.stop = code
	}
}

// skipAll skips every path of paths with reason.
func (pl *repairPlanner) skipAll(paths []string, reason string) {
	for _, p := range paths {
		pl.plan.skipped[p] = reason
	}
}

// repairPathSpecific reports whether a failure with code concerns only its
// path, so the repair goes on with the others.
func repairPathSpecific(code string) bool {
	switch contract.ErrorCode(code) {
	case contract.CodeSHANotFound, contract.CodePageNotFound, contract.CodeUsage, contract.CodeInvalidPath:
		return true
	}
	return false
}

// repairErrCode is the contract code of err ("" for nil), read through the
// ErrorCode hook sync.HTTPError and sync.NetworkError implement; internal
// when err has none.
func repairErrCode(err error) string {
	if err == nil {
		return ""
	}
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		if c := coded.ErrorCode(); c != "" {
			return c
		}
	}
	return string(contract.CodeInternal)
}

// repairVerified reports whether path's base file still hashes to its
// record.
func repairVerified(b *syncstate.Bases, path string) bool {
	_, err := b.ReadVerified(path)
	return err == nil
}

// repairApply writes plan in the E8 order (base files, conflicts/<P> files,
// bases.json, conflicts.json, state.json), each index file at most once and
// only when it changed. It first re-reads state.json: an action whose
// page_shas value is no longer the one v was loaded with (an old ctx or MCP
// binary, which takes no lock, wrote it meanwhile) is dropped and its path
// skipped state_changed, as is every action when state.json is now corrupt.
// Errors are local I/O failures.
func repairApply(env Env, v *syncstate.View, plan *repairPlan) error {
	fresh, corrupt, err := sync.LoadStateOrEmpty(env.HubDir)
	if err != nil {
		return err
	}
	kept := make([]repairAction, 0, len(plan.actions))
	for _, a := range plan.actions {
		if corrupt || fresh.PageSHAs[a.path] != a.oldSHA {
			plan.skipped[a.path] = repairSkipStateChanged
			continue
		}
		kept = append(kept, a)
	}
	plan.actions = kept

	now := repairNow(env)
	basesChanged, conflictsChanged, stateChanged := false, false, false
	for _, a := range kept {
		if a.writeBase {
			if err := v.Bases.Put(a.path, a.base, a.newSHA, now); err != nil {
				return err
			}
			basesChanged = true
		}
	}
	for _, a := range kept {
		switch {
		case a.sidecar != nil:
			if err := v.Conflicts.Put(*a.sidecar, a.sidecarContent); err != nil {
				return err
			}
			conflictsChanged = true
		case a.clearSidecar:
			if err := v.Conflicts.Clear(a.path); err != nil {
				return err
			}
			conflictsChanged = true
		}
	}
	if basesChanged {
		if err := v.Bases.Save(); err != nil {
			return err
		}
	}
	if conflictsChanged {
		if err := v.Conflicts.Save(); err != nil {
			return err
		}
	}
	for _, a := range kept {
		if fresh.PageSHAs[a.path] != a.newSHA {
			fresh.PageSHAs[a.path] = a.newSHA
			stateChanged = true
		}
	}
	if stateChanged {
		// fresh keeps whatever else state.json holds (last_pull_sha).
		return sync.SaveState(env.HubDir, fresh)
	}
	return nil
}

// repairWarning is the base_repair_skipped warning naming every skipped
// path (sorted), with the distinct reasons in its message.
func repairWarning(skipped map[string]string) contract.Warning {
	paths := repairSortedKeys(skipped)
	seen := map[string]bool{}
	for _, r := range skipped {
		seen[r] = true
	}
	return contract.Warning{
		Code: contract.WarnBaseRepairSkipped,
		Message: fmt.Sprintf("%d page(s) left unverified: base repair skipped (%s)",
			len(paths), strings.Join(repairSortedKeys(seen), ", ")),
		Paths: paths,
	}
}

// repairSortedKeys returns the keys of m sorted in byte order, never nil.
func repairSortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// repairNow is env's clock, time.Now when it has none.
func repairNow(env Env) time.Time {
	if env.Now != nil {
		return env.Now()
	}
	return time.Now()
}
