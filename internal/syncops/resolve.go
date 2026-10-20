package syncops

import (
	"context"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// Owner: WP-1.4. Fields may be added, never renamed or removed.

// ResolveOptions configures Resolve. IfServerSHA, when set, must equal the
// server's current sha for the path, otherwise the result is stale.
type ResolveOptions struct {
	Path        string
	Strategy    contract.ResolveStrategy
	IfServerSHA string
}

// Resolve applies --ours or --theirs to one path and builds the
// `ctx resolve --json` document.
func Resolve(ctx context.Context, env Env, o ResolveOptions) (*contract.ResolveDoc, error) {
	return nil, NotImplemented("resolve")
}
