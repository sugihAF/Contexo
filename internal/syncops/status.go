package syncops

import (
	"context"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// Owner: WP-1.1. Fields may be added, never renamed or removed.

// StatusOptions configures Status.
type StatusOptions struct {
	Offline bool
}

// Status builds the `ctx status --json` document.
func Status(ctx context.Context, env Env, o StatusOptions) (*contract.StatusDoc, error) {
	return nil, NotImplemented("status")
}
