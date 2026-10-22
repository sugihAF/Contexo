package syncops

import (
	"context"
	"os"

	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/syncops/contract"
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

// Status builds the `ctx status --json` document.
func Status(ctx context.Context, env Env, o StatusOptions) (*contract.StatusDoc, error) {
	return nil, NotImplemented("status")
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
