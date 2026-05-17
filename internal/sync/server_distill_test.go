package sync

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
