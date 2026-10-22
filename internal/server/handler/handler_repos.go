package handler

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/userstore"
)

var repoIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type createRepoRequest struct {
	ID string `json:"id"`
}

type createRepoResponse struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// CreateRepo handles POST /v1/repos: a signed-in user creates a NEW repo and
// becomes its owner (201 {"id","role":"owner"}, the shape the dashboard
// reads). Legacy auth has no user to own a repo (403).
//
// Every id must match repoIDPattern, checked before any existence check:
// any other id is 400 repoIDRule, even one of an existing repo, so a live
// dotted repo such as "my.repo" (hosted may hold one) is a 400 for everyone,
// its owner included, never a 409. An existing repo with a canonical id is
// 409 "repo already exists" for everyone, its owner and members included,
// and is never claimed: not an ownerless one (only the first sign-in
// bootstrap in GoogleAuth adopts those), and not an alias of another id's
// directory. gitstore keeps only the [A-Za-z0-9_-] characters of an id and a
// case-insensitive filesystem folds case, so "myrepo" can name the directory
// of "my.repo", and "case-repo" that of "Case-Repo", whose membership rows
// use the other id.
//
// Creation is race-safe under createRepoMu: of concurrent creates of one id
// on any route, only the request that created the repo becomes its owner.
// Once creation is definitive, the stale rows of the id and of its aliases
// (left by an operator-deleted repo of the same directory) are purged before
// the owner row is added. See createRepoAsOwner.
func (h *Handler) CreateRepo(c *gin.Context) {
	uid := h.userID(c)
	if h.users == nil || auth.IsLegacy(uid) {
		c.JSON(http.StatusForbidden, gin.H{"error": "user session required"})
		return
	}
	var req createRepoRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}
	created, ok := h.createRepoAsOwner(c, req.ID, uid)
	if !ok {
		return
	}
	if !created {
		c.JSON(http.StatusConflict, gin.H{"error": "repo already exists"})
		return
	}
	c.JSON(http.StatusCreated, createRepoResponse{ID: req.ID, Role: userstore.RoleOwner})
}

// createRepoAsOwner creates the NEW repo repoID with uid, a signed-in user
// (h.users is set), as its only member, the owner. It is every signed-in
// creation: POST /v1/repos, and through createOwnedRepo a signed-in first
// push and the deprecated create. created is true only for the request that
// created the repo; created is false with ok true when the repo exists or
// another request created it first (the caller applies its existing-repo
// rules and must not claim it); ok is false when the response is already
// written: 400 for an id outside repoIDPattern, 402 from the quota, 500 when
// a store call fails.
//
// After the pattern check, in this order:
//  1. An existing repo (an alias of one included) is left alone: nothing is
//     claimed, added or purged. It is checked here without createRepoMu, so
//     refusing an existing id never waits for another creation, and again
//     in step 3.
//  2. The known repo ids are listed before the repo appears, which keeps
//     the window between the repo appearing and the purge short, and before
//     createRepoMu, which every creation, push membership check and
//     invite-key route waits for: the listing needs no lock. A row written
//     after it for an id that names the new directory would be that id's
//     first row, and an id gets its first row only from a create, which
//     makes the directory (this request then stops in step 3 or 5), from the
//     first sign-in, which adopts a directory that exists, or from a route
//     that needs rows of the id already (join, mint, a signed-in push or
//     pull). repoID's own rows are purged whether they are listed or not.
//  3. createRepoMu is taken, and the repo is checked again.
//  4. The quota: a 402 creates nothing.
//  5. gitstore.Create. created=false means another process or an operator
//     created it in between: not ours.
//  6. created=true is definitive (.git was certainly absent), so every row of
//     repoID and of the aliases of its directory belongs to a repo an
//     operator deleted. They are purged before the owner row exists, so an
//     old member, invite key or activity feed cannot reach the new repo. The
//     purge never runs before or without created=true: a stat error can make
//     a live repo look absent, and purging it would wipe its members.
//  7. The owner row must be inserted by this request. A row for uid that
//     appeared after the purge means this request did not establish the
//     ownership it would report, so it fails closed (500) instead.
//
// A failure after step 5 leaves the new repo empty and ownerless. Since an
// existing repo is never claimed, an operator has to remove that directory.
//
// createRepoMu is not reentrant: it is taken once, and only h.store.Exists
// and Create, h.enforceRepoQuota, PurgeRepo, AddMemberIfAbsent and the
// os.Stat calls of repoAliases run under it, never another holder of it
// (createLegacyRepo, isMemberAfterCreate and so requireMemberAfterCreate,
// JoinRepo, MintInviteKey, ListInviteKeys, DeleteInviteKey, RemoveMember), nor
// createOwnedRepo, which is this function. The lock order is createRepoMu,
// then the gitstore repo lock (inside Create), then SQLite.
//
// Around the purge, the decisions that write, list or delete access rows,
// or write content, are made under createRepoMu. The invite-key routes check
// the key or the owner row and act under it, so a read of a stale row can no
// longer be followed by an insert that lands right after the purge (in WAL
// mode the read saw the rows while the purge was open, and a client that
// kept retrying won on every re-create). A push or deprecated create decides
// membership under it (isMemberAfterCreate), so a former member's push that
// sees .git before step 6 waits for the purge and is refused. And the
// legacy-key creates (createLegacyRepo) purge too.
//
// The read routes check membership without createRepoMu (requireMember) and
// read afterwards, so a former member's check can see its stale row just
// before the purge commits while the read runs after the new owner's row or
// first push. They decide again from what they read instead: the member list
// answers only a caller it lists itself, and the other reads (pages, pull,
// timeline, history, activity, diff, evolution) re-check membership after
// the read and before answering (memberAfterRead). The removal of a member
// decides under createRepoMu, like the invite-key routes.
func (h *Handler) createRepoAsOwner(c *gin.Context, repoID, uid string) (created, ok bool) {
	if !repoIDPattern.MatchString(repoID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": repoIDRule})
		return false, false
	}
	if h.store.Exists(repoID) {
		return false, true
	}
	known, err := h.users.KnownRepoIDs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false, false
	}
	createRepoMu.Lock()
	defer createRepoMu.Unlock()
	if h.store.Exists(repoID) {
		return false, true
	}
	if !h.enforceRepoQuota(c, uid) {
		return false, false
	}
	created, err = h.store.Create(repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false, false
	}
	if !created {
		return false, true
	}
	if err := h.purgeStaleAccess(repoID, known); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false, false
	}
	inserted, err := h.users.AddMemberIfAbsent(repoID, uid, userstore.RoleOwner)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false, false
	}
	if !inserted {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "repo owner row not added"})
		return false, false
	}
	return true, true
}

// purgeStaleAccess deletes the membership rows, invite keys and activity of
// repoID and of every certain alias of its directory among known, in one
// transaction. Call it only after gitstore.Create reported created=true for
// repoID. An alias repoAliases could not resolve keeps its rows (never
// guess), and its error is returned after the purge, so the create still
// fails without an owner row.
func (h *Handler) purgeStaleAccess(repoID string, known []string) error {
	aliases, aliasErr := h.repoAliases(repoID, known)
	return errors.Join(h.users.PurgeRepo(repoID, aliases...), aliasErr)
}

// repoAliases returns the ids among known, other than repoID, whose gitstore
// directory is repoID's directory (which exists). An id is an alias on every
// filesystem when repoDirName(id) == repoID ("my.repo" for "myrepo"). An id
// whose directory name differs from repoID only in case is an alias only
// where the filesystem folds case, as os.SameFile on the two directories
// shows; a variant that does not exist is another, absent repo (a
// case-sensitive filesystem). Near misses such as "my-repo" or "myrepo2"
// never are: the match is exact up to case.
//
// A variant whose directory cannot be stat'ed for another reason (EACCES,
// EIO) is never guessed to be an alias or not: it is left out and reported in
// err. The certain aliases are returned with that error, so the caller can
// still purge them: the stale rows of a directory just created must not
// survive because of one uncertain variant.
func (h *Handler) repoAliases(repoID string, known []string) (aliases []string, err error) {
	var (
		errs    []error
		dir     os.FileInfo
		dirErr  error
		statted bool
	)
	for _, id := range known {
		if id == repoID {
			continue
		}
		name := repoDirName(id)
		if name == "" || !strings.EqualFold(name, repoID) {
			continue
		}
		if name == repoID {
			aliases = append(aliases, id)
			continue
		}
		fi, statErr := os.Stat(filepath.Join(h.store.Root, name))
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		if statErr != nil {
			errs = append(errs, fmt.Errorf("handler: repo alias %q: %w", id, statErr))
			continue
		}
		if !statted {
			// Stat'ed lazily: only a case variant that exists needs it.
			dir, dirErr = os.Stat(filepath.Join(h.store.Root, repoID))
			statted = true
		}
		if dirErr != nil {
			errs = append(errs, fmt.Errorf("handler: repo alias %q: %w", id, dirErr))
			continue
		}
		if os.SameFile(fi, dir) {
			aliases = append(aliases, id)
		}
	}
	return aliases, errors.Join(errs...)
}

// repoDirName is the name of the directory gitstore keeps repoID in: the id
// with every character outside [A-Za-z0-9_-] dropped ("" when none is left).
// It must match gitstore's sanitize; TestRepoDirNameMirrorsGitstore pins it.
func repoDirName(repoID string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			return r
		default:
			return -1
		}
	}, repoID)
}
