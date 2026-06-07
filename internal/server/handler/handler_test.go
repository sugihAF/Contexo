package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/sugihAF/contexo/internal/auth"
	"github.com/sugihAF/contexo/internal/server/gitstore"
	"github.com/sugihAF/contexo/internal/userstore"
)

// fakeVerifier returns canned claims for the given id_token literal.
type fakeVerifier map[string]*auth.GoogleClaims

func (f fakeVerifier) Verify(idToken string) (*auth.GoogleClaims, error) {
	if c, ok := f[idToken]; ok {
		return c, nil
	}
	return nil, errBadToken
}

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

const errBadToken fakeErr = "bad token"

type testRig struct {
	store    *gitstore.Store
	users    *userstore.Store
	signer   *auth.SessionSigner
	verifier fakeVerifier
	resolver *auth.Resolver
	router   *gin.Engine
	h        *Handler
}

func setupRig(t *testing.T) *testRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()

	store, err := gitstore.Open(filepath.Join(dir, "repos"))
	if err != nil {
		t.Fatalf("gitstore: %v", err)
	}
	users, err := userstore.Open(filepath.Join(dir, "contexo.db"))
	if err != nil {
		t.Fatalf("userstore: %v", err)
	}
	t.Cleanup(func() { users.Close() })

	signer, _ := auth.NewSessionSigner("0123456789abcdef0123456789abcdef")
	verifier := fakeVerifier{
		"alice-token": {Email: "alice@example.com", Name: "Alice", Subject: "g1", EmailVerified: true},
		"bob-token":   {Email: "bob@example.com", Name: "Bob", Subject: "g2", EmailVerified: true},
	}

	h := New(store, users, signer, verifier)
	r := gin.New()
	r.POST("/v1/auth/google", h.GoogleAuth)

	resolver := auth.NewResolver(signer, users, "legacy-key")
	v1 := r.Group("/v1")
	v1.Use(auth.GinMiddleware(resolver.Validator()))
	v1.GET("/me", h.Me)
	v1.GET("/repos", h.ListRepos)
	v1.POST("/repos", h.CreateRepo)
	v1.POST("/repos/join", h.JoinRepo)
	v1.POST("/repos/:id/invite-keys", h.MintInviteKey)
	v1.GET("/repos/:id/members", h.ListMembers)
	v1.DELETE("/repos/:id/members/:userId", h.RemoveMember)
	v1.POST("/pats", h.CreatePAT)
	v1.GET("/pats", h.ListPATs)

	return &testRig{
		store:    store,
		users:    users,
		signer:   signer,
		verifier: verifier,
		resolver: resolver,
		router:   r,
		h:        h,
	}
}

func doJSON(t *testing.T, r http.Handler, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestE2E_GoogleSignIn_FirstUserClaimsExistingRepos(t *testing.T) {
	rig := setupRig(t)

	// Pre-seed an existing repo on disk (simulating production data).
	if err := rig.store.Init("legacy-repo"); err != nil {
		t.Fatalf("seed legacy repo: %v", err)
	}

	// First user signs in — should claim ownership of the orphan repo.
	w := doJSON(t, rig.router, "POST", "/v1/auth/google", "", map[string]string{
		"id_token": "alice-token",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("google sign-in: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		AccessToken string `json:"access_token"`
		User        struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AccessToken == "" || resp.User.Email != "alice@example.com" {
		t.Errorf("unexpected sign-in response: %+v", resp)
	}

	// Alice's /v1/repos must now include legacy-repo.
	w = doJSON(t, rig.router, "GET", "/v1/repos", resp.AccessToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list repos: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("legacy-repo")) {
		t.Errorf("expected legacy-repo in list, got %s", w.Body.String())
	}

	// Bob signs in later — does NOT inherit any repos.
	w = doJSON(t, rig.router, "POST", "/v1/auth/google", "", map[string]string{
		"id_token": "bob-token",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("bob sign-in: %d %s", w.Code, w.Body.String())
	}
	var bobResp struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &bobResp)

	w = doJSON(t, rig.router, "GET", "/v1/repos", bobResp.AccessToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("bob list: %d", w.Code)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("legacy-repo")) {
		t.Errorf("bob should NOT see legacy-repo, got: %s", w.Body.String())
	}
}

func TestE2E_InviteKeyFlow(t *testing.T) {
	rig := setupRig(t)

	// Alice signs in (becomes first user, no existing repos).
	aliceToken := signIn(t, rig.router, "alice-token")

	// Alice creates a repo.
	w := doJSON(t, rig.router, "POST", "/v1/repos", aliceToken, map[string]string{"id": "team-repo"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create repo: %d %s", w.Code, w.Body.String())
	}

	// Alice mints an invite key.
	w = doJSON(t, rig.router, "POST", "/v1/repos/team-repo/invite-keys", aliceToken, map[string]string{"label": "for-bob"})
	if w.Code != http.StatusCreated {
		t.Fatalf("mint key: %d %s", w.Code, w.Body.String())
	}
	var mint struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &mint)
	if mint.Token == "" {
		t.Fatal("expected token in response")
	}

	// Bob signs in.
	bobToken := signIn(t, rig.router, "bob-token")

	// Before joining, Bob doesn't see team-repo.
	w = doJSON(t, rig.router, "GET", "/v1/repos", bobToken, nil)
	if bytes.Contains(w.Body.Bytes(), []byte("team-repo")) {
		t.Error("bob saw team-repo before joining")
	}

	// Bob joins with the key.
	w = doJSON(t, rig.router, "POST", "/v1/repos/join", bobToken, map[string]string{"key": mint.Token})
	if w.Code != http.StatusOK {
		t.Fatalf("join: %d %s", w.Code, w.Body.String())
	}

	// Bob now sees team-repo.
	w = doJSON(t, rig.router, "GET", "/v1/repos", bobToken, nil)
	if !bytes.Contains(w.Body.Bytes(), []byte("team-repo")) {
		t.Errorf("bob should see team-repo after join, got: %s", w.Body.String())
	}

	// Bob cannot mint invite keys (not owner).
	w = doJSON(t, rig.router, "POST", "/v1/repos/team-repo/invite-keys", bobToken, nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("bob expected 403 minting invite key, got %d %s", w.Code, w.Body.String())
	}
}

func TestE2E_PATAuth(t *testing.T) {
	rig := setupRig(t)
	aliceToken := signIn(t, rig.router, "alice-token")

	// Mint PAT.
	w := doJSON(t, rig.router, "POST", "/v1/pats", aliceToken, map[string]string{"label": "laptop"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create pat: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Token == "" {
		t.Fatal("expected token in response")
	}

	// Hit /v1/me using the PAT.
	w = doJSON(t, rig.router, "GET", "/v1/me", resp.Token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("me with PAT: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("alice@example.com")) {
		t.Errorf("expected alice's email in /v1/me response, got: %s", w.Body.String())
	}
}

func TestE2E_LegacyKeySeesAllRepos(t *testing.T) {
	rig := setupRig(t)
	// Seed two repos as if via legacy push.
	_ = rig.store.Init("orphan-1")
	_ = rig.store.Init("orphan-2")

	w := doJSON(t, rig.router, "GET", "/v1/repos", "legacy-key", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy list: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("orphan-1")) ||
		!bytes.Contains(w.Body.Bytes(), []byte("orphan-2")) {
		t.Errorf("legacy auth should see all repos, got: %s", w.Body.String())
	}
}

func signIn(t *testing.T, r http.Handler, idToken string) string {
	t.Helper()
	w := doJSON(t, r, "POST", "/v1/auth/google", "", map[string]string{"id_token": idToken})
	if w.Code != http.StatusOK {
		t.Fatalf("signIn(%s): %d %s", idToken, w.Code, w.Body.String())
	}
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.AccessToken
}

func TestE2E_ListMembers(t *testing.T) {
	rig := setupRig(t)
	aliceToken := signIn(t, rig.router, "alice-token")
	w := doJSON(t, rig.router, "POST", "/v1/repos", aliceToken, map[string]string{"id": "team-repo"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create repo: %d %s", w.Code, w.Body.String())
	}

	w = doJSON(t, rig.router, "GET", "/v1/repos/team-repo/members", aliceToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list members: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("alice@example.com")) ||
		!bytes.Contains(w.Body.Bytes(), []byte("owner")) {
		t.Errorf("expected alice as owner in members list, got %s", w.Body.String())
	}
}

func TestE2E_RemoveMember(t *testing.T) {
	rig := setupRig(t)
	aliceToken := signIn(t, rig.router, "alice-token")
	doJSON(t, rig.router, "POST", "/v1/repos", aliceToken, map[string]string{"id": "team-repo"})

	// Alice mints a key; Bob joins as a member.
	w := doJSON(t, rig.router, "POST", "/v1/repos/team-repo/invite-keys", aliceToken, map[string]string{"label": "for-bob"})
	var mint struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &mint)
	bobToken := signIn(t, rig.router, "bob-token")
	w = doJSON(t, rig.router, "POST", "/v1/repos/join", bobToken, map[string]string{"key": mint.Token})
	if w.Code != http.StatusOK {
		t.Fatalf("bob join: %d %s", w.Code, w.Body.String())
	}

	// Resolve user ids from the members list.
	w = doJSON(t, rig.router, "GET", "/v1/repos/team-repo/members", aliceToken, nil)
	var members struct {
		Members []struct {
			UserID string `json:"user_id"`
			Email  string `json:"email"`
		} `json:"members"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &members)
	var aliceID, bobID string
	for _, m := range members.Members {
		switch m.Email {
		case "alice@example.com":
			aliceID = m.UserID
		case "bob@example.com":
			bobID = m.UserID
		}
	}
	if aliceID == "" || bobID == "" {
		t.Fatalf("could not resolve user ids from %s", w.Body.String())
	}

	// A regular member cannot remove anyone.
	w = doJSON(t, rig.router, "DELETE", "/v1/repos/team-repo/members/"+bobID, bobToken, nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("member removing -> want 403, got %d", w.Code)
	}

	// The owner removes Bob.
	w = doJSON(t, rig.router, "DELETE", "/v1/repos/team-repo/members/"+bobID, aliceToken, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("owner remove -> want 204, got %d %s", w.Code, w.Body.String())
	}

	// Bob no longer sees the repo.
	w = doJSON(t, rig.router, "GET", "/v1/repos", bobToken, nil)
	if bytes.Contains(w.Body.Bytes(), []byte("team-repo")) {
		t.Error("bob still sees team-repo after removal")
	}

	// The last owner cannot be removed.
	w = doJSON(t, rig.router, "DELETE", "/v1/repos/team-repo/members/"+aliceID, aliceToken, nil)
	if w.Code != http.StatusConflict {
		t.Errorf("last-owner removal -> want 409, got %d %s", w.Code, w.Body.String())
	}
}

func TestE2E_InviteKeyExpiry(t *testing.T) {
	rig := setupRig(t)
	aliceToken := signIn(t, rig.router, "alice-token")
	doJSON(t, rig.router, "POST", "/v1/repos", aliceToken, map[string]string{"id": "team-repo"})

	// Minting returns a future expires_at.
	w := doJSON(t, rig.router, "POST", "/v1/repos/team-repo/invite-keys", aliceToken, map[string]string{"label": "x"})
	if w.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", w.Code, w.Body.String())
	}
	var mint struct {
		Token string `json:"token"`
		Key   struct {
			ExpiresAt int64 `json:"expires_at"`
		} `json:"key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &mint)
	if mint.Key.ExpiresAt <= time.Now().Unix() {
		t.Errorf("expected a future expires_at, got %d", mint.Key.ExpiresAt)
	}

	// Force the key to expire; a join must then be rejected with 410 Gone.
	if _, err := rig.users.DB().Exec(
		`UPDATE repo_invite_keys SET expires_at = ? WHERE repo_id = ?`,
		time.Now().Add(-time.Hour).Unix(), "team-repo",
	); err != nil {
		t.Fatalf("expire key: %v", err)
	}
	bobToken := signIn(t, rig.router, "bob-token")
	w = doJSON(t, rig.router, "POST", "/v1/repos/join", bobToken, map[string]string{"key": mint.Token})
	if w.Code != http.StatusGone {
		t.Errorf("expired-key join -> want 410, got %d %s", w.Code, w.Body.String())
	}
}
