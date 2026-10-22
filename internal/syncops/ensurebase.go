package syncops

import (
	"context"
	"time"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// Owner: WP-1.5. Fields may be added, never renamed or removed.

// ensureBaseStatusBudget caps the network phase of an EnsureBase that takes
// the lock itself (an online status must stay responsive). Tests shorten
// it.
var ensureBaseStatusBudget = 20 * time.Second

// EnsureBaseOptions configures EnsureBase. Listing is the live listing the
// caller already fetched (nil when offline: EnsureBase is then a no-op with
// zero requests). Paths, when non-empty, limits the repair to those paths.
// Locked reports that the caller holds .sync/lock; otherwise EnsureBase
// TryLocks internally (E5).
type EnsureBaseOptions struct {
	Listing []sync.PageMeta
	Paths   []string
	Locked  bool
}

// EnsureBaseResult reports what EnsureBase changed: Repaired are the paths
// whose base or state sha it wrote, Unverified the stale paths it left, all
// of them named by one base_repair_skipped warning. Slices are never nil;
// both path lists are sorted.
type EnsureBaseResult struct {
	Repaired, Unverified []string
	Warnings             []contract.Warning
}

// EnsureBase repairs the missing or stale bases of the paths that have a
// page_shas value (E11/E12), as Repair does, for an engine that has just
// listed the repo. It makes no request and takes no lock when Listing is
// nil, there is no Remote or no base is stale. Unless o.Locked it TryLocks
// .sync/lock, leaving every stale path unverified when another process
// holds it, and caps the network phase at ensureBaseStatusBudget. A failed
// request leaves its paths unverified and is not an error: errors are local
// I/O under .sync, and the cancellation of ctx, after which nothing is
// written. It never prints and never writes a working file.
func EnsureBase(ctx context.Context, env Env, o EnsureBaseOptions) (*EnsureBaseResult, error) {
	res := &EnsureBaseResult{Repaired: []string{}, Unverified: []string{}, Warnings: []contract.Warning{}}
	if o.Listing == nil || env.Remote == nil {
		return res, nil
	}
	v, stale, err := repairLoad(env.HubDir, o.Paths)
	if err != nil || len(stale) == 0 {
		return res, err
	}
	parent := ctx
	if err := parent.Err(); err != nil {
		res.Unverified = stale
		return res, err
	}
	if !o.Locked {
		unlock, ok, err := syncstate.TryLock(env.HubDir, "status")
		if err != nil || !ok {
			// status never waits for the lock: the next run repairs.
			res.Unverified = stale
			res.Warnings = append(res.Warnings, repairWarning(ensureBaseSkipAll(stale, repairSkipLockBusy)))
			return res, nil
		}
		defer unlock()
		// The holder we may have waited behind can have repaired or
		// changed state: decide on what is there now.
		locked, lockedStale, err := repairLoad(env.HubDir, o.Paths)
		if err != nil {
			res.Unverified = stale
			return res, err
		}
		if len(lockedStale) == 0 {
			return res, nil
		}
		v, stale = locked, lockedStale
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, ensureBaseStatusBudget)
		defer cancel()
	}

	// Read before any request of the plan: it reflects the caller's listing.
	_, caps, known := env.Remote.ServerInfo()
	plan := ensureBasePlan(ctx, env, v, o.Listing, stale,
		repairBudget{max: repairDefaultBudget, conc: repairMaxConcurrency}, caps, known)
	if err := parent.Err(); err != nil {
		res.Unverified = stale
		return res, err
	}
	if err := repairApply(env, v, plan); err != nil {
		res.Unverified = stale
		return res, err
	}
	for _, a := range plan.actions {
		res.Repaired = append(res.Repaired, a.path)
	}
	res.Unverified = append(res.Unverified, repairSortedKeys(plan.skipped)...)
	if len(plan.skipped) > 0 {
		res.Warnings = append(res.Warnings, repairWarning(plan.skipped))
	}
	return res, nil
}

// ensureBaseSkipAll maps every path of paths to reason.
func ensureBaseSkipAll(paths []string, reason string) map[string]string {
	m := make(map[string]string, len(paths))
	for _, p := range paths {
		m[p] = reason
	}
	return m
}
