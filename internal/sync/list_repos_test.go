package sync

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListReposHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/repos" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing/wrong bearer: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"repos":[
			{"id":"shoplens","role":"owner","page_count":12},
			{"id":"chompchat-prod","role":"member"},
			{"id":"_test","role":"owner"}
		]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	repos, err := c.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 3 {
		t.Fatalf("got %d repos, want 3", len(repos))
	}
	if repos[0].ID != "shoplens" || repos[0].Role != "owner" {
		t.Errorf("first repo wrong: %+v", repos[0])
	}
	if repos[1].Role != "member" {
		t.Errorf("second repo role: %q", repos[1].Role)
	}
}

func TestListReposEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"repos":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	repos, err := c.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("expected empty list, got %d", len(repos))
	}
}

func TestListReposUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	_, err := c.ListRepos()
	if err == nil {
		t.Fatalf("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status: %v", err)
	}
}

func TestListReposServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`boom`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	_, err := c.ListRepos()
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500-class error, got %v", err)
	}
}
