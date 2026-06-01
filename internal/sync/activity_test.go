package sync

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListActivity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/repos/team-repo/activity" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("offset") != "50" {
			t.Errorf("expected limit=25 offset=50, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":7,"events":[
			{"user_id":"u2","email":"bob@example.com","action":"pull","created_at":1700100000,"detail":{"client":"claude-code"}},
			{"user_id":"u1","email":"alice@example.com","action":"push","created_at":1700000000,"detail":{"paths":["wiki/concepts/x.md"]}}
		]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	events, total, err := c.ListActivity("team-repo", 25, 50)
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Detail == nil || events[0].Detail.Client != "claude-code" {
		t.Errorf("event[0] detail wrong: %+v", events[0].Detail)
	}
	if events[1].Detail == nil || len(events[1].Detail.Paths) != 1 || events[1].Detail.Paths[0] != "wiki/concepts/x.md" {
		t.Errorf("event[1] detail wrong: %+v", events[1].Detail)
	}
}

func TestListActivityDefaultLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("limit") || r.URL.Query().Has("offset") {
			t.Errorf("expected no limit/offset params, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"events":[],"total":0}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	events, total, err := c.ListActivity("team-repo", 0, 0)
	if err != nil {
		t.Fatalf("ListActivity: %v", err)
	}
	if len(events) != 0 || total != 0 {
		t.Errorf("expected empty, got events=%d total=%d", len(events), total)
	}
}

func TestPullSendsClientHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Contexo-Client"); got != "ctx-cli" {
			t.Errorf("X-Contexo-Client = %q, want ctx-cli", got)
		}
		_, _ = w.Write([]byte(`{"new_head":"abc","files":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	c.SetClientName("ctx-cli")
	if _, err := c.PullPages("team-repo", ""); err != nil {
		t.Fatalf("PullPages: %v", err)
	}
}
