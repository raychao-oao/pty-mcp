package audit

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

// Server is the audit log collector HTTP server.
type Server struct {
	token string
	mu    sync.Mutex
	file  *os.File
}

// NewServer opens the log file and returns a ready Server.
func NewServer(token, logPath string) (*Server, error) {
	if token == "" {
		return nil, fmt.Errorf("PTY_MCP_AUDIT_TOKEN must not be empty")
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("open log %q: %w", logPath, err)
	}
	return &Server{token: token, file: f}, nil
}

// Serve starts the HTTP server on the given address (e.g. ":8080").
func (s *Server) Serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("[audit] listening on %s, writing to %s", ln.Addr(), s.file.Name())
	return http.Serve(ln, s) //nolint:gosec
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/audit" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if !s.checkToken(r.Header.Get("Authorization")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !json.Valid(body) {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.file.Write(append(body, '\n')) //nolint:errcheck
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// Close closes the log file.
func (s *Server) Close() error {
	return s.file.Close()
}

func (s *Server) checkToken(auth string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	tok := []byte(auth[len(prefix):])
	expected := []byte(s.token)
	if len(tok) == 0 || len(expected) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare(tok, expected) == 1
}
