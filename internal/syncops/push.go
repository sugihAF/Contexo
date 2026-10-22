package syncops

import (
	"context"
	"errors"

	"github.com/sugihAF/contexo/internal/sync"
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
	// Feature, Tag and Type narrow the batch built without Paths to pages
	// whose local frontmatter has the tag (Feature wins over Tag) and the
	// type, compared case-insensitively. They cannot be combined with Paths.
	Feature string
	Tag     string
	Type    string
	// Confirm, when set, is called under the lock with the files about to be
	// sent, after the distill guard passed and before anything is sent (also
	// on a dry run). Returning false stops the push with ErrPushDeclined, an
	// error stops it with that error; either way nothing is sent or written.
	// Human `ctx push` uses it for its preview and prompt; --json and MCP
	// leave it nil.
	Confirm func(plan []PushPlanFile) (bool, error)
}

// PushPlanFile is one file Push is about to send: the request file and
// whether it creates the page on the server or updates it.
type PushPlanFile struct {
	Kind contract.WriteKind
	File sync.PushFile
}

// ErrPushDeclined is returned when PushOptions.Confirm declines the batch.
var ErrPushDeclined = errors.New("push: declined")

// Test seams. pushResolveFn runs the Resolved pre-step outside the lock;
// pushEnsureBase repairs bases under the lock (E5).
var (
	pushResolveFn  = Resolve
	pushEnsureBase = EnsureBase
)

// Push sends the changed pages and builds the `ctx push --json` document.
func Push(ctx context.Context, env Env, o PushOptions) (*contract.PushDoc, error) {
	return nil, NotImplemented("push")
}
