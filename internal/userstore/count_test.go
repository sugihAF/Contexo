package userstore

import "testing"

func TestCountOwnedRepos(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")
	bob, _, _ := s.UpsertGoogleUser("bob@example.com", "B", "2")

	// Alice owns repo-a and repo-b; she is only a member of repo-c.
	_ = s.AddMember("repo-a", alice.ID, RoleOwner)
	_ = s.AddMember("repo-b", alice.ID, RoleOwner)
	_ = s.AddMember("repo-c", bob.ID, RoleOwner)
	_ = s.AddMember("repo-c", alice.ID, RoleMember)

	if n, err := s.CountOwnedRepos(alice.ID); err != nil || n != 2 {
		t.Errorf("CountOwnedRepos(alice) = %d, %v; want 2", n, err)
	}
	if n, err := s.CountOwnedRepos(bob.ID); err != nil || n != 1 {
		t.Errorf("CountOwnedRepos(bob) = %d, %v; want 1", n, err)
	}
	if n, _ := s.CountOwnedRepos("ghost"); n != 0 {
		t.Errorf("CountOwnedRepos(ghost) = %d; want 0", n)
	}
}

func TestCountRepoMembers(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")
	bob, _, _ := s.UpsertGoogleUser("bob@example.com", "B", "2")

	// A fresh repo counts its owner as a member (owner counts toward the cap).
	_ = s.AddMember("repo-a", alice.ID, RoleOwner)
	if n, err := s.CountRepoMembers("repo-a"); err != nil || n != 1 {
		t.Errorf("CountRepoMembers after owner = %d, %v; want 1", n, err)
	}
	_ = s.AddMember("repo-a", bob.ID, RoleMember)
	if n, err := s.CountRepoMembers("repo-a"); err != nil || n != 2 {
		t.Errorf("CountRepoMembers(repo-a) = %d, %v; want 2", n, err)
	}
	if n, _ := s.CountRepoMembers("repo-empty"); n != 0 {
		t.Errorf("CountRepoMembers(repo-empty) = %d; want 0", n)
	}
}

func TestRepoOwners(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")
	bob, _, _ := s.UpsertGoogleUser("bob@example.com", "B", "2")
	carol, _, _ := s.UpsertGoogleUser("carol@example.com", "C", "3")

	_ = s.AddMember("repo-a", alice.ID, RoleOwner)
	_ = s.AddMember("repo-a", bob.ID, RoleOwner)
	_ = s.AddMember("repo-a", carol.ID, RoleMember)

	owners, err := s.RepoOwners("repo-a")
	if err != nil {
		t.Fatalf("RepoOwners: %v", err)
	}
	set := map[string]bool{}
	for _, o := range owners {
		set[o] = true
	}
	if len(owners) != 2 || !set[alice.ID] || !set[bob.ID] {
		t.Errorf("expected owners {alice,bob}, got %v", owners)
	}
	if set[carol.ID] {
		t.Error("carol is a member, must not be reported as an owner")
	}
	if got, _ := s.RepoOwners("repo-none"); len(got) != 0 {
		t.Errorf("expected no owners for unknown repo, got %v", got)
	}
}
