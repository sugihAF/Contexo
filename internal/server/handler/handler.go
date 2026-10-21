// Package handler holds the HTTP handlers for the Contexo git-backed server.
package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/sync"
	"github.com/sugihAF/contexo/internal/userstore"
	"github.com/sugihAF/contexo/quota"
)

// Handler holds dependencies for every HTTP route.
type Handler struct {
	store  *gitstore.Store
	users  *userstore.Store
	signer *auth.SessionSigner
	google auth.Verifier
	quota  quota.Policy
}

// New constructs a Handler. users/signer/google may be nil during tests of the
// gitstore-only paths. The quota policy defaults to Unlimited (no caps); the
// hosted build overrides it via SetQuota.
func New(store *gitstore.Store, users *userstore.Store, signer *auth.SessionSigner, google auth.Verifier) *Handler {
	return &Handler{store: store, users: users, signer: signer, google: google, quota: quota.Unlimited{}}
}

// SetQuota installs the hosted usage-limit policy. The OSS/self-host server
// never calls this, so its handler keeps the Unlimited default — caps are a
// hosted-only concern. app.Run wires this from a WithQuota option.
func (h *Handler) SetQuota(p quota.Policy) {
	if p == nil {
		p = quota.Unlimited{}
	}
	h.quota = p
}

// writeQuotaError maps a *quota.LimitError to 402 Payment Required with a
// structured body the CLI and dashboard render as an upgrade prompt. It returns
// true when it handled err (a real plan-limit rejection), false otherwise.
func (h *Handler) writeQuotaError(c *gin.Context, err error) bool {
	var le *quota.LimitError
	if errors.As(err, &le) {
		c.JSON(http.StatusPaymentRequired, gin.H{
			"error":       le.Message,
			"code":        "quota_" + le.Kind,
			"limit":       le.Limit,
			"upgrade_url": le.UpgradeURL,
		})
		return true
	}
	return false
}

// enforceRepoQuota reports whether uid may create one more repo, writing a 402
// (and returning false) when the injected quota policy blocks it. Legacy auth
// and the no-policy OSS/self-host build are never capped. Every repo-creation
// path consults this so the cap can't be sidestepped by choosing a different
// client (dashboard create, `ctx push` auto-create, or the legacy route).
func (h *Handler) enforceRepoQuota(c *gin.Context, uid string) bool {
	if auth.IsLegacy(uid) || h.users == nil {
		return true
	}
	owned, err := h.users.CountOwnedRepos(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if err := h.quota.AllowRepoCreate(uid, owned); err != nil {
		if h.writeQuotaError(c, err) {
			return false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	return true
}

// userID returns the authenticated user_id placed in the gin context by the
// auth middleware, or "" if missing.
func (h *Handler) userID(c *gin.Context) string {
	v, _ := c.Get("user_id")
	id, _ := v.(string)
	return id
}

// requireMember returns true (and writes the response) if the request is not
// authorized to act on repoID. Legacy auth always passes.
func (h *Handler) requireMember(c *gin.Context, repoID string) bool {
	uid := h.userID(c)
	if auth.IsLegacy(uid) {
		return true
	}
	if h.users == nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "user auth not configured"})
		return false
	}
	ok, err := h.users.IsMember(repoID, uid)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if !ok {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not a member of this repo"})
		return false
	}
	return true
}

// requireOwner is like requireMember but also checks role=owner.
func (h *Handler) requireOwner(c *gin.Context, repoID string) bool {
	uid := h.userID(c)
	if auth.IsLegacy(uid) {
		return true
	}
	if h.users == nil {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "user auth not configured"})
		return false
	}
	role, err := h.users.GetRole(repoID, uid)
	if err != nil {
		if errors.Is(err, userstore.ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "not a member of this repo"})
			return false
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if role != userstore.RoleOwner {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "owner role required"})
		return false
	}
	return true
}

// ListRepos handles GET /v1/repos. Returns every repo for legacy auth, or
// just the user's memberships for real users.
func (h *Handler) ListRepos(c *gin.Context) {
	uid := h.userID(c)
	all, err := h.store.ListReposWithMeta()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if auth.IsLegacy(uid) || h.users == nil {
		c.JSON(http.StatusOK, gin.H{"repos": all})
		return
	}
	memberships, err := h.users.ListUserRepos(uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	allowed := make(map[string]string, len(memberships))
	for _, m := range memberships {
		allowed[m.RepoID] = m.Role
	}
	filtered := make([]repoSummaryWithRole, 0, len(allowed))
	for _, r := range all {
		if role, ok := allowed[r.ID]; ok {
			filtered = append(filtered, repoSummaryWithRole{RepoSummary: r, Role: role})
		}
	}
	c.JSON(http.StatusOK, gin.H{"repos": filtered})
}

type repoSummaryWithRole struct {
	gitstore.RepoSummary
	Role string `json:"role"`
}

// ListPages handles GET /v1/repos/:id/pages.
func (h *Handler) ListPages(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
	pages, err := h.store.ListPages(repoID)
	if err != nil {
		if errors.Is(err, gitstore.ErrRepoNotFound) {
			errorWithCode(c, http.StatusNotFound, "repo not found", "repo_not_found")
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if pages == nil {
		pages = []gitstore.PageMeta{} // an empty repo lists [], never null
	}
	c.JSON(http.StatusOK, gin.H{"pages": pages})
}

// errorWithCode writes {"error": msg, "code": code}. Clients branch on the
// code; msg keeps the text older clients match ("repo not found", "page not
// found"), so adding the code is backward compatible.
func errorWithCode(c *gin.Context, status int, msg, code string) {
	c.JSON(status, gin.H{"error": msg, "code": code})
}

// CreateRepoLegacy handles the deprecated POST /v1/repos/:id used by the
// existing CLI's repo-init flow. Kept for back-compat with legacy auth; real
// users should use POST /v1/repos.
//
// For a signed-in user it never grants access to someone else's repo: an
// existing repo answers 201 (no membership change) only to a member, is
// claimed like POST /v1/repos only when it has no owner, and is otherwise a
// 409. Legacy auth (or no user store) keeps the idempotent Init.
func (h *Handler) CreateRepoLegacy(c *gin.Context) {
	repoID := c.Param("id")
	uid := h.userID(c)
	if auth.IsLegacy(uid) || h.users == nil {
		if err := h.store.Init(repoID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"repo_id": repoID})
		return
	}

	exists := h.store.Exists(repoID)
	if exists {
		member, err := h.users.IsMember(repoID, uid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if member {
			c.JSON(http.StatusCreated, gin.H{"repo_id": repoID})
			return
		}
		hasOwner, err := h.users.RepoHasOwner(repoID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if hasOwner {
			c.JSON(http.StatusConflict, gin.H{"error": "repo already exists"})
			return
		}
	}

	// The caller becomes a new owner (of a fresh repo or an orphan claim), so
	// consult the quota before creating anything.
	if !h.enforceRepoQuota(c, uid) {
		return
	}
	if !exists {
		if err := h.store.Init(repoID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := h.users.AddMember(repoID, uid, userstore.RoleOwner); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"repo_id": repoID})
}

// ReadPage handles GET /v1/repos/:id/pages/*path: the page at HEAD, or with
// ?at=<sha> the page as of that commit (capability pages.at).
//
// A ?at read answers 200 with X-Contexo-At (the requested sha echoed
// verbatim, abbreviations included) and X-Page-SHA (the path's own last
// commit at or before it). Servers that predate ?at ignore the parameter and
// serve HEAD with a 200, so clients trust a ?at response only when it echoes
// X-Contexo-At. Every error body carries a code: invalid_path, invalid_sha,
// repo_not_found, page_not_found (HEAD) or sha_not_found (?at).
func (h *Handler) ReadPage(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
	path := strings.TrimPrefix(c.Param("path"), "/")
	if path == "" {
		errorWithCode(c, http.StatusBadRequest, "missing page path", "invalid_path")
		return
	}

	if at, hasAt := c.GetQuery("at"); hasAt {
		// Validated before any git call, so a flag (--all, -G.) or a ref name
		// never reaches git. An empty ?at= is invalid, not "HEAD".
		if !gitstore.ValidSHA(at) {
			errorWithCode(c, http.StatusBadRequest, "invalid sha", "invalid_sha")
			return
		}
		content, pathSHA, err := h.store.ReadPageAt(repoID, path, at)
		if err != nil {
			switch {
			case errors.Is(err, gitstore.ErrRepoNotFound):
				errorWithCode(c, http.StatusNotFound, "repo not found", "repo_not_found")
			case errors.Is(err, gitstore.ErrUnsafePath):
				errorWithCode(c, http.StatusBadRequest, "invalid page path", "invalid_path")
			case errors.Is(err, gitstore.ErrUnknownSHA):
				errorWithCode(c, http.StatusNotFound, "sha not found", "sha_not_found")
			case errors.Is(err, gitstore.ErrPathNotAtSHA):
				errorWithCode(c, http.StatusNotFound, "page not found at sha", "sha_not_found")
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}
		c.Header("X-Contexo-At", at)
		c.Header("X-Page-SHA", pathSHA)
		c.Data(http.StatusOK, "text/markdown", content)
		return
	}

	content, sha, err := h.store.Read(repoID, path)
	if err != nil {
		switch {
		case errors.Is(err, gitstore.ErrRepoNotFound):
			errorWithCode(c, http.StatusNotFound, "repo not found", "repo_not_found")
		case errors.Is(err, os.ErrNotExist):
			errorWithCode(c, http.StatusNotFound, "page not found", "page_not_found")
		case errors.Is(err, gitstore.ErrUnsafePath):
			errorWithCode(c, http.StatusBadRequest, "invalid page path", "invalid_path")
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.Header("X-Page-SHA", sha)
	c.Data(http.StatusOK, "text/markdown", content)
}

// Push handles POST /v1/repos/:id/sync/push. For real users, the first push
// to a new repo auto-creates and assigns ownership.
func (h *Handler) Push(c *gin.Context) {
	repoID := c.Param("id")
	uid := h.userID(c)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
		return
	}
	var req sync.PushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad json: " + err.Error()})
		return
	}
	// expect_absent is read from the same bytes through a handler-local shadow
	// type: sync.PushFile is the client's wire type and gains the field
	// separately. Both decodes agree once it has.
	var flags pushFileFlags
	if err := json.Unmarshal(body, &flags); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad json: " + err.Error()})
		return
	}
	// Author attribution fallback chain:
	//   1. Use what the client sent (browser-login CLI rounds these through).
	//   2. If empty and the request is from a real authenticated user, look
	//      up their stored name/email — we have the user_id from auth
	//      middleware, so a PAT-only client (no browser login) still gets
	//      proper attribution instead of "unknown".
	//   3. Only fall through to the "unknown" sentinel if both the request
	//      and the user lookup are empty (legacy auth without user identity).
	if (req.AuthorName == "" || req.AuthorEmail == "") && !auth.IsLegacy(uid) && h.users != nil {
		if u, lerr := h.users.GetUserByID(uid); lerr == nil {
			if req.AuthorName == "" {
				req.AuthorName = u.Name
			}
			if req.AuthorEmail == "" {
				req.AuthorEmail = u.Email
			}
		}
	}
	if req.AuthorName == "" {
		req.AuthorName = "unknown"
	}
	if req.AuthorEmail == "" {
		req.AuthorEmail = "unknown@contexo.local"
	}
	if req.Message == "" {
		req.Message = "ctx push"
	}

	repoExists := h.store.Exists(repoID)
	if !repoExists {
		// First push to a new repo auto-creates it — gate that like any other
		// repo creation so the cap can't be bypassed via the CLI/agent.
		if !h.enforceRepoQuota(c, uid) {
			return
		}
		if err := h.store.Init(repoID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !auth.IsLegacy(uid) && h.users != nil {
			_ = h.users.AddMember(repoID, uid, userstore.RoleOwner)
		}
	} else {
		if !h.requireMember(c, repoID) {
			return
		}
	}

	var conflicts []sync.Conflict
	var pushed []sync.PushedFile
	var changed []string

	for i, file := range req.Files {
		res, conflict, err := h.store.WriteWithOptions(repoID, file.Path, []byte(file.Content), gitstore.WriteOptions{
			AuthorName:   req.AuthorName,
			AuthorEmail:  req.AuthorEmail,
			Message:      req.Message,
			ParentSHA:    file.ParentSHA,
			ExpectAbsent: flags.expectAbsent(i),
		})
		if err != nil {
			if errors.Is(err, gitstore.ErrConflict) {
				// Layer 4 enrichment: read the ancestor content (the version
				// the client's edit was based on) so the agent can three-way
				// merge. Best-effort — if the ancestor sha doesn't resolve,
				// the conflict is still returned, just without ancestor bytes.
				var ancestor []byte
				if conflict.ExpectedParentSHA != "" {
					if b, ferr := h.store.ReadAtSha(repoID, conflict.Path, conflict.ExpectedParentSHA); ferr == nil {
						ancestor = b
					}
				}
				conflicts = append(conflicts, sync.Conflict{
					Path:              conflict.Path,
					CurrentSHA:        conflict.CurrentSHA,
					CurrentContent:    conflict.CurrentContent,
					ExpectedParentSHA: conflict.ExpectedParentSHA,
					AncestorContent:   ancestor,
				})
				continue
			}
			if errors.Is(err, gitstore.ErrUnsafePath) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file path: " + file.Path})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// An unchanged page stays in pushed with its own last-touch commit, so
		// the client records a sha that is in the path's history.
		pushed = append(pushed, sync.PushedFile{Path: file.Path, SHA: res.SHA})
		if res.Changed {
			changed = append(changed, file.Path)
		}
	}

	// new_head is the repo HEAD after the batch, never the sha of whichever
	// file happened to be last (which, for an unchanged file, is an older
	// commit).
	newHead, _ := h.store.HeadSHA(repoID)

	status := http.StatusOK
	if len(conflicts) > 0 {
		status = http.StatusConflict
	}
	// Record a "push" only when at least one file was actually committed, and
	// list only those files, so re-pushing unchanged pages adds no noise.
	if len(changed) > 0 {
		h.recordActivity(c, repoID, "push", pushDetail(changed, activityClient(c)))
	}
	c.JSON(status, sync.PushResponse{
		NewHead:   newHead,
		Pushed:    pushed,
		Conflicts: conflicts,
	})
}

// pushFileFlags is a shadow decode of the push body for per-file flags that
// sync.PushFile does not carry.
type pushFileFlags struct {
	Files []struct {
		ExpectAbsent bool `json:"expect_absent"`
	} `json:"files"`
}

func (f pushFileFlags) expectAbsent(i int) bool {
	return i < len(f.Files) && f.Files[i].ExpectAbsent
}

// Pull handles GET /v1/repos/:id/sync/pull?since=<sha>.
func (h *Handler) Pull(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
	since := c.Query("since")

	paths, head, err := h.store.ChangedSince(repoID, since)
	if err != nil {
		if errors.Is(err, gitstore.ErrRepoNotFound) {
			errorWithCode(c, http.StatusNotFound, "repo not found", "repo_not_found")
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	files := make([]sync.PullFile, 0, len(paths))
	for _, p := range paths {
		content, sha, err := h.store.Read(repoID, p)
		if err != nil {
			continue
		}
		files = append(files, sync.PullFile{Path: p, Content: string(content), SHA: sha})
	}

	// Record a "pull" only when pages were actually received, so no-op
	// "already up to date" polls don't flood the feed.
	if len(files) > 0 {
		h.recordActivity(c, repoID, "pull", pullDetail(activityClient(c)))
	}
	c.JSON(http.StatusOK, sync.PullResponse{NewHead: head, Files: files})
}

// Timeline handles GET /v1/repos/:id/timeline?limit=N.
func (h *Handler) Timeline(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	commits, err := h.store.Log(repoID, limit)
	if err != nil {
		if errors.Is(err, gitstore.ErrRepoNotFound) {
			errorWithCode(c, http.StatusNotFound, "repo not found", "repo_not_found")
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"commits": commits})
}

// History handles GET /v1/repos/:id/history/*path?limit=N.
func (h *Handler) History(c *gin.Context) {
	repoID := c.Param("id")
	if !h.requireMember(c, repoID) {
		return
	}
	path := strings.TrimPrefix(c.Param("path"), "/")
	if path == "" {
		errorWithCode(c, http.StatusBadRequest, "missing path", "invalid_path")
		return
	}
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	commits, err := h.store.LogPath(repoID, path, limit)
	if err != nil {
		switch {
		case errors.Is(err, gitstore.ErrRepoNotFound):
			errorWithCode(c, http.StatusNotFound, "repo not found", "repo_not_found")
		case errors.Is(err, gitstore.ErrUnsafePath):
			errorWithCode(c, http.StatusBadRequest, "invalid page path", "invalid_path")
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"commits": commits})
}
