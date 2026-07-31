package cli

import (
	"bytes"
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

func TestFetchRepoOptionsReturnsStructsAndLabels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"repos":[
			{"id":"shoplens","role":"owner"},
			{"id":"chompchat-prod","role":"member"},
			{"id":"a","role":""}
		]}`))
	}))
	defer srv.Close()

	client := sync.NewClient(srv.URL, "tok")
	opts, labels, err := fetchRepoOptions(client)
	if err != nil {
		t.Fatalf("fetchRepoOptions: %v", err)
	}
	if len(opts) != 3 || len(labels) != 3 {
		t.Fatalf("got %d opts / %d labels, want 3 each", len(opts), len(labels))
	}
	// Assert on the structs, not the display strings: the picker selects by
	// index now, so label formatting is cosmetic and must not be pinned.
	if opts[0].ID != "shoplens" || opts[0].Role != "owner" {
		t.Errorf("opts[0] = %+v", opts[0])
	}
	// Role defaulting is the behaviour that matters.
	if !strings.Contains(labels[2], "member") {
		t.Errorf("labels[2] = %q, want a defaulted role", labels[2])
	}
}

// Selection must be by index, so restyling a label can never change which
// repo gets chosen.
func TestSelectRepoReturnsIDByIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"repos":[{"id":"one","role":"owner"},{"id":"two","role":"member"}]}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	got, err := selectRepoInteractive(sync.NewClient(srv.URL, "tok"),
		strings.NewReader("2\n"), &out)
	if err != nil {
		t.Fatalf("selectRepoInteractive: %v", err)
	}
	if got != "two" {
		t.Errorf("selected %q, want \"two\"", got)
	}
}

func TestSelectRepoEmptyGivesOnboardingHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"repos":[]}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	if _, err := selectRepoInteractive(sync.NewClient(srv.URL, "tok"),
		strings.NewReader(""), &out); err == nil {
		t.Error("expected an error when the user has no repos")
	}
	if !strings.Contains(out.String(), "ctx join") {
		t.Errorf("missing onboarding hint: %q", out.String())
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
