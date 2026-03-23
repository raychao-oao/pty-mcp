// internal/aitx/server.go
package aitx

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type Server struct {
	mu          sync.RWMutex
	sessions    map[string]*PTYSession
	idleTimeout time.Duration
}

func RunServer(socketPath string, idleSeconds int) error {
	os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", socketPath, err)
	}
	defer ln.Close()
	defer os.Remove(socketPath)

	// restrict socket permissions to owner only
	os.Chmod(socketPath, 0700)

	srv := &Server{
		sessions:    make(map[string]*PTYSession),
		idleTimeout: time.Duration(idleSeconds) * time.Second,
	}

	if idleSeconds > 0 {
		go srv.idleReaper()
	}

	log.Printf("[ai-tmux] server listening on %s (idle timeout: %ds)", socketPath, idleSeconds)

	// catch signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("[ai-tmux] shutting down...")
		srv.closeAll()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			return nil // listener closed
		}
		go srv.handleConn(conn)
	}
}

func (srv *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	encoder := json.NewEncoder(conn)

	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			log.Printf("[ai-tmux] parse error: %v", err)
			continue
		}
		resp := srv.handle(&req)
		encoder.Encode(resp)
	}
}

func (srv *Server) handle(req *Request) Response {
	switch req.Method {
	case "create_session":
		return srv.createSession(req)
	case "send_input":
		return srv.sendInput(req)
	case "read_output":
		return srv.readOutput(req)
	case "send_control":
		return srv.sendControl(req)
	case "list_sessions":
		return srv.listSessions(req)
	case "close_session":
		return srv.closeSession(req)
	default:
		return Response{ID: req.ID, Error: fmt.Sprintf("unknown method: %s", req.Method)}
	}
}

func (srv *Server) createSession(req *Request) Response {
	var p CreateSessionParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return Response{ID: req.ID, Error: err.Error()}
	}

	var idBytes [4]byte
	rand.Read(idBytes[:])
	id := hex.EncodeToString(idBytes[:])
	s, err := NewPTYSession(id, p.Name, p.Command)
	if err != nil {
		return Response{ID: req.ID, Error: err.Error()}
	}

	srv.mu.Lock()
	srv.sessions[id] = s
	srv.mu.Unlock()

	return Response{ID: req.ID, Result: SessionResult{
		SessionID: id,
		Type:      "pty",
		Name:      s.Name(),
	}}
}

func (srv *Server) sendInput(req *Request) Response {
	var p SendInputParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return Response{ID: req.ID, Error: err.Error()}
	}

	s, err := srv.getSession(p.SessionID)
	if err != nil {
		return Response{ID: req.ID, Error: err.Error()}
	}

	if err := s.Write(p.Input); err != nil {
		return Response{ID: req.ID, Error: err.Error()}
	}

	output, isComplete := s.ReadScreen(p.TimeoutMs)
	return Response{ID: req.ID, Result: OutputResult{
		Output:     output,
		IsAlive:    s.IsAlive(),
		IsComplete: isComplete,
	}}
}

func (srv *Server) readOutput(req *Request) Response {
	var p ReadOutputParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return Response{ID: req.ID, Error: err.Error()}
	}

	s, err := srv.getSession(p.SessionID)
	if err != nil {
		return Response{ID: req.ID, Error: err.Error()}
	}

	timeoutMs := p.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	output, isComplete := s.ReadScreen(timeoutMs)
	return Response{ID: req.ID, Result: OutputResult{
		Output:     output,
		IsAlive:    s.IsAlive(),
		IsComplete: isComplete,
	}}
}

func (srv *Server) sendControl(req *Request) Response {
	var p SendControlParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return Response{ID: req.ID, Error: err.Error()}
	}

	controlKeys := map[string]string{
		"ctrl+c": "\x03", "ctrl+d": "\x04", "ctrl+z": "\x1a",
		"ctrl+l": "\x0c", "ctrl+r": "\x12", "enter": "\r",
		"tab": "\t", "escape": "\x1b",
		"up": "\x1b[A", "down": "\x1b[B", "left": "\x1b[D", "right": "\x1b[C",
	}

	seq, ok := controlKeys[p.Key]
	if !ok {
		return Response{ID: req.ID, Error: fmt.Sprintf("unknown control key: %s", p.Key)}
	}

	s, err := srv.getSession(p.SessionID)
	if err != nil {
		return Response{ID: req.ID, Error: err.Error()}
	}

	if err := s.WriteRaw(seq); err != nil {
		return Response{ID: req.ID, Error: err.Error()}
	}

	output, isComplete := s.ReadScreen(5000)
	return Response{ID: req.ID, Result: OutputResult{
		Output:     output,
		IsAlive:    s.IsAlive(),
		IsComplete: isComplete,
	}}
}

func (srv *Server) listSessions(req *Request) Response {
	srv.mu.RLock()
	defer srv.mu.RUnlock()

	list := make([]SessionInfo, 0, len(srv.sessions))
	for _, s := range srv.sessions {
		list = append(list, SessionInfo{
			ID:        s.ID(),
			Name:      s.Name(),
			Command:   s.Command(),
			IsAlive:   s.IsAlive(),
			CreatedAt: s.CreatedAt().Format(time.RFC3339),
			LastUsed:  s.LastUsed().Format(time.RFC3339),
		})
	}
	return Response{ID: req.ID, Result: list}
}

func (srv *Server) closeSession(req *Request) Response {
	var p SessionIDParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return Response{ID: req.ID, Error: err.Error()}
	}

	srv.mu.Lock()
	s, ok := srv.sessions[p.SessionID]
	if !ok {
		srv.mu.Unlock()
		return Response{ID: req.ID, Error: fmt.Sprintf("session %q not found", p.SessionID)}
	}
	delete(srv.sessions, p.SessionID)
	srv.mu.Unlock()

	s.Close()
	return Response{ID: req.ID, Result: map[string]bool{"success": true}}
}

func (srv *Server) getSession(id string) (*PTYSession, error) {
	srv.mu.RLock()
	defer srv.mu.RUnlock()
	s, ok := srv.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return s, nil
}

func (srv *Server) idleReaper() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		var toClose []*PTYSession
		srv.mu.Lock()
		now := time.Now()
		for id, s := range srv.sessions {
			if now.Sub(s.LastUsed()) > srv.idleTimeout {
				toClose = append(toClose, s)
				delete(srv.sessions, id)
			}
		}
		srv.mu.Unlock()
		for _, s := range toClose {
			log.Printf("[ai-tmux] idle reaping session %s", s.ID())
			s.Close()
		}
	}
}

func (srv *Server) closeAll() {
	srv.mu.Lock()
	all := make([]*PTYSession, 0, len(srv.sessions))
	for _, s := range srv.sessions {
		all = append(all, s)
	}
	srv.sessions = make(map[string]*PTYSession)
	srv.mu.Unlock()
	for _, s := range all {
		s.Close()
	}
}
