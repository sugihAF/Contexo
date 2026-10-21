package syncops

import "context"

// Owner: WP-1.4. Fields may be added, never renamed or removed.

// ShowOptions configures Show. At is "base", "server" or a hex sha.
type ShowOptions struct {
	Path, At string
}

// Show returns the raw bytes `ctx show` writes to stdout.
func Show(ctx context.Context, env Env, o ShowOptions) ([]byte, error) {
	return nil, NotImplemented("show")
}
