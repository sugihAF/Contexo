package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/sync"
)

func TestRepoOptionLabelPadsAndDefaultsRole(t *testing.T) {
	got := repoOptionLabel(sync.RepoOption{ID: "abc", Role: ""}, 8)
	if got != "abc       (member)" {
		t.Errorf("default role + padding: got %q", got)
	}
	got = repoOptionLabel(sync.RepoOption{ID: "shoplens", Role: "owner"}, 8)
	if got != "shoplens  (owner)" {
		t.Errorf("owner label: got %q", got)
	}
}

func TestFetchRepoOptionsFormatsAndAligns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"repos":[
			{"id":"shoplens","role":"owner"},
			{"id":"chompchat-prod","role":"member"},
			{"id":"a","role":"owner"}
		]}`))
	}))
	defer srv.Close()

	client := sync.NewClient(srv.URL, "tok")
	opts, labels, err := fetchRepoOptions(client)
	if err != nil {
		t.Fatalf("fetchRepoOptions: %v", err)
	}
	if len(opts) != 3 {
		t.Fatalf("want 3 options, got %d", len(opts))
	}
	// idWidth = len("chompchat-prod") = 14. All labels should start at col 14.
	for i, l := range labels {
		idPart := strings.Split(l, "  (")[0]
		if len(idPart) != 14 {
			t.Errorf("label %d %q: id column width = %d, want 14", i, l, len(idPart))
		}
	}
}

func TestFetchRepoOptionsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"repos":[]}`))
	}))
	defer srv.Close()

	client := sync.NewClient(srv.URL, "tok")
	opts, labels, err := fetchRepoOptions(client)
	if err != nil {
		t.Fatalf("fetchRepoOptions: %v", err)
	}
	if len(opts) != 0 || len(labels) != 0 {
		t.Errorf("expected empty slices, got %d opts / %d labels", len(opts), len(labels))
	}
}

func TestFetchRepoOptionsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := sync.NewClient(srv.URL, "tok")
	_, _, err := fetchRepoOptions(client)
	if err == nil {
		t.Errorf("expected error on 500")
	}
}
