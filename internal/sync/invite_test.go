package sync

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMintInviteKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/repos/team-repo/invite-keys" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing/wrong bearer: %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"label":"for-contractor"`) {
			t.Errorf("body missing label: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"key":{"id":"k1","label":"for-contractor","created_at":1700000000,"expires_at":1700604800},"token":"ctxi_abc123"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	key, token, err := c.MintInviteKey("team-repo", "for-contractor")
	if err != nil {
		t.Fatalf("MintInviteKey: %v", err)
	}
	if token != "ctxi_abc123" {
		t.Errorf("token: %q", token)
	}
	if key.ID != "k1" || key.Label != "for-contractor" {
		t.Errorf("key wrong: %+v", key)
	}
	if key.ExpiresAt != 1700604800 {
		t.Errorf("expires_at: %d", key.ExpiresAt)
	}
}

func TestListInviteKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/repos/team-repo/invite-keys" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"keys":[
			{"id":"k1","label":"for-contractor","created_at":1,"expires_at":2},
			{"id":"k2","label":"team","created_at":3,"expires_at":4}
		]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	keys, err := c.ListInviteKeys("team-repo")
	if err != nil {
		t.Fatalf("ListInviteKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2", len(keys))
	}
	if keys[0].ID != "k1" || keys[1].Label != "team" {
		t.Errorf("keys wrong: %+v", keys)
	}
}

func TestDeleteInviteKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/v1/repos/team-repo/invite-keys/k1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	if err := c.DeleteInviteKey("team-repo", "k1"); err != nil {
		t.Fatalf("DeleteInviteKey: %v", err)
	}
}

func TestDeleteInviteKeyNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"invite key not found"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	err := c.DeleteInviteKey("team-repo", "k1")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 error, got %v", err)
	}
}
