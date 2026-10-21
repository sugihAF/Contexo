package userstore

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
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
// place, those of repoID and of every alias alike (one transaction for all
// the ids), so a failed create never leaves a half-purged id. The deletes of
// one table are blocked for one id at a time, for each table and for the
// repo id and its alias in turn, so a purge that commits per id, per table
// or not at all fails whatever order it deletes in.
func TestPurgeRepoIsAtomic(t *testing.T) {
	for _, blocked := range []string{"x", "x.y"} {
		for _, table := range []string{"repo_members", "repo_invite_keys", "repo_activity"} {
			t.Run(blocked+"/"+table, func(t *testing.T) {
				s := openTestStore(t)
				ids := wp07Users(t, s, "a@example.com", "b@example.com", "c@example.com")
				xKeys := wp07Seed(t, s, "x", ids[0], []string{ids[1]}, 1, 1)
				aliasKeys := wp07Seed(t, s, "x.y", ids[2], []string{ids[0]}, 1, 1)
				trigger := fmt.Sprintf(`CREATE TRIGGER wp07_block BEFORE DELETE ON %s WHEN OLD.repo_id = '%s'
BEGIN SELECT RAISE(ABORT, 'blocked'); END`, table, blocked)
				if _, err := s.DB().Exec(trigger); err != nil {
					t.Fatalf("install trigger: %v", err)
				}
				if err := s.PurgeRepo("x", "x.y"); err == nil {
					t.Fatalf("purge with the %s deletes of %q blocked: nil error", table, blocked)
				}
				wp07WantRepoRows(t, s, "rolled back", "x", 2, 1, 1, xKeys)
				wp07WantRepoRows(t, s, "rolled back alias", "x.y", 2, 1, 1, aliasKeys)
			})
		}
	}
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
	// Several rows per id and several ids per table, so each table's ids
	// are walked one after another.
	for _, repo := range []string{"a-only", "a-only", "a-only.2", "b", "a-only.2"} {
		if err := s.RecordActivity(repo, a, "push", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.MintInviteKey("m-only", a, ""); err != nil {
		t.Fatal(err)
	}

	got, err := s.KnownRepoIDs()
	want := []string{"Z.upper", "a-only", "a-only.2", "all", "b", "k-only", "m-only"}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("KnownRepoIDs = %q (err %v), want %q", got, err, want)
	}
}

// TestKnownRepoIDsSeeksInsteadOfScanning: every POST /v1/repos of a new id
// lists the known repo ids, and repo_activity keeps up to
// activityRetentionPerRepo rows per repo, so a listing that reads every row
// costs each create a scan of the whole activity log (about 150 ms at 2,000
// repos). It must reach each distinct id through the table's repo_id index,
// one seek per id, and never scan a table or a whole index.
func TestKnownRepoIDsSeeksInsteadOfScanning(t *testing.T) {
	s := openTestStore(t)
	rows, err := s.DB().Query(`EXPLAIN QUERY PLAN ` + knownRepoIDsQuery)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("explain columns: %v", err)
	}
	scan := regexp.MustCompile(`^SCAN (repo_members|repo_invite_keys|repo_activity)\b`)
	seek := regexp.MustCompile(`^SEARCH (repo_members|repo_invite_keys|repo_activity) `)
	var plan []string
	seeks := map[string]bool{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("explain row: %v", err)
		}
		detail := fmt.Sprint(vals[len(vals)-1])
		plan = append(plan, detail)
		if scan.MatchString(detail) {
			t.Errorf("the listing scans a whole table: %q", detail)
		}
		if m := seek.FindStringSubmatch(detail); m != nil {
			seeks[m[1]] = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows: %v", err)
	}
	if len(seeks) != 3 {
		t.Errorf("index seeks on %v, want all three tables", seeks)
	}
	if t.Failed() {
		t.Logf("plan:\n%s", strings.Join(plan, "\n"))
	}
}
