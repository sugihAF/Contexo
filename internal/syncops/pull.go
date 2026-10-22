package syncops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	stdsync "sync"
	"time"

	"github.com/sugihAF/contexo/internal/fsutil"
	"github.com/sugihAF/contexo/internal/indexer"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// Owner: WP-1.3. Fields may be added, never renamed or removed.

// PullOptions configures Pull.
type PullOptions struct{}

// pullEnsureBase is EnsureBase; tests override it (WP-1.5 replaces the stub
// before WP-1.I, so tests that need unverified pages stub this seam).
var pullEnsureBase = EnsureBase

// pullBeforeWriteHook, when non-nil, runs just before pull acts on a fetched
// path: before the re-check of a working-file write, and before the re-read
// behind an adoption or conflict decision. Test seam.
var pullBeforeWriteHook func(rel string)

const (
	// pullFetchConcurrency caps the ReadPage calls in flight.
	pullFetchConcurrency = 4
	// pullBatchFallback is the most needed pages missing from the first
	// batch that are read one by one; more cost one PullPages("") instead.
	pullBatchFallback = 8
)

// Pull runs the safe pull and builds the `ctx pull --json` document. It never
// overwrites a page with local edits: such pages are listed in skipped, and
// skips are normal results. It runs in five phases, all under .sync/lock:
//
//   - PREPARE: the hub and Env guards, the lock, one live ListPages,
//     EnsureBase over that listing, then LoadView over it.
//   - CLASSIFY: contract.ClassifySides plus the E9 sidecar overlay for every
//     entry. Paths that differ only by letter case are skipped (R2);
//     modified, invalid and deleted_local pages are skipped as they are. The
//     incoming, incoming_new, conflict and unverified pages are needed.
//   - FETCH, all or nothing and before any write: one PullPages since
//     last_pull_sha (a rejected since is retried once with "", R4); needed
//     pages missing from it come from one PullPages("") when there are more
//     than pullBatchFallback, else from ReadPage with at most
//     pullFetchConcurrency in flight. A page the server no longer serves is
//     skipped changed_during_pull (R3). Every server byte is sanitized (E6).
//   - APPLY, in path order: incoming pages are overwritten, incoming_new
//     pages created, content-equal conflict and unverified pages adopted and
//     other conflicts recorded as sidecars. Each decision re-reads the
//     working file just before acting and skips changed_during_pull when it
//     changed since the scan (R7).
//   - FINISH, in the E8 write order, each index file saved at most once and
//     only when changed: base files, conflict sidecars, bases.json,
//     conflicts.json, state.json (page_shas and last_pull_sha), then
//     index.md and tags.md when a page was written.
//
// A failure before APPLY writes nothing. An I/O failure in APPLY stops it;
// FINISH still persists what was applied, then Pull returns the error (R9).
func Pull(ctx context.Context, env Env, o PullOptions) (*contract.PullDoc, error) {
	if err := pullGuard(&env); err != nil {
		return nil, err
	}
	unlock, err := syncstate.Lock(env.HubDir, "pull", syncstate.DefaultLockWait())
	if err != nil {
		// Wrapped, not converted: *syncstate.BusyError keeps its code and
		// holder details.
		return nil, fmt.Errorf("pull: %w", err)
	}
	defer unlock()

	r, err := pullPrepare(ctx, env)
	if err != nil {
		return nil, err
	}
	if err := pullClassify(r); err != nil {
		return nil, err
	}
	if err := pullFetch(r); err != nil {
		return nil, err
	}
	pullApply(r)
	if err := pullFinish(r); err != nil {
		return nil, err
	}
	return pullDoc(r), nil
}

// pullRun is the working set of one Pull.
type pullRun struct {
	ctx    context.Context
	env    Env
	now    time.Time
	ensure *EnsureBaseResult
	view   *syncstate.View

	entries    []*pullEntry // classified entries, in path order
	collisions []string     // sorted members of every case-collision group
	head       string       // new_head of the first successful PullPages (R5)

	written, adopted []contract.PulledPage
	skipped          []contract.SkippedPage
	writes, adopts   []*pullEntry // entries whose base FINISH stores
	sidecars         []*pullEntry // conflicts whose sidecar FINISH (re)writes
	reindexErr       error
	reindexed        bool
	firstErr         error // first APPLY/FINISH I/O failure (R9)
}

// pullEntry is one classified view entry and, when needed, what FETCH got.
type pullEntry struct {
	f      *syncstate.PathFacts
	state  contract.PageState
	kind   contract.ConflictKind
	needed bool

	fetched bool   // the server delivered the page; false = vanished (R3)
	content []byte // C: the sanitized server bytes
	sha     string // S: the delivered sha, else the listing sha
}

// pullFail records err as the run's failure unless one is recorded already.
func pullFail(r *pullRun, err error) {
	if r.firstErr == nil {
		r.firstErr = err
	}
}

// ---- PREPARE ----

// pullGuard checks the hub and the Env (R12) before anything is locked.
func pullGuard(env *Env) error {
	hub, err := RequireHub(env.Root)
	if err != nil {
		return err
	}
	if env.HubDir == "" {
		env.HubDir = hub
	}
	if env.Remote == nil {
		return contract.New(contract.CodeNotAuthenticated, "pull: not logged in", "ctx auth login")
	}
	if env.RepoID == "" {
		return contract.New(contract.CodeRepoNotConfigured,
			"pull: no repo_id in .contexo/config.json", "ctx remote set-repo <id>")
	}
	return nil
}

// pullPrepare lists the server, runs EnsureBase over that listing and loads
// the view. The caller holds the lock.
func pullPrepare(ctx context.Context, env Env) (*pullRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("pull: %w", err)
	}
	listing, err := env.Remote.ListPages(env.RepoID)
	if err != nil {
		return nil, fmt.Errorf("pull: %w", err)
	}
	if listing == nil {
		listing = []sync.PageMeta{} // online: never the offline nil
	}
	ensure, err := pullEnsureBase(ctx, env, EnsureBaseOptions{Listing: listing, Locked: true})
	if err != nil {
		return nil, fmt.Errorf("pull: %w", err)
	}
	if ensure == nil {
		ensure = &EnsureBaseResult{}
	}
	// Server paths that are not page paths ("../x.md", ".sync/state.json",
	// "/abs.md") name nothing pull may touch.
	remote := make(map[string]string, len(listing))
	for _, m := range listing {
		if syncstate.ValidateRelPath(m.Path) == nil {
			remote[m.Path] = m.SHA
		}
	}
	view, err := syncstate.LoadView(env.HubDir, remote)
	if err != nil {
		return nil, fmt.Errorf("pull: %w", err)
	}
	now := time.Now()
	if env.Now != nil {
		now = env.Now()
	}
	return &pullRun{
		ctx:     ctx,
		env:     env,
		now:     now,
		ensure:  ensure,
		view:    view,
		written: []contract.PulledPage{},
		adopted: []contract.PulledPage{},
		skipped: []contract.SkippedPage{},
	}, nil
}

// ---- CLASSIFY ----

// pullClassify classifies every view entry, skips the case-collision groups
// (R2) and the pages pull never writes (modified, invalid, deleted_local),
// and marks the needed ones.
func pullClassify(r *pullRun) error {
	for _, rel := range r.view.Paths {
		f := r.view.Facts[rel]
		res, err := contract.ClassifySides(contract.Side(f.Local), contract.Side(f.Base), contract.Side(f.Remote))
		if err != nil {
			return fmt.Errorf("pull: %s: %w", rel, err)
		}
		res = ApplySidecar(res, f.Base.SHA, pullSidecarFact(f.Conflict))
		if res.State == "" {
			continue
		}
		r.entries = append(r.entries, &pullEntry{f: f, state: res.State, kind: res.ConflictKind})
	}
	r.collisions = pullCollisions(r.entries)
	colliding := make(map[string]bool, len(r.collisions))
	for _, p := range r.collisions {
		colliding[p] = true
	}
	for _, e := range r.entries {
		if colliding[e.f.Path] {
			pullSkip(r, e, contract.SkipCaseCollision, pullListedSHA(e))
			continue
		}
		switch e.state {
		case contract.StateIncoming, contract.StateIncomingNew, contract.StateConflict, contract.StateUnverified:
			e.needed = true
		case contract.StateModified:
			pullSkip(r, e, contract.SkipModified, pullListedSHA(e))
		case contract.StateInvalid:
			pullSkip(r, e, contract.SkipInvalid, pullListedSHA(e))
		case contract.StateDeletedLocal:
			pullSkip(r, e, contract.SkipDeletedLocal, pullListedSHA(e))
		}
	}
	return nil
}

// pullSidecarFact is the E9 overlay input of a conflicts.json record.
func pullSidecarFact(rec *syncstate.ConflictRecord) *SidecarFact {
	if rec == nil {
		return nil
	}
	return &SidecarFact{ServerSHA: rec.ServerSHA, Kind: contract.ConflictKind(rec.Kind)}
}

// pullCollisions groups the entry paths under simple case folding (R2) and
// returns the members of every group of two or more paths, sorted.
func pullCollisions(entries []*pullEntry) []string {
	groups := map[string][]string{}
	for _, e := range entries {
		key := strings.ToLower(strings.ToUpper(e.f.Path))
		groups[key] = append(groups[key], e.f.Path)
	}
	out := []string{}
	for _, g := range groups {
		if len(g) >= 2 {
			out = append(out, g...)
		}
	}
	sort.Strings(out)
	return out
}

// pullListedSHA is the server_sha of a skip that fetched nothing (R1): the
// listing sha when the server has the page, except for modified, whose
// listing sha is the base sha.
func pullListedSHA(e *pullEntry) string {
	if e.state == contract.StateModified || e.f.Remote.Kind != syncstate.RemotePresent {
		return ""
	}
	return e.f.Remote.SHA
}

// pullSkip lists e as skipped for reason.
func pullSkip(r *pullRun, e *pullEntry, reason contract.SkipReason, serverSHA string) {
	r.skipped = append(r.skipped, contract.SkippedPage{
		Path: e.f.Path, State: e.state, Reason: reason, ServerSHA: serverSHA,
	})
}

// ---- FETCH ----

// pullFetch delivers every needed page it can before anything is written.
// Needed pages left unfetched vanished from the server (R3).
func pullFetch(r *pullRun) error {
	if err := r.ctx.Err(); err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	resp, full, err := pullBatch(r.env, r.view.State.LastPullSHA)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	r.head = resp.NewHead
	missing := pullTake(r, resp.Files)
	switch {
	case len(missing) == 0 || full:
		// A full batch lists every page at HEAD: what it lacks is gone
		// (R3), and it was the one PullPages("") (R6).
	case len(missing) > pullBatchFallback:
		all, err := r.env.Remote.PullPages(r.env.RepoID, "")
		if err != nil {
			return fmt.Errorf("pull: %w", err)
		}
		if all != nil {
			pullTake(r, all.Files)
		}
	default:
		if err := pullReadMissing(r, missing); err != nil {
			return err
		}
	}
	if err := r.ctx.Err(); err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	return nil
}

// pullBatch is the one PullPages call since last_pull_sha, retried once with
// "" when the server rejects since (R4). full reports that the returned
// batch was made with since "".
func pullBatch(env Env, since string) (resp *sync.PullResponse, full bool, err error) {
	resp, err = env.Remote.PullPages(env.RepoID, since)
	if err != nil && since != "" && pullSinceRejected(err) {
		since = ""
		resp, err = env.Remote.PullPages(env.RepoID, since)
	}
	if err != nil {
		return nil, false, err
	}
	if resp == nil {
		resp = &sync.PullResponse{}
	}
	return resp, since == "", nil
}

// pullSinceRejected reports a PullPages failure that means the server could
// not use since (an unknown or malformed sha): server_error or usage.
func pullSinceRejected(err error) bool {
	var he *sync.HTTPError
	return errors.As(err, &he) && (he.Code == contract.CodeServerError || he.Code == contract.CodeUsage)
}

// pullTake delivers the needed, not yet fetched entries that files holds and
// returns the ones it lacks, in path order. Files of other paths, page
// paths or not, are ignored.
func pullTake(r *pullRun, files []sync.PullFile) []*pullEntry {
	byPath := make(map[string]int, len(files))
	for i, pf := range files {
		byPath[pf.Path] = i
	}
	var missing []*pullEntry
	for _, e := range r.entries {
		if !e.needed || e.fetched {
			continue
		}
		if i, ok := byPath[e.f.Path]; ok {
			pullDeliver(e, files[i].Content, files[i].SHA)
		} else {
			missing = append(missing, e)
		}
	}
	return missing
}

// pullDeliver stores the server version of e: its sanitized bytes (E6) and
// its sha, the listing sha when the server sent none.
func pullDeliver(e *pullEntry, server, sha string) {
	e.fetched = true
	e.content = []byte(schema.SanitizeContent(server))
	e.sha = sha
	if e.sha == "" {
		e.sha = e.f.Remote.SHA
	}
}

// pullReadMissing reads each missing page with ReadPage, at most
// pullFetchConcurrency at once. A page_not_found leaves the entry unfetched
// (vanished, R3); any other failure is returned, the first in path order.
func pullReadMissing(r *pullRun, missing []*pullEntry) error {
	errs := make([]error, len(missing))
	sem := make(chan struct{}, pullFetchConcurrency)
	var wg stdsync.WaitGroup
	for i, e := range missing {
		if err := r.ctx.Err(); err != nil {
			errs[i] = err
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() {
				<-sem
				wg.Done()
			}()
			body, sha, err := r.env.Remote.ReadPage(r.env.RepoID, e.f.Path)
			switch {
			case err == nil:
				pullDeliver(e, string(body), sha)
			case !pullPageGone(err):
				errs[i] = err
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return fmt.Errorf("pull: %w", err)
		}
	}
	return nil
}

// pullPageGone reports a ReadPage failure meaning the server no longer has
// the page (R3). An *sync.HTTPError decides by its code alone: its legacy
// sentinel is ErrPageNotFound for every 404, repo_not_found included.
func pullPageGone(err error) bool {
	var he *sync.HTTPError
	if errors.As(err, &he) {
		return he.Code == contract.CodePageNotFound
	}
	return errors.Is(err, sync.ErrPageNotFound)
}

// ---- APPLY ----

// pullApply acts on every needed entry in path order. It stops at the first
// failed working-file write (R9).
func pullApply(r *pullRun) {
	for _, e := range r.entries {
		if !e.needed {
			continue
		}
		if !e.fetched {
			pullSkip(r, e, contract.SkipChangedDuringPull, "") // vanished (R3)
			continue
		}
		if pullBeforeWriteHook != nil {
			pullBeforeWriteHook(e.f.Path)
		}
		var err error
		switch e.state {
		case contract.StateIncoming:
			err = pullApplyIncoming(r, e)
		case contract.StateIncomingNew:
			err = pullApplyNew(r, e)
		default: // conflict, unverified
			pullDecide(r, e)
		}
		if err != nil {
			pullFail(r, err)
			return
		}
	}
}

// pullApplyIncoming overwrites an unmodified page with the server version,
// keeping the file's permission bits (R13), unless the file changed since the
// scan (R7).
//
// R14: between the re-check and the rename inside WriteFileAtomic an edit can
// still land and be replaced. That window is well under a millisecond and
// stays open: there is no portable rename that refuses to replace a file.
func pullApplyIncoming(r *pullRun, e *pullEntry) error {
	p := pullDiskPath(r.env.HubDir, e.f.Path)
	cur, perm, ok := pullReread(p)
	if !ok || syncstate.Hash(cur) != e.f.Base.Hash {
		pullSkip(r, e, contract.SkipChangedDuringPull, e.sha)
		return nil
	}
	if err := fsutil.WriteFileAtomic(p, e.content, perm); err != nil {
		return err
	}
	pullWritten(r, e, contract.WriteUpdate)
	return nil
}

// pullApplyNew creates a page the server added, unless a path component has
// a case variant on disk (R2) or the file appeared since the scan (R7).
func pullApplyNew(r *pullRun, e *pullEntry) error {
	if pullCaseVariant(r.env.HubDir, e.f.Path) {
		pullSkip(r, e, contract.SkipCaseCollision, e.sha)
		return nil
	}
	p := pullDiskPath(r.env.HubDir, e.f.Path)
	if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
		pullSkip(r, e, contract.SkipChangedDuringPull, e.sha)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// R14 applies here as well.
	if err := fsutil.WriteFileAtomic(p, e.content, 0o644); err != nil {
		return err
	}
	pullWritten(r, e, contract.WriteCreate)
	return nil
}

// pullDecide settles a conflict or unverified page without writing it: the
// file is re-read (R7); content equal to the server version is adopted, a
// conflict gets a sidecar and an unverified page is left alone.
func pullDecide(r *pullRun, e *pullEntry) {
	cur, _, ok := pullReread(pullDiskPath(r.env.HubDir, e.f.Path))
	if !ok {
		pullSkip(r, e, contract.SkipChangedDuringPull, e.sha)
		return
	}
	h := syncstate.Hash(cur)
	switch {
	case h != e.f.Local.Hash:
		pullSkip(r, e, contract.SkipChangedDuringPull, e.sha)
	case h == syncstate.Hash(e.content):
		r.adopted = append(r.adopted, contract.PulledPage{Path: e.f.Path, SHA: e.sha})
		r.adopts = append(r.adopts, e)
	case e.state == contract.StateConflict:
		pullSkip(r, e, contract.SkipConflict, e.sha)
		if !pullSidecarCurrent(e) {
			r.sidecars = append(r.sidecars, e)
		}
	default:
		pullSkip(r, e, contract.SkipUnverified, e.sha)
	}
}

// pullWritten lists e as written with kind.
func pullWritten(r *pullRun, e *pullEntry, kind contract.WriteKind) {
	r.written = append(r.written, contract.PulledPage{Path: e.f.Path, SHA: e.sha, Kind: kind})
	r.writes = append(r.writes, e)
}

// pullReread reads the working file at p just before pull acts on it. ok is
// false when it is gone or unreadable, or not a regular file: pull then
// leaves it alone.
func pullReread(p string) (data []byte, perm os.FileMode, ok bool) {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, 0, false
	}
	data, err = os.ReadFile(p)
	if err != nil {
		return nil, 0, false
	}
	return data, fi.Mode().Perm(), true
}

// pullSidecarCurrent reports whether e's existing sidecar already records
// this conflict: same server sha, kind and base sha, with content. It is
// then kept as is, detected_at included (R8).
func pullSidecarCurrent(e *pullEntry) bool {
	rec := e.f.Conflict
	if rec == nil {
		return false
	}
	want := pullConflictRecord(e, time.Time{})
	return rec.ServerSHA == want.ServerSHA && rec.Kind == want.Kind &&
		rec.BaseSHA == want.BaseSHA && rec.HasContent == (len(e.content) > 0)
}

// pullConflictRecord is the sidecar pull stores for the conflict e. BaseSHA
// is empty for both_added, as conflicts.json stores it.
func pullConflictRecord(e *pullEntry, now time.Time) syncstate.ConflictRecord {
	rec := syncstate.ConflictRecord{
		Path:       e.f.Path,
		ServerSHA:  e.sha,
		BaseSHA:    e.f.Base.SHA,
		Kind:       string(e.kind),
		Source:     syncstate.SourcePull,
		DetectedAt: now,
	}
	if rec.Kind == syncstate.KindBothAdded {
		rec.BaseSHA = ""
	}
	return rec
}

// pullCaseVariant reports whether a component of rel has a sibling on disk
// that differs from it only by letter case, so a write to rel could land in
// that file or directory on a case-insensitive file system. It walks rel's
// segments down from hub with os.ReadDir: a case variant reports true, an
// exact match descends, and an absent segment or an unreadable directory
// ends the walk with false.
func pullCaseVariant(hub, rel string) bool {
	dir := hub
	for _, seg := range strings.Split(rel, "/") {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		exact := false
		for _, d := range entries {
			switch name := d.Name(); {
			case name == seg:
				exact = true
			case strings.EqualFold(name, seg):
				return true
			}
		}
		if !exact {
			return false
		}
		dir = filepath.Join(dir, seg)
	}
	return false
}

// pullDiskPath is the working file of the page path rel under hub.
func pullDiskPath(hub, rel string) string {
	return filepath.Join(hub, filepath.FromSlash(rel))
}

// ---- FINISH ----

// pullFinish persists what APPLY did in the E8 order: base files, conflict
// sidecars, bases.json, conflicts.json, state.json, each index file at most
// once and only when changed; then reindexes when a page was written. It
// returns the run's first I/O failure (R9).
func pullFinish(r *pullRun) error {
	stored := append(append([]*pullEntry{}, r.writes...), r.adopts...)
	basesDirty, stateDirty := pullStoreBases(r, stored)
	conflictsDirty := pullStoreSidecars(r, stored)
	if basesDirty {
		if err := r.view.Bases.Save(); err != nil {
			pullFail(r, err)
		}
	}
	if conflictsDirty {
		if err := r.view.Conflicts.Save(); err != nil {
			pullFail(r, err)
		}
	}
	st := r.view.State
	if st.LastPullSHA != r.head {
		st.LastPullSHA = r.head
		stateDirty = true
	}
	if stateDirty {
		if err := sync.SaveState(r.env.HubDir, st); err != nil {
			pullFail(r, err)
		}
	}
	if len(r.written) > 0 {
		r.reindexErr = indexer.Generate(&pagestore.Store{Root: r.env.HubDir})
		r.reindexed = r.reindexErr == nil
	}
	if r.firstErr != nil {
		return fmt.Errorf("pull: %w", r.firstErr)
	}
	return nil
}

// pullStoreBases writes the base of every stored (written, then adopted)
// entry and points page_shas at its sha. page_shas moves even when Bases.Put
// fails: the page is then unverified, never modified (R9).
func pullStoreBases(r *pullRun, stored []*pullEntry) (basesDirty, stateDirty bool) {
	st := r.view.State
	if st.PageSHAs == nil {
		st.PageSHAs = map[string]string{}
	}
	for _, e := range stored {
		if err := r.view.Bases.Put(e.f.Path, e.content, e.sha, r.now); err != nil {
			pullFail(r, err)
		} else {
			basesDirty = true
		}
		if st.PageSHAs[e.f.Path] != e.sha {
			st.PageSHAs[e.f.Path] = e.sha
			stateDirty = true
		}
	}
	return basesDirty, stateDirty
}

// pullStoreSidecars clears the sidecar of every stored entry that had one and
// writes the new or changed conflict sidecars. It reports whether the
// conflicts index changed.
func pullStoreSidecars(r *pullRun, stored []*pullEntry) bool {
	dirty := false
	for _, e := range stored {
		if _, ok := r.view.Conflicts.Get(e.f.Path); !ok {
			continue
		}
		if err := r.view.Conflicts.Clear(e.f.Path); err != nil {
			pullFail(r, err)
			continue
		}
		dirty = true
	}
	for _, e := range r.sidecars {
		if err := r.view.Conflicts.Put(pullConflictRecord(e, r.now), e.content); err != nil {
			pullFail(r, err)
			continue
		}
		dirty = true
	}
	return dirty
}

// ---- document ----

// pullDoc builds the ctx.pull.v1 document: every slice non-nil, skipped
// sorted by path (written and adopted already are).
func pullDoc(r *pullRun) *contract.PullDoc {
	sort.SliceStable(r.skipped, func(i, j int) bool { return r.skipped[i].Path < r.skipped[j].Path })
	return &contract.PullDoc{
		Schema:    contract.SchemaPull,
		ServerURL: r.env.ServerURL,
		RepoID:    r.env.RepoID,
		Head:      r.head,
		Written:   r.written,
		Adopted:   r.adopted,
		Skipped:   r.skipped,
		Reindexed: r.reindexed,
		Warnings:  pullWarnings(r),
	}
}

// pullWarnings lists the warnings in R10 order: EnsureBase's, then
// state_unreadable (unless EnsureBase reported it), case_collision and
// reindex_failed.
func pullWarnings(r *pullRun) []contract.Warning {
	out := []contract.Warning{}
	unreadable := false
	for _, w := range r.ensure.Warnings {
		if w.Paths == nil {
			w.Paths = []string{}
		}
		unreadable = unreadable || w.Code == contract.WarnStateUnreadable
		out = append(out, w)
	}
	if len(r.view.Corrupt) > 0 && !unreadable {
		paths := make([]string, 0, len(r.view.Corrupt))
		for _, name := range r.view.Corrupt {
			paths = append(paths, ".sync/"+name)
		}
		out = append(out, contract.Warning{
			Code:    contract.WarnStateUnreadable,
			Message: "corrupt sync state treated as empty: " + strings.Join(paths, ", "),
			Paths:   paths,
		})
	}
	if n := len(r.collisions); n > 0 {
		which := "both"
		if n > 2 {
			which = "all of them"
		}
		out = append(out, contract.Warning{
			Code: contract.WarnCaseCollision,
			Message: fmt.Sprintf("%d paths differ only by letter case; pull skipped %s: %s",
				n, which, strings.Join(r.collisions, ", ")),
			Paths: r.collisions,
		})
	}
	if r.reindexErr != nil {
		out = append(out, contract.Warning{
			Code:    contract.WarnReindexFailed,
			Message: "reindex: " + r.reindexErr.Error(),
			Paths:   []string{"index.md", "tags.md"},
		})
	}
	return out
}
