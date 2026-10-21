package userstore

import (
	"errors"
	"slices"
	"testing"
)

// wp07Seed gives repo an owner (and optional extra members), n invite keys
// and m activity rows, and returns the raw keys.
func wp07Seed(t *testing.T, s *Store, repo, owner string, members []string, n, m int) []string {
	t.Helper()
	if err := s.AddMember(repo, owner, RoleOwner); err != nil {
		t.Fatalf("seed %s owner: %v", repo, err)
	}
	for _, u := range members {
		if err := s.AddMember(repo, u, RoleMember); err != nil {
			t.Fatalf("seed %s member: %v", repo, err)
		}
	}
	var keys []string
	for i := 0; i < n; i++ {
		_, raw, err := s.MintInviteKey(repo, owner, "k")
		if err != nil {
			t.Fatalf("seed %s key: %v", repo, err)
		}
		keys = append(keys, raw)
	}
	for i := 0; i < m; i++ {
		if err := s.RecordActivity(repo, owner, "push", ""); err != nil {
			t.Fatalf("seed %s activity: %v", repo, err)
		}
	}
	return keys
}

// wp07WantRepoRows asserts how many members, keys and activity rows repo
// has, and that every raw key in keys resolves to repo (or, when the repo
// should have no keys, is unknown).
func wp07WantRepoRows(t *testing.T, s *Store, label, repo string, members, nKeys, activity int, keys []string) {
	t.Helper()
	if ms, err := s.ListRepoMembers(repo); err != nil || len(ms) != members {
		t.Errorf("%s: %q members %+v (err %v), want %d", label, repo, ms, err, members)
	}
	if ks, err := s.ListInviteKeys(repo); err != nil || len(ks) != nKeys {
		t.Errorf("%s: %q keys %+v (err %v), want %d", label, repo, ks, err, nKeys)
	}
	if n, err := s.CountActivity(repo); err != nil || n != activity {
		t.Errorf("%s: %q activity %d (err %v), want %d", label, repo, n, err, activity)
	}
	for _, raw := range keys {
		got, err := s.ResolveInviteKey(raw)
		if nKeys == 0 {
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: a purged %q key resolves to %q (err %v), want ErrNotFound", label, repo, got, err)
			}
			continue
		}
		if err != nil || got != repo {
			t.Errorf("%s: a %q key resolves to %q (err %v)", label, repo, got, err)
		}
	}
}

// TestPurgeRepoRemovesMembersAndKeys: PurgeRepo drops every membership row,
// invite key and activity row of a repo id and of the alias ids passed with
// it, and nothing else.
func TestPurgeRepoRemovesMembersAndKeys(t *testing.T) {
	s := openTestStore(t)
	ids := wp07Users(t, s, "a@example.com", "b@example.com", "c@example.com")
	a, b, c := ids[0], ids[1], ids[2]
	xKeys := wp07Seed(t, s, "x", a, []string{b}, 2, 2)
	aliasKeys := wp07Seed(t, s, "x.y", c, nil, 1, 1)
	otherKeys := wp07Seed(t, s, "other", a, nil, 1, 1)

	if err := s.PurgeRepo("x", "x.y"); err != nil {
		t.Fatalf("purge: %v", err)
	}
	wp07WantRepoRows(t, s, "purged", "x", 0, 0, 0, xKeys)
	wp07WantRepoRows(t, s, "purged alias", "x.y", 0, 0, 0, aliasKeys)
	wp07WantRepoRows(t, s, "untouched", "other", 1, 1, 1, otherKeys)
	if n, err := s.CountUsers(); err != nil || n != 3 {
		t.Errorf("users %d (err %v), want 3", n, err)
	}

	if err := s.PurgeRepo("absent"); err != nil {
		t.Errorf("purge of an id without rows: %v", err)
	}
	if err := s.PurgeRepo(""); err == nil {
		t.Errorf("purge of an empty id: nil error")
	}
	if err := s.PurgeRepo("other", ""); err == nil {
		t.Errorf("purge with an empty alias: nil error")
	}
	wp07WantRepoRows(t, s, "after the refused purges", "other", 1, 1, 1, otherKeys)
}

// TestPurgeRepoIsAtomic: a purge that fails part way leaves every row in
// place (one transaction), so a failed create never leaves a half-purged id.
func TestPurgeRepoIsAtomic(t *testing.T) {
	s := openTestStore(t)
	ids := wp07Users(t, s, "a@example.com", "b@example.com")
	keys := wp07Seed(t, s, "x", ids[0], []string{ids[1]}, 1, 1)
	if _, err := s.DB().Exec(`CREATE TRIGGER wp07_block BEFORE DELETE ON repo_activity
BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}
	if err := s.PurgeRepo("x"); err == nil {
		t.Fatalf("purge with activity deletes blocked: nil error")
	}
	wp07WantRepoRows(t, s, "rolled back", "x", 2, 1, 1, keys)
}

// TestKnownRepoIDs: every repo id that has a membership row, an invite key
// or an activity row, once each, sorted.
func TestKnownRepoIDs(t *testing.T) {
	s := openTestStore(t)
	if got, err := s.KnownRepoIDs(); err != nil || len(got) != 0 {
		t.Fatalf("empty store: %v (err %v), want none", got, err)
	}
	ids := wp07Users(t, s, "a@example.com")
	a := ids[0]
	if err := s.AddMember("m-only", a, RoleOwner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.MintInviteKey("k-only", a, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordActivity("a-only", a, "push", ""); err != nil {
		t.Fatal(err)
	}
	wp07Seed(t, s, "all", a, nil, 2, 2)
	wp07Seed(t, s, "Z.upper", a, nil, 1, 0)

	got, err := s.KnownRepoIDs()
	want := []string{"Z.upper", "a-only", "all", "k-only", "m-only"}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("KnownRepoIDs = %q (err %v), want %q", got, err, want)
	}
}
