package userstore

import (
	"path/filepath"
	"strings"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertGoogleUser_NewAndUpdate(t *testing.T) {
	s := openTestStore(t)

	if n, err := s.CountUsers(); err != nil || n != 0 {
		t.Fatalf("expected 0 users, got %d (err=%v)", n, err)
	}

	u, isNew, err := s.UpsertGoogleUser("Alice@Example.com ", "Alice", "google-sub-1")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !isNew {
		t.Fatal("expected isNew=true on first insert")
	}
	if u.Email != "alice@example.com" {
		t.Errorf("email not lowercased/trimmed: %q", u.Email)
	}
	if u.ID == "" {
		t.Error("expected user id to be assigned")
	}

	// Second upsert: name+sub change, no new row.
	u2, isNew2, err := s.UpsertGoogleUser("alice@example.com", "Alice Doe", "google-sub-2")
	if err != nil {
		t.Fatalf("upsert#2: %v", err)
	}
	if isNew2 {
		t.Error("expected isNew=false on update")
	}
	if u2.ID != u.ID {
		t.Errorf("user id changed across upserts: %s -> %s", u.ID, u2.ID)
	}
	if u2.Name != "Alice Doe" {
		t.Errorf("name not updated: %q", u2.Name)
	}

	if n, _ := s.CountUsers(); n != 1 {
		t.Errorf("expected 1 user, got %d", n)
	}
}

func TestGetUserByEmail_NotFound(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.GetUserByEmail("ghost@example.com"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMintAndResolvePAT(t *testing.T) {
	s := openTestStore(t)
	u, _, _ := s.UpsertGoogleUser("bob@example.com", "Bob", "sub")

	pat, raw, err := s.MintPAT(u.ID, "laptop")
	if err != nil {
		t.Fatalf("mint pat: %v", err)
	}
	if !strings.HasPrefix(raw, PATPrefix) {
		t.Errorf("expected raw token to start with %q, got %q", PATPrefix, raw)
	}
	if pat.UserID != u.ID || pat.Label != "laptop" {
		t.Errorf("pat mismatch: %+v", pat)
	}

	gotUser, err := s.ResolvePAT(raw)
	if err != nil {
		t.Fatalf("resolve pat: %v", err)
	}
	if gotUser != u.ID {
		t.Errorf("resolve returned %q, want %q", gotUser, u.ID)
	}

	if _, err := s.ResolvePAT("ctxp_not-a-real-token"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound for bogus token, got %v", err)
	}

	// last_used_at is bumped after Resolve.
	pats, err := s.ListPATs(u.ID)
	if err != nil {
		t.Fatalf("list pats: %v", err)
	}
	if len(pats) != 1 {
		t.Fatalf("expected 1 pat, got %d", len(pats))
	}
	if pats[0].LastUsedAt == nil {
		t.Error("expected LastUsedAt to be set after Resolve")
	}
}

func TestDeletePAT_ScopedToUser(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")
	bob, _, _ := s.UpsertGoogleUser("bob@example.com", "B", "2")

	_, _, _ = s.MintPAT(alice.ID, "alice-laptop")
	bobPAT, _, _ := s.MintPAT(bob.ID, "bob-laptop")

	// Alice cannot delete Bob's PAT.
	if err := s.DeletePAT(alice.ID, bobPAT.ID); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	// Bob can.
	if err := s.DeletePAT(bob.ID, bobPAT.ID); err != nil {
		t.Errorf("bob delete own pat: %v", err)
	}
}

func TestMembership(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")

	if err := s.AddMember("repo-a", alice.ID, RoleOwner); err != nil {
		t.Fatalf("add member: %v", err)
	}
	role, err := s.GetRole("repo-a", alice.ID)
	if err != nil {
		t.Fatalf("get role: %v", err)
	}
	if role != RoleOwner {
		t.Errorf("expected owner, got %q", role)
	}
	if ok, _ := s.IsMember("repo-a", alice.ID); !ok {
		t.Error("expected IsMember=true")
	}
	if ok, _ := s.IsMember("repo-z", alice.ID); ok {
		t.Error("expected IsMember=false for unrelated repo")
	}

	// Idempotent: second AddMember with role=member preserves owner role.
	if err := s.AddMember("repo-a", alice.ID, RoleMember); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	role, _ = s.GetRole("repo-a", alice.ID)
	if role != RoleOwner {
		t.Errorf("role downgraded on idempotent AddMember: %q", role)
	}

	if has, _ := s.RepoHasOwner("repo-a"); !has {
		t.Error("expected RepoHasOwner=true")
	}
	if has, _ := s.RepoHasOwner("repo-empty"); has {
		t.Error("expected RepoHasOwner=false for non-existent repo")
	}

	repos, err := s.ListUserRepos(alice.ID)
	if err != nil || len(repos) != 1 || repos[0].RepoID != "repo-a" {
		t.Errorf("ListUserRepos mismatch: %+v err=%v", repos, err)
	}
}

func TestInviteKey_LifecycleAndIsolation(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")

	k, raw, err := s.MintInviteKey("repo-a", alice.ID, "team-onboarding")
	if err != nil {
		t.Fatalf("mint invite: %v", err)
	}
	if !strings.HasPrefix(raw, InviteKeyPrefix) {
		t.Errorf("expected prefix %q, got %q", InviteKeyPrefix, raw)
	}

	got, err := s.ResolveInviteKey(raw)
	if err != nil {
		t.Fatalf("resolve invite: %v", err)
	}
	if got != "repo-a" {
		t.Errorf("resolve returned %q, want repo-a", got)
	}

	// Listing scoped per repo.
	keys, _ := s.ListInviteKeys("repo-a")
	if len(keys) != 1 {
		t.Errorf("expected 1 key, got %d", len(keys))
	}
	keys, _ = s.ListInviteKeys("repo-b")
	if len(keys) != 0 {
		t.Errorf("expected 0 keys on unrelated repo, got %d", len(keys))
	}

	if err := s.DeleteInviteKey("repo-a", k.ID); err != nil {
		t.Errorf("delete invite: %v", err)
	}
	if _, err := s.ResolveInviteKey(raw); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}
