package sync

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sugihAF/contexo/internal/syncops/contract"
)

func TestServerDistillReturnsNotImplemented(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"server-side distillation not implemented"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	_, err := c.ServerDistill("repo", &DistillRequest{SessionID: "sess"})
	if err == nil {
		t.Fatalf("expected error for 501 stub")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error should mention not implemented: %v", err)
	}
}

func TestServerDistillSurfacesUnexpectedErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	_, err := c.ServerDistill("repo", &DistillRequest{SessionID: "sess"})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500-class error, got %v", err)
	}
}

// ServerDistill goes through do() (E15): its 501 is a typed server_error with
// the legacy text, and the success path still decodes the response.
func TestServerDistillTypedErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/repos/repo/sync/distill" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":"server-side distillation not implemented"}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "token").ServerDistill("repo", &DistillRequest{SessionID: "sess"})
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("%T is not an *HTTPError", err)
	}
	if err.Error() != "server-side distillation not implemented (planned for Phase 4)" {
		t.Errorf("Error() = %q", err.Error())
	}
	if he.Code != contract.CodeServerError || he.HTTPStatus() != 501 {
		t.Errorf("Code = %q Status = %d", he.Code, he.HTTPStatus())
	}

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"source_path":"wiki/sources/s.md","body":"b"}`))
	}))
	defer ok.Close()
	dr, err := NewClient(ok.URL, "token").ServerDistill("repo", &DistillRequest{SessionID: "sess"})
	if err != nil || dr.SourcePath != "wiki/sources/s.md" || dr.Body != "b" {
		t.Errorf("ServerDistill 200 = %+v, %v", dr, err)
	}
}
