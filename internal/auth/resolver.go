package auth

import (
	"errors"
	"strings"

	"github.com/sugihAF/contexo/internal/userstore"
)

// LegacyAdminID is the sentinel user_id assigned when a request authenticates
// with the deprecated CONTEXO_API_KEY shared secret. Handlers may use this to
// bypass per-user membership checks while phase-1 ships ahead of the CLI and
// dashboard rewrites.
const LegacyAdminID = "legacy:admin"

// Resolver turns a Bearer token into a user_id. It accepts three flavors:
//   - Session JWT (HS256, issued by SessionSigner) — for dashboard sessions
//   - Personal Access Token (prefix "ctxp_") — for CLI/MCP
//   - Legacy API key (matches CONTEXO_API_KEY env var) — for back-compat
type Resolver struct {
	session   *SessionSigner
	users     *userstore.Store
	legacyKey string
}

// NewResolver constructs a Resolver. Any of session/users may be nil to
// disable a flavor. legacyKey may be "" to disable legacy auth.
func NewResolver(session *SessionSigner, users *userstore.Store, legacyKey string) *Resolver {
	return &Resolver{session: session, users: users, legacyKey: legacyKey}
}

// Resolve attempts to extract a user_id from the bearer token.
func (r *Resolver) Resolve(token string) (string, error) {
	if r == nil || token == "" {
		return "", errors.New("auth: empty token")
	}
	switch {
	case strings.HasPrefix(token, userstore.PATPrefix):
		if r.users == nil {
			return "", errors.New("auth: pat auth disabled")
		}
		userID, err := r.users.ResolvePAT(token)
		if err != nil {
			return "", err
		}
		return userID, nil
	case r.legacyKey != "" && token == r.legacyKey:
		return LegacyAdminID, nil
	default:
		if r.session == nil {
			return "", errors.New("auth: session auth disabled")
		}
		userID, _, err := r.session.Verify(token)
		if err != nil {
			return "", err
		}
		return userID, nil
	}
}

// Validator returns a KeyValidator suitable for GinMiddleware.
func (r *Resolver) Validator() KeyValidator {
	return func(token string) (string, bool) {
		userID, err := r.Resolve(token)
		if err != nil {
			return "", false
		}
		return userID, true
	}
}

// IsLegacy reports whether userID refers to the legacy shared-key auth path.
func IsLegacy(userID string) bool {
	return userID == LegacyAdminID
}
