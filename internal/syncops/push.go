package syncops

import (
	"context"

	"github.com/sugihAF/contexo/internal/syncops/contract"
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
}

// Push sends the changed pages and builds the `ctx push --json` document.
func Push(ctx context.Context, env Env, o PushOptions) (*contract.PushDoc, error) {
	return nil, NotImplemented("push")
}
