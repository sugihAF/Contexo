// Package quota is the public open-core seam for hosted-only usage limits.
//
// The OSS/self-host server never constructs a real Policy: app.Run falls back
// to Unlimited, so a self-hosted Contexo has no caps. The hosted build
// (contexo-backend) injects a Policy backed by its subscription store, so the
// cap numbers and the "is this owner subscribed?" logic live entirely in the
// private overlay — only the types in this package cross the module boundary.
package quota

// Policy decides whether a hosted account may grow. Core handlers compute the
// current counts (they own the data) and consult the injected Policy before
// mutating; the Policy only decides. Implementations must be safe for
// concurrent use.
type Policy interface {
	// AllowRepoCreate reports whether userID may own one more repo, given how
	// many they already own. A non-nil *LimitError means "blocked, offer
	// upgrade"; any other error is treated as a server error.
	AllowRepoCreate(userID string, ownedRepoCount int) error

	// AllowMemberAdd reports whether a repo may gain one more member, given the
	// repo's current owner IDs and its current member count (the owner counts).
	// The repo is uncapped if any of its owners has an active subscription.
	AllowMemberAdd(repoID string, ownerIDs []string, currentMemberCount int) error
}

// LimitError is returned by a Policy when an action is blocked by a plan limit.
// Its fields are authored by the hosted policy (so the cap numbers and upgrade
// copy never live in core) and read by the HTTP layer to build a 402 response.
type LimitError struct {
	Kind       string // "repos" | "members"
	Limit      int    // the free-tier cap that was hit
	Message    string // human-readable upgrade prompt
	UpgradeURL string // where to upgrade
}

func (e *LimitError) Error() string { return e.Message }

// Unlimited is the default Policy for builds that inject none (OSS/self-host).
// It never denies.
type Unlimited struct{}

func (Unlimited) AllowRepoCreate(string, int) error          { return nil }
func (Unlimited) AllowMemberAdd(string, []string, int) error { return nil }
