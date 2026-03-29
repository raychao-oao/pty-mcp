package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/raychao-oao/pty-mcp/internal/buffer"
)

// Session represents an interactive terminal session
type Session interface {
	ID() string
	Type() string // "ssh" | "serial" | "local" | "remote"
	Write(input string) error    // send a command (newline appended automatically)
	WriteRaw(data string) error  // send raw data (no newline, used for control keys)
	ReadScreen(timeoutMs int) (output string, isComplete bool)
	IsAlive() bool
	Close() error
	Buffer() *buffer.RingBuffer
	PollRemote(ctx context.Context)
}

// Info holds session metadata (used by list)
type Info struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Target    string    `json:"target"`
	IsAlive   bool      `json:"is_alive"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
}

const maxSessions = 50

// Manager manages the lifecycle of all sessions
type Manager struct {
	mu          sync.RWMutex
	sessions    map[string]Session
	infos       map[string]Info
	idleTimeout time.Duration
}

// NewManager creates a SessionManager; idleSeconds is the idle timeout in seconds (0 means no timeout)
func NewManager(idleSeconds int) *Manager {
	m := &Manager{
		sessions:    make(map[string]Session),
		infos:       make(map[string]Info),
		idleTimeout: time.Duration(idleSeconds) * time.Second,
	}
	if idleSeconds > 0 {
		go m.idleReaper()
	}
	return m
}

func (m *Manager) idleReaper() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		// collect sessions to close first, then release lock before calling Close (avoid deadlock)
		var toClose []Session
		m.mu.Lock()
		now := time.Now()
		for id, info := range m.infos {
			if now.Sub(info.LastUsed) > m.idleTimeout {
				if s, ok := m.sessions[id]; ok {
					toClose = append(toClose, s)
					delete(m.sessions, id)
					delete(m.infos, id)
				}
			}
		}
		m.mu.Unlock()

		for _, s := range toClose {
			s.Close()
		}
	}
}

// SessionNotFoundError is returned when a session ID does not exist.
type SessionNotFoundError struct {
	ID string
}

func (e *SessionNotFoundError) Error() string {
	return fmt.Sprintf("session %q not found", e.ID)
}

// SessionLimitError is returned when the session limit is reached.
type SessionLimitError struct{}

func (e *SessionLimitError) Error() string {
	return fmt.Sprintf("session limit reached (%d max); close existing sessions first", maxSessions)
}

func (m *Manager) Add(s Session, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= maxSessions {
		s.Close()
		return &SessionLimitError{}
	}
	now := time.Now()
	m.sessions[s.ID()] = s
	m.infos[s.ID()] = Info{
		ID:        s.ID(),
		Type:      s.Type(),
		Target:    target,
		IsAlive:   true,
		CreatedAt: now,
		LastUsed:  now,
	}
	return nil
}

func (m *Manager) Get(id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, &SessionNotFoundError{ID: id}
	}
	if info, ok := m.infos[id]; ok {
		info.LastUsed = time.Now()
		m.infos[id] = info
	}
	return s, nil
}

func (m *Manager) GetInfo(id string) Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if info, ok := m.infos[id]; ok {
		if s, ok := m.sessions[id]; ok {
			info.IsAlive = s.IsAlive()
		}
		return info
	}
	return Info{}
}

func (m *Manager) List() []Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Info, 0, len(m.infos))
	for id, info := range m.infos {
		if s, ok := m.sessions[id]; ok {
			info.IsAlive = s.IsAlive()
		}
		result = append(result, info)
	}
	return result
}

func (m *Manager) Close(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return &SessionNotFoundError{ID: id}
	}
	delete(m.sessions, id)
	delete(m.infos, id)
	m.mu.Unlock()

	return s.Close()
}

// Detach disconnects the remote session without closing the remote PTY
func (m *Manager) Detach(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return &SessionNotFoundError{ID: id}
	}
	delete(m.sessions, id)
	delete(m.infos, id)
	m.mu.Unlock()

	if rs, ok := s.(*RemoteSession); ok {
		return rs.Detach()
	}
	// non-remote sessions cannot be detached, close directly
	return s.Close()
}

func NewID() string {
	var b [8]byte
	rand.Read(b[:]) //nolint:errcheck
	return hex.EncodeToString(b[:]) // 16 hex chars = 64 bits of entropy
}
