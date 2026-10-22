package syncops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sugihAF/contexo/internal/fsutil"
	"github.com/sugihAF/contexo/internal/indexer"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/store/pagestore"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// Owner: WP-1.4. Fields may be added, never renamed or removed.

// Test seams; production never changes them. resolveWriteFile writes the
// --theirs backup and working file, and nothing else, so a test can record
// their order or fail the second write. resolveBeforeWrite, when set, runs
// after the classification and right before the first write (for --theirs,
// before it reads the bytes it backs up), so a test can edit the working
// file inside that window.
var (
	resolveWriteFile   = fsutil.WriteFileAtomic
	resolveBeforeWrite func()
)

// ResolveOptions configures Resolve. IfServerSHA, when set, must equal the
// server's current sha for the path, otherwise the result is stale.
type ResolveOptions struct {
	Path        string
	Strategy    contract.ResolveStrategy
	IfServerSHA string
}

// ResolvePreflight checks Resolve's arguments without any I/O and returns
// the contract path. It is the single source of the argument rules: the
// engine calls it first, and `ctx resolve` calls it before it opens the
// remote, so a bad argument is invalid_path or usage (exit 3) whatever the
// credentials. The path is checked first (showValidatePath), then the
// strategy, then the --if-server-sha form (7-64 hex, either case).
func ResolvePreflight(env Env, o ResolveOptions) (string, error) {
	rel, err := showValidatePath(env, o.Path)
	if err != nil {
		return "", err
	}
	switch o.Strategy {
	case contract.ResolveOurs, contract.ResolveTheirs:
	default:
		return "", New(contract.CodeUsage, "resolve: pass exactly one of --ours or --theirs")
	}
	if o.IfServerSHA != "" && !showIsSHA(o.IfServerSHA) {
		return "", New(contract.CodeUsage, "resolve: --if-server-sha must be a 7-64 character hex sha")
	}
	return rel, nil
}

// Resolve applies --ours or --theirs to one path and builds the
// `ctx resolve --json` document.
//
// PREPARE runs in this order: the argument rules, the hub check (before
// Lock, which would create a missing .contexo/.sync), the remote check, the
// lock, exactly one ReadPage for the server content and X-Page-SHA, the
// --if-server-sha guard, then the classification of the path against that
// fresh sha with the E9 sidecar overlay. Resolve never lists pages, never
// reads ?at, never calls EnsureBase (E5) and never pushes.
func Resolve(ctx context.Context, env Env, o ResolveOptions) (*contract.ResolveDoc, error) {
	rel, err := ResolvePreflight(env, o)
	if err != nil {
		return nil, err
	}
	if err := showRequireHub(env); err != nil {
		return nil, err
	}
	if err := showRequireRemote(env, "resolve"); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("resolve %s: %w", rel, err)
	}
	unlock, err := syncstate.Lock(env.HubDir, "resolve", syncstate.DefaultLockWait())
	if err != nil {
		// %w and no *contract.Error: ui.ToContractError then reads the
		// *syncstate.BusyError, so the envelope details are the holder.
		return nil, fmt.Errorf("resolve: %w", err)
	}
	defer unlock()

	p, err := resolvePrepare(ctx, env, o, rel)
	if err != nil {
		return nil, err
	}
	doc := &contract.ResolveDoc{
		Schema:        contract.SchemaResolve,
		Path:          rel,
		Strategy:      o.Strategy,
		PreviousState: p.prev.State,
		BaseSHA:       p.sha,
		Warnings:      resolveWarnings(p.view),
	}
	if o.Strategy == contract.ResolveOurs {
		err = resolveOurs(env, p)
	} else {
		err = resolveTheirs(env, p, doc)
	}
	if err != nil {
		return nil, err
	}
	// The result state comes from a fresh view of what is now on disk, not
	// from what the strategy meant to produce.
	_, _, after, err := resolveClassify(env, rel, p.sha)
	if err != nil {
		return nil, err
	}
	doc.State = after.State
	return doc, nil
}

// resolveOurs keeps the working file and re-bases it onto the server
// version: base := C at the server sha, page_shas := that sha, sidecar
// cleared. The page becomes modified (unchanged when the content is equal)
// and the next push sends the local content with that parent, deliberately
// superseding the teammate's version.
//
// Only a conflict (either kind) or an unverified page with a valid local
// file may be re-based. Every other state, modified and unchanged included,
// is not_resolvable with nothing written: on incoming it would
// revert a teammate's change, and WP-1.2 reads details.state.
func resolveOurs(env Env, p *resolvePrep) error {
	ok := p.prev.State == contract.StateConflict ||
		p.prev.State == contract.StateUnverified && p.facts.Local.Kind == syncstate.LocalValid
	if !ok {
		return resolveNotResolvable(p, contract.ResolveOurs)
	}
	if resolveBeforeWrite != nil {
		resolveBeforeWrite()
	}
	return resolveRecord(env, p, env.Now())
}

// resolveTheirs replaces the working file with the server version C and
// records C as the base, so the page ends unchanged. The server must have
// the page, which the successful ReadPage guarantees; every local state is
// allowed. A working file whose raw bytes differ from C is first saved to
// .sync/backup/<rel>.<UTC stamp>, read right before the overwrite rather
// than taken from the scan, so an edit saved meanwhile is what the backup
// holds. A missing file is created with its directories (deleted_local,
// incoming_new); a file already byte-equal to C is not rewritten. Writes go
// backup, working file, then resolveRecord, and the index is regenerated
// when the working file was written.
//
// From unchanged --theirs is a no-op success that only drops a lingering
// sidecar: left behind, offline status would still read it as a
// newer server version. So a retried --theirs never takes a second backup.
func resolveTheirs(env Env, p *resolvePrep, doc *contract.ResolveDoc) error {
	if p.prev.State == contract.StateUnchanged {
		return resolveDropSidecar(p)
	}
	abs := filepath.Join(env.HubDir, filepath.FromSlash(p.rel))
	if err := resolveInsideHub(env.HubDir, abs, p.rel); err != nil {
		return err
	}
	if resolveBeforeWrite != nil {
		resolveBeforeWrite()
	}
	cur, err := os.ReadFile(abs)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("resolve %s: %w", p.rel, err)
	}
	now := env.Now()
	if !exists || !bytes.Equal(cur, p.content) {
		if exists {
			if err := syncstate.EnsureSyncDir(env.HubDir); err != nil {
				return fmt.Errorf("resolve %s: %w", p.rel, err)
			}
			if err := resolveWriteFile(syncstate.BackupFile(env.HubDir, p.rel, now), cur, 0o644); err != nil {
				return fmt.Errorf("resolve %s: back up the working file: %w", p.rel, err)
			}
			doc.BackupFile = syncstate.BackupRel(p.rel, now)
		}
		if err := resolveWriteFile(abs, p.content, 0o644); err != nil {
			return fmt.Errorf("resolve %s: write the server version: %w", p.rel, err)
		}
		doc.FileWritten = true
	}
	if err := resolveRecord(env, p, now); err != nil {
		return err
	}
	if doc.FileWritten {
		if err := indexer.Generate(&pagestore.Store{Root: env.HubDir}); err != nil {
			doc.Warnings = append(doc.Warnings, contract.Warning{
				Code: contract.WarnReindexFailed, Message: err.Error(), Paths: []string{p.rel},
			})
		}
	}
	return nil
}

// resolveDropSidecar clears rel's sidecar, if it has one, and saves
// conflicts.json. It writes nothing when there is none.
func resolveDropSidecar(p *resolvePrep) error {
	if p.facts.Conflict == nil {
		return nil
	}
	if err := p.view.Conflicts.Clear(p.rel); err != nil {
		return fmt.Errorf("resolve %s: %w", p.rel, err)
	}
	if err := p.view.Conflicts.Save(); err != nil {
		return fmt.Errorf("resolve %s: %w", p.rel, err)
	}
	return nil
}

// resolveInsideHub refuses the --theirs write when the nearest existing
// directory of abs resolves outside the resolved hub. rel has
// passed the path rule, so only a symlinked directory inside .contexo can
// lead out, and it must not redirect the write. The refusal is
// invalid_path; nothing has been written yet.
func resolveInsideHub(hub, abs, rel string) error {
	dir := filepath.Dir(abs)
	for {
		if _, err := os.Lstat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	realHub, herr := filepath.EvalSymlinks(hub)
	realDir, derr := filepath.EvalSymlinks(dir)
	if herr == nil && derr == nil {
		if r, err := filepath.Rel(realHub, realDir); err == nil && (r == "." || filepath.IsLocal(r)) {
			return nil
		}
	}
	return Wrap(&syncstate.PathError{Path: rel, Reason: "its directory resolves outside the hub"},
		contract.CodeInvalidPath, "resolve "+rel+" --theirs")
}

// resolveRecord stores the server version as rel's base and drops its
// sidecar, continuing the E8 write order after any working-file write: the
// base file, the conflicts/<rel> file, bases.json, conflicts.json (only
// when rel had a record, so a corrupt index is left for status to report),
// then state.json.
func resolveRecord(env Env, p *resolvePrep, now time.Time) error {
	v := p.view
	if err := v.Bases.Put(p.rel, p.content, p.sha, now); err != nil {
		return fmt.Errorf("resolve %s: %w", p.rel, err)
	}
	if err := v.Conflicts.Clear(p.rel); err != nil {
		return fmt.Errorf("resolve %s: %w", p.rel, err)
	}
	if err := v.Bases.Save(); err != nil {
		return fmt.Errorf("resolve %s: %w", p.rel, err)
	}
	if p.facts.Conflict != nil {
		if err := v.Conflicts.Save(); err != nil {
			return fmt.Errorf("resolve %s: %w", p.rel, err)
		}
	}
	v.State.PageSHAs[p.rel] = p.sha
	if err := sync.SaveState(env.HubDir, v.State); err != nil {
		return fmt.Errorf("resolve %s: %w", p.rel, err)
	}
	return nil
}

// resolveNotResolvable is the refusal of strategy s from the page's
// current state: exit 9, details {path, state, strategy}.
func resolveNotResolvable(p *resolvePrep, s contract.ResolveStrategy) error {
	what := string(p.prev.State)
	if what == "" {
		what = "absent locally"
	}
	e := New(contract.CodeNotResolvable,
		fmt.Sprintf("resolve %s --%s: the page is %s, not a conflict", p.rel, s, what))
	e.Details = contract.NotResolvableDetails{Path: p.rel, State: p.prev.State, Strategy: s}
	return e
}

// resolveWarnings lists the document's warnings known after PREPARE: the
// index files LoadView found corrupt and treated as empty.
// Never nil, so the document always carries "warnings": [].
func resolveWarnings(v *syncstate.View) []contract.Warning {
	out := []contract.Warning{}
	if len(v.Corrupt) > 0 {
		names := make([]string, len(v.Corrupt))
		for i, n := range v.Corrupt {
			names[i] = ".sync/" + n
		}
		out = append(out, contract.Warning{
			Code:    contract.WarnStateUnreadable,
			Message: "treated unreadable " + strings.Join(names, ", ") + " as empty",
			Paths:   []string{},
		})
	}
	return out
}

// resolvePrep is what PREPARE learned about the path: the server version
// (content already sanitized) and the pre-write view and classification.
type resolvePrep struct {
	rel     string
	sha     string // X-Page-SHA of the one ReadPage
	content []byte // SanitizeContent(server bytes): what resolve stores and writes
	view    *syncstate.View
	facts   *syncstate.PathFacts
	prev    contract.ClassifyResult // with the E9 overlay applied
}

// resolvePrepare makes the one ReadPage, applies the stale guard and
// classifies rel against the live sha. The caller holds the lock.
func resolvePrepare(ctx context.Context, env Env, o ResolveOptions, rel string) (*resolvePrep, error) {
	raw, sha, err := env.Remote.ReadPage(env.RepoID, rel)
	if err != nil {
		// %w keeps the HTTPError code (page_not_found / repo_not_found per
		// E14, auth_rejected, ...) and NetworkError's network. There is no
		// sidecar-content fallback in v1.
		return nil, fmt.Errorf("resolve %s: %w", rel, err)
	}
	if !showIsSHA(sha) {
		// A 200 without a usable X-Page-SHA is a garbled answer:
		// a base needs the sha it belongs to, so nothing is written.
		return nil, New(contract.CodeServerError,
			fmt.Sprintf("resolve %s: the server sent the page without a valid X-Page-SHA (%q)", rel, sha))
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("resolve %s: %w", rel, err)
	}
	if o.IfServerSHA != "" && !strings.EqualFold(o.IfServerSHA, sha) {
		// In full and case-insensitively: an abbreviation is stale, never a
		// prefix match.
		e := New(contract.CodeStale,
			fmt.Sprintf("resolve %s: the server is at %s, not %s", rel, sha, o.IfServerSHA))
		e.Details = contract.StaleDetails{Path: rel, ExpectedSHA: o.IfServerSHA, ServerSHA: sha}
		return nil, e
	}

	p := &resolvePrep{rel: rel, sha: sha, content: []byte(schema.SanitizeContent(string(raw)))}
	if p.view, p.facts, p.prev, err = resolveClassify(env, rel, sha); err != nil {
		return nil, err
	}
	return p, nil
}

// resolveClassify loads the view with rel present at sha (the live answer
// stands in for a listing) and classifies rel with the E9 sidecar overlay.
func resolveClassify(env Env, rel, sha string) (*syncstate.View, *syncstate.PathFacts, contract.ClassifyResult, error) {
	var none contract.ClassifyResult
	v, err := syncstate.LoadView(env.HubDir, map[string]string{rel: sha})
	if err != nil {
		return nil, nil, none, fmt.Errorf("resolve %s: %w", rel, err)
	}
	f := v.Facts[rel]
	if f == nil {
		// LoadView makes an entry for every valid remote key; rel is one.
		return nil, nil, none, New(contract.CodeInternal, "resolve "+rel+": the path has no entry in the local view")
	}
	res, err := contract.ClassifySides(contract.Side(f.Local), contract.Side(f.Base), contract.Side(f.Remote))
	if err != nil {
		return nil, nil, none, fmt.Errorf("resolve %s: %w", rel, err)
	}
	return v, f, ApplySidecar(res, f.Base.SHA, resolveSidecarFact(f.Conflict)), nil
}

// resolveSidecarFact is the SidecarFact of a conflict record, nil without
// one.
func resolveSidecarFact(rec *syncstate.ConflictRecord) *SidecarFact {
	if rec == nil {
		return nil
	}
	return &SidecarFact{ServerSHA: rec.ServerSHA, Kind: contract.ConflictKind(rec.Kind)}
}
