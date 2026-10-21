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
// An existing repo is 409 "repo already exists" for everyone, its owner and
// members included, and is never claimed: not an ownerless one (only the
// first sign-in bootstrap in GoogleAuth adopts those), and not an alias of
// another id's directory. gitstore keeps only the [A-Za-z0-9_-] characters of
// an id and a case-insensitive filesystem folds case, so "myrepo" can name
// the directory of "my.repo", and "case-repo" that of "Case-Repo", whose
// membership rows use the other id. A new id must match repoIDPattern (400,
// before any existence check).
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
// (h.users is set), as its only member, the owner. Its contract is
// createOwnedRepo's, so that createOwnedRepo can delegate to it: created is
// true only for the request that created the repo; created is false with ok
// true when the repo exists or another request created it first (the caller
// applies its existing-repo rules and must not claim it); ok is false when
// the response is already written: 400 for an id outside repoIDPattern, 402
// from the quota, 500 when a store call fails.
//
// Everything after the pattern check runs under createRepoMu, in this order:
//  1. An existing repo (an alias of one included) is left alone: nothing is
//     claimed, added or purged.
//  2. The quota: a 402 creates nothing.
//  3. The known repo ids are listed before the repo appears, which keeps the
//     window between the repo appearing and the purge short.
//  4. gitstore.Create. created=false means someone outside createRepoMu
//     (legacy auth, another process) created it in between: not ours.
//  5. created=true is definitive (.git was certainly absent), so every row of
//     repoID and of the aliases of its directory belongs to a repo an
//     operator deleted. They are purged before the owner row exists, so an
//     old member, invite key or activity feed cannot reach the new repo. The
//     purge never runs before or without created=true: a stat error can make
//     a live repo look absent, and purging it would wipe its members.
//  6. The owner row must be inserted by this request. A row for uid that
//     appeared after the purge means this request did not establish the
//     ownership it would report, so it fails closed (500) instead.
//
// A failure after step 4 leaves the new repo empty and ownerless, as
// createOwnedRepo does when AddMember fails. Since an existing repo is never
// claimed, an operator has to remove that directory.
//
// createRepoMu is not reentrant: it is taken once, and only h.store.Exists
// and Create, h.enforceRepoQuota, h.users methods and the os.Stat calls of
// repoAliases run under it, never createOwnedRepo, isMemberAfterCreate or
// requireMemberAfterCreate. The lock order is createRepoMu, then the gitstore
// repo lock (inside Create), then SQLite.
//
// Known gaps, outside this file: git init creates .git a few tens of
// milliseconds before step 5 commits, and in that window the repo exists
// while its stale rows still grant access. A former member's push sees
// Exists, passes requireMemberAfterCreate on its stale row without waiting
// for createRepoMu, and can commit into the new repo once Create releases
// the repo lock. And invite-key mint and join check a row and then insert
// without createRepoMu, so a key minted, or a join resolved, against a stale
// row before the purge can be inserted after it.
func (h *Handler) createRepoAsOwner(c *gin.Context, repoID, uid string) (created, ok bool) {
	if !repoIDPattern.MatchString(repoID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": repoIDRule})
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
	known, err := h.users.KnownRepoIDs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
