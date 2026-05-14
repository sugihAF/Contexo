package recorder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/sugihAF/contexo/internal/schema"
)

// HTTPServer provides an HTTP endpoint for receiving events.
type HTTPServer struct {
	recorder *Recorder
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
	running  bool
	port     int
}

// NewHTTPServer creates an HTTP server that routes POST /event to the recorder.
func NewHTTPServer(rec *Recorder, port int) *HTTPServer {
	s := &HTTPServer{
		recorder: rec,
		port:     port,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/event", s.handleEvent)
	mux.HandleFunc("/health", s.handleHealth)

	s.server = &http.Server{
		Handler: mux,
	}

	return s
}

// Start begins listening on the configured port.
func (s *HTTPServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("server already running")
	}

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("recorder http: listen %s: %w", addr, err)
	}
	s.listener = ln
	s.running = true

	go s.server.Serve(ln)
	return nil
}

// Stop shuts down the HTTP server.
func (s *HTTPServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.running = false
	return s.server.Shutdown(context.Background())
}

// Addr returns the listener address.
func (s *HTTPServer) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

// Port returns the configured port.
func (s *HTTPServer) Port() int {
	return s.port
}

func (s *HTTPServer) handleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var event schema.SessionEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if err := event.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("validation: %v", err), http.StatusBadRequest)
		return
	}

	if err := s.recorder.IngestEvent(r.Context(), &event); err != nil {
		http.Error(w, fmt.Sprintf("ingest: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy"}`))
}
