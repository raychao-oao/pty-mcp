package session

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Session 代表一個互動式 terminal session
type Session interface {
	ID() string
	Type() string // "ssh" | "serial"
	Write(input string) error    // 送出指令（自動加換行）
	WriteRaw(data string) error  // 送出原始資料（不加換行，用於控制鍵）
	ReadScreen(timeoutMs int) (output string, isComplete bool)
	IsAlive() bool
	Close() error
}

// Info 是 session 的 metadata（給 list 用）
type Info struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Target    string    `json:"target"`
	IsAlive   bool      `json:"is_alive"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
}

// Manager 管理所有 session 的生命週期
type Manager struct {
	mu          sync.RWMutex
	sessions    map[string]Session
	infos       map[string]Info
	idleTimeout time.Duration
}

// NewManager 建立 SessionManager，idleSeconds 是 idle timeout 秒數（0 表示不 timeout）
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
		// 先收集要關的 session，釋放鎖後再 Close（避免 deadlock）
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

// Detach 斷開 remote session 但不關閉遠端 PTY
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
	// 非 remote session 無法 detach，直接 close
	return s.Close()
}

func NewID() string {
	return uuid.New().String()[:8]
}
