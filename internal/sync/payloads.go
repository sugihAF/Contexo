package sync

import (
	"time"

	"github.com/sugihAF/contexo/internal/diff"
)

// PushRequest is sent to POST /v1/repos/:id/sync/push.
type PushRequest struct {
	AuthorName  string     `json:"author_name"`
	AuthorEmail string     `json:"author_email"`
	Message     string     `json:"message"`
	Files       []PushFile `json:"files"`
}

// PushFile is one file in a push request.
type PushFile struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	ParentSHA string `json:"parent_sha,omitempty"`
}

// PushResponse is what the server returns for a push.
type PushResponse struct {
	NewHead   string       `json:"new_head"`
	Pushed    []PushedFile `json:"pushed,omitempty"`
	Conflicts []Conflict   `json:"conflicts,omitempty"`
}

// PushedFile carries a path and the sha of the commit that created/updated it.
type PushedFile struct {
	Path string `json:"path"`
	SHA  string `json:"sha"`
}

// Conflict mirrors gitstore.Conflict on the wire. AncestorContent is
// optional; it's populated when the server can locate the ExpectedParentSHA's
// content, enabling three-way merge by the MCP agent (Layer 4).
type Conflict struct {
	Path              string `json:"path"`
	CurrentSHA        string `json:"current_sha"`
	CurrentContent    []byte `json:"current_content"`
	ExpectedParentSHA string `json:"expected_parent_sha"`
	AncestorContent   []byte `json:"ancestor_content,omitempty"`
}

// PullResponse is the response from GET /v1/repos/:id/sync/pull.
type PullResponse struct {
	NewHead string     `json:"new_head"`
	Files   []PullFile `json:"files"`
}

// PullFile is one file in a PullResponse.
type PullFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA     string `json:"sha"`
}

// Commit mirrors gitstore.CommitMeta on the wire.
type Commit struct {
	SHA     string    `json:"sha"`
	Author  string    `json:"author"`
	Email   string    `json:"email"`
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// EvolutionEntry pairs one commit with its diff against the prior commit for
// the same path. Mirrors handler.EvolutionEntry on the wire.
type EvolutionEntry struct {
	Commit Commit           `json:"commit"`
	Diff   diff.SectionDiff `json:"diff"`
}

// RepoOption is one entry in the response from GET /v1/repos as the CLI
// cares about it. Other server-side fields (page_count, last_commit) are
// ignored when the CLI just needs to enumerate memberships.
type RepoOption struct {
	ID   string `json:"id"`
	Role string `json:"role,omitempty"`
}

// InviteKey is one entry in /v1/repos/:id/invite-keys responses (mint + list).
// Mirrors handler.inviteKeyBody on the wire. CreatedAt and ExpiresAt are Unix
// seconds.
type InviteKey struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}
