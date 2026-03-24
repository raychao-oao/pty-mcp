package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
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

func (m *Manager) Add(s Session, target string) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
}

func (m *Manager) Get(id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	if info, ok := m.infos[id]; ok {
		info.LastUsed = time.Now()
		m.infos[id] = info
	}
	return s, nil
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
		return fmt.Errorf("session %q not found", id)
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
		return fmt.Errorf("session %q not found", id)
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
	return uuid.New().String()[:8]
}
