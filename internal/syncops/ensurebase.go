package syncops

import (
	"context"

	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// Owner: WP-1.5. Fields may be added, never renamed or removed.

// EnsureBaseOptions configures EnsureBase. Listing is the live listing the
// caller already fetched (nil when offline: EnsureBase is then a no-op with
// zero requests). Locked reports that the caller holds .sync/lock; otherwise
// EnsureBase TryLocks internally (E5).
type EnsureBaseOptions struct {
	Listing []sync.PageMeta
	Paths   []string
	Locked  bool
}

// EnsureBaseResult reports what EnsureBase changed.
type EnsureBaseResult struct {
	Repaired, Unverified []string
	Warnings             []contract.Warning
}

// EnsureBase repairs missing or stale bases (E11/E12). The wave-0 stub
// changes nothing.
func EnsureBase(ctx context.Context, env Env, o EnsureBaseOptions) (*EnsureBaseResult, error) {
	return &EnsureBaseResult{Repaired: []string{}, Unverified: []string{}, Warnings: []contract.Warning{}}, nil
}
