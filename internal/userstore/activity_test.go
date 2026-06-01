package userstore

import (
	"strconv"
	"testing"
)

func TestRecordAndListActivity(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")
	bob, _, _ := s.UpsertGoogleUser("bob@example.com", "B", "2")

	if err := s.RecordActivity("repo-a", alice.ID, "push", ""); err != nil {
		t.Fatalf("record push: %v", err)
	}
	if err := s.RecordActivity("repo-a", bob.ID, "pull", ""); err != nil {
		t.Fatalf("record pull: %v", err)
	}

	events, err := s.ListActivity("repo-a", 50, 0)
	if err != nil {
		t.Fatalf("list activity: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	// Reverse-chron by insert order (id DESC): bob's pull is most recent.
	if events[0].UserID != bob.ID || events[0].Action != "pull" || events[0].Email != "bob@example.com" {
		t.Errorf("event[0] wrong: %+v", events[0])
	}
	if events[1].UserID != alice.ID || events[1].Action != "push" {
		t.Errorf("event[1] wrong: %+v", events[1])
	}
}

func TestListActivity_ScopedPerRepo(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")
	_ = s.RecordActivity("repo-a", alice.ID, "push", "")
	_ = s.RecordActivity("repo-b", alice.ID, "pull", "")

	a, _ := s.ListActivity("repo-a", 50, 0)
	if len(a) != 1 || a[0].Action != "push" {
		t.Errorf("repo-a activity wrong: %+v", a)
	}
	b, _ := s.ListActivity("repo-b", 50, 0)
	if len(b) != 1 || b[0].Action != "pull" {
		t.Errorf("repo-b activity wrong: %+v", b)
	}
}

func TestRecordActivity_PrunesToRetention(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")

	orig := activityRetentionPerRepo
	activityRetentionPerRepo = 3
	defer func() { activityRetentionPerRepo = orig }()

	for i := 0; i < 10; i++ {
		if err := s.RecordActivity("repo-a", alice.ID, "pull", ""); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	events, err := s.ListActivity("repo-a", 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("expected retention to cap at 3, got %d", len(events))
	}
}

func TestActivityDetailRoundTrip(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")
	detail := `{"paths":["wiki/concepts/architecture.md"]}`
	if err := s.RecordActivity("repo-a", alice.ID, "push", detail); err != nil {
		t.Fatalf("record: %v", err)
	}
	events, _ := s.ListActivity("repo-a", 50, 0)
	if len(events) != 1 || events[0].Detail != detail {
		t.Errorf("detail not round-tripped: %+v", events)
	}
}

func TestListActivityOffset(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")
	for i := 0; i < 5; i++ {
		_ = s.RecordActivity("repo-a", alice.ID, "pull", `{"n":`+strconv.Itoa(i)+`}`)
	}
	first, _ := s.ListActivity("repo-a", 2, 0)
	if len(first) != 2 || first[0].Detail != `{"n":4}` || first[1].Detail != `{"n":3}` {
		t.Fatalf("first page wrong: %+v", first)
	}
	next, _ := s.ListActivity("repo-a", 2, 2)
	if len(next) != 2 || next[0].Detail != `{"n":2}` || next[1].Detail != `{"n":1}` {
		t.Fatalf("offset page wrong: %+v", next)
	}
}

func TestCountActivity(t *testing.T) {
	s := openTestStore(t)
	alice, _, _ := s.UpsertGoogleUser("alice@example.com", "A", "1")
	for i := 0; i < 4; i++ {
		_ = s.RecordActivity("repo-a", alice.ID, "pull", "")
	}
	n, err := s.CountActivity("repo-a")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 4 {
		t.Errorf("expected 4, got %d", n)
	}
}
