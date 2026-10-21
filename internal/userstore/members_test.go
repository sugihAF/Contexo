package userstore

import "testing"

// wp07Users creates one real user per email (repo_members has a foreign key
// on users) and returns their ids in order.
func wp07Users(t *testing.T, s *Store, emails ...string) []string {
	t.Helper()
	ids := make([]string, 0, len(emails))
	for _, e := range emails {
		u, _, err := s.UpsertGoogleUser(e, e, "sub-"+e)
		if err != nil {
			t.Fatalf("user %s: %v", e, err)
		}
		ids = append(ids, u.ID)
	}
	return ids
}

// TestAddMemberReportsInserted: AddMemberIfAbsent is AddMember's INSERT OR
// IGNORE that also reports whether it added the row, so a creator can tell
// "this request made me owner" from "a row was already there".
func TestAddMemberReportsInserted(t *testing.T) {
	s := openTestStore(t)
	ids := wp07Users(t, s, "u1@example.com", "u2@example.com", "u3@example.com")
	u1, u2, u3 := ids[0], ids[1], ids[2]

	wantRole := func(label, user, want string) {
		t.Helper()
		if role, err := s.GetRole("r", user); err != nil || role != want {
			t.Errorf("%s: role %q (err %v), want %q", label, role, err, want)
		}
	}

	if inserted, err := s.AddMemberIfAbsent("r", u1, RoleOwner); err != nil || !inserted {
		t.Errorf("first add: inserted=%v err=%v, want true nil", inserted, err)
	}
	if inserted, err := s.AddMemberIfAbsent("r", u1, RoleMember); err != nil || inserted {
		t.Errorf("second add: inserted=%v err=%v, want false nil", inserted, err)
	}
	wantRole("existing row kept", u1, RoleOwner)

	if inserted, err := s.AddMemberIfAbsent("r", u2, "bogus"); err != nil || !inserted {
		t.Errorf("bogus role: inserted=%v err=%v, want true nil", inserted, err)
	}
	wantRole("bogus role normalised", u2, RoleMember)

	// AddMember keeps its idempotent contract.
	for i := 0; i < 2; i++ {
		if err := s.AddMember("r", u3, RoleMember); err != nil {
			t.Errorf("AddMember #%d: %v", i+1, err)
		}
	}
	wantRole("AddMember", u3, RoleMember)

	if inserted, err := s.AddMemberIfAbsent("r", "no-such-user", RoleOwner); err == nil || inserted {
		t.Errorf("unknown user: inserted=%v err=%v, want false and an error", inserted, err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if inserted, err := s.AddMemberIfAbsent("r", u1, RoleOwner); err == nil || inserted {
		t.Errorf("closed store: inserted=%v err=%v, want false and an error", inserted, err)
	}
}
