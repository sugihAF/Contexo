package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/sync"
)

func TestResolveMemberID(t *testing.T) {
	members := []sync.Member{
		{UserID: "u1", Email: "alice@example.com", Role: "owner"},
		{UserID: "u2", Email: "bob@example.com", Role: "member"},
	}
	id, err := resolveMemberID(members, "bob@example.com")
	if err != nil {
		t.Fatalf("resolveMemberID: %v", err)
	}
	if id != "u2" {
		t.Errorf("got %q, want u2", id)
	}
}

func TestResolveMemberIDCaseInsensitive(t *testing.T) {
	members := []sync.Member{{UserID: "u1", Email: "Alice@Example.com", Role: "owner"}}
	id, err := resolveMemberID(members, "alice@example.com")
	if err != nil {
		t.Fatalf("resolveMemberID: %v", err)
	}
	if id != "u1" {
		t.Errorf("got %q, want u1", id)
	}
}

func TestResolveMemberIDNotFound(t *testing.T) {
	members := []sync.Member{{UserID: "u1", Email: "alice@example.com", Role: "owner"}}
	_, err := resolveMemberID(members, "ghost@example.com")
	if err == nil || !strings.Contains(err.Error(), "ghost@example.com") {
		t.Errorf("expected not-found error mentioning email, got %v", err)
	}
}

func TestFriendlyRemoveErr(t *testing.T) {
	cases := []struct {
		in   error
		want string
	}{
		{sync.ErrNotOwner, "only an owner"},
		{sync.ErrMemberNotFound, "not a member"},
		{sync.ErrLastOwner, "last owner"},
	}
	for _, tc := range cases {
		got := friendlyRemoveErr(tc.in)
		if got == nil || !strings.Contains(got.Error(), tc.want) {
			t.Errorf("friendlyRemoveErr(%v) = %v, want contains %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderMembers(t *testing.T) {
	var buf bytes.Buffer
	renderMembers(&buf, []sync.Member{
		{UserID: "u1", Email: "alice@example.com", Role: "owner", AddedAt: 1700000000},
		{UserID: "u2", Email: "bob@example.com", Role: "member", AddedAt: 1700100000},
	})
	out := buf.String()
	if !strings.Contains(out, "alice@example.com") || !strings.Contains(out, "owner") {
		t.Errorf("missing owner row: %s", out)
	}
	if !strings.Contains(out, "bob@example.com") || !strings.Contains(out, "member") {
		t.Errorf("missing member row: %s", out)
	}
}

func TestRenderMembersEmpty(t *testing.T) {
	var buf bytes.Buffer
	renderMembers(&buf, nil)
	if !strings.Contains(buf.String(), "no members") {
		t.Errorf("expected 'no members', got %q", buf.String())
	}
}

func TestRunMembersRemove(t *testing.T) {
	var deleted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/repos/team-repo/members":
			_, _ = w.Write([]byte(`{"members":[
				{"user_id":"u1","email":"alice@example.com","role":"owner","added_at":1},
				{"user_id":"u2","email":"bob@example.com","role":"member","added_at":2}
			]}`))
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v1/repos/team-repo/members/"):
			deleted = strings.TrimPrefix(r.URL.Path, "/v1/repos/team-repo/members/")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := sync.NewClient(srv.URL, "tok")
	var buf bytes.Buffer
	if err := runMembersRemove(&buf, client, "team-repo", "bob@example.com"); err != nil {
		t.Fatalf("runMembersRemove: %v", err)
	}
	if deleted != "u2" {
		t.Errorf("deleted %q, want u2", deleted)
	}
	if !strings.Contains(buf.String(), "bob@example.com") {
		t.Errorf("expected confirmation mentioning email, got %q", buf.String())
	}
}
