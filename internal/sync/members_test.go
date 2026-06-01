package sync

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListMembers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/repos/team-repo/members" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing/wrong bearer: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"members":[
			{"user_id":"u1","email":"alice@example.com","role":"owner","added_at":1700000000},
			{"user_id":"u2","email":"bob@example.com","role":"member","added_at":1700100000}
		]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	members, err := c.ListMembers("team-repo")
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}
	if members[0].UserID != "u1" || members[0].Email != "alice@example.com" || members[0].Role != "owner" {
		t.Errorf("member[0] wrong: %+v", members[0])
	}
	if members[1].Role != "member" || members[1].AddedAt != 1700100000 {
		t.Errorf("member[1] wrong: %+v", members[1])
	}
}

func TestRemoveMember(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/v1/repos/team-repo/members/u2" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing/wrong bearer: %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	if err := c.RemoveMember("team-repo", "u2"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
}

func TestRemoveMemberNotOwner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"owner role required"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	err := c.RemoveMember("team-repo", "u2")
	if !errors.Is(err, ErrNotOwner) {
		t.Errorf("expected ErrNotOwner, got %v", err)
	}
}

func TestRemoveMemberNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not a member of this repo"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	err := c.RemoveMember("team-repo", "u2")
	if !errors.Is(err, ErrMemberNotFound) {
		t.Errorf("expected ErrMemberNotFound, got %v", err)
	}
}

func TestRemoveMemberLastOwner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"cannot remove the last owner"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	err := c.RemoveMember("team-repo", "u2")
	if !errors.Is(err, ErrLastOwner) {
		t.Errorf("expected ErrLastOwner, got %v", err)
	}
}
