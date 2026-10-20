package syncops

import (
	"context"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

// Owner: WP-1.3. Fields may be added, never renamed or removed.

// PullOptions configures Pull.
type PullOptions struct{}

// Pull runs the safe pull and builds the `ctx pull --json` document.
func Pull(ctx context.Context, env Env, o PullOptions) (*contract.PullDoc, error) {
	return nil, NotImplemented("pull")
}
