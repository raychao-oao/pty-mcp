package mcp

import (
	"encoding/json"
	"fmt"
	"pty-mcp/internal/session"
)

type Handler struct {
	mgr *session.Manager
}

func NewHandler(mgr *session.Manager) *Handler {
	return &Handler{mgr: mgr}
}

type CreateSSHParams struct {
	Host       string `json:"host"`
	Port       string `json:"port"`
	User       string `json:"user"`
	Password   string `json:"password"`
	KeyPath    string `json:"key_path"`
	IgnoreHost bool   `json:"ignore_host_key"`
	Persistent bool   `json:"persistent"` // 使用 ai-tmux persistent session
	Command    string `json:"command"`    // persistent 模式的初始指令
}

func (h *Handler) CreateSSHSession(params json.RawMessage) (any, error) {
	var p CreateSSHParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	cfg := session.SSHConfig{
		Host:       p.Host,
		Port:       p.Port,
		User:       p.User,
		Password:   p.Password,
		KeyPath:    p.KeyPath,
		IgnoreHost: p.IgnoreHost,
	}
	var s session.Session
	var err error
	var sessionType string

	if p.Persistent {
		s, err = session.NewRemoteSSHSession(cfg, p.Command)
		sessionType = "remote"
	} else {
		s, err = session.NewSSHSession(cfg)
		sessionType = "ssh"
	}

	if err != nil {
		return nil, err
	}
	target := fmt.Sprintf("%s@%s", p.User, p.Host)
	h.mgr.Add(s, target)
	return map[string]string{"session_id": s.ID(), "type": sessionType, "target": target}, nil
}

type CreateSerialParams struct {
	Device   string `json:"device"`
	BaudRate int    `json:"baud_rate"`
}

func (h *Handler) CreateSerialSession(params json.RawMessage) (any, error) {
	var p CreateSerialParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	s, err := session.NewSerialSession(p.Device, p.BaudRate)
	if err != nil {
		return nil, err
	}
	target := fmt.Sprintf("%s@%d", p.Device, p.BaudRate)
	h.mgr.Add(s, target)
	return map[string]string{"session_id": s.ID(), "type": "serial", "target": target}, nil
}

type SendInputParams struct {
	SessionID string `json:"session_id"`
	Input     string `json:"input"`
	TimeoutMs int    `json:"timeout_ms"`
}

func (h *Handler) SendInput(params json.RawMessage) (any, error) {
	var p SendInputParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.TimeoutMs <= 0 {
		p.TimeoutMs = 5000
	}
	if p.TimeoutMs > 30000 {
		p.TimeoutMs = 30000
	}
	s, err := h.mgr.Get(p.SessionID)
	if err != nil {
		return nil, err
	}
	if err := s.Write(p.Input); err != nil {
		return nil, err
	}
	output, isComplete := s.ReadScreen(p.TimeoutMs)
	return map[string]any{"output": output, "is_alive": s.IsAlive(), "is_complete": isComplete}, nil
}

type SessionIDParams struct {
	SessionID string `json:"session_id"`
}

func (h *Handler) ReadOutput(params json.RawMessage) (any, error) {
	var p SessionIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	s, err := h.mgr.Get(p.SessionID)
	if err != nil {
		return nil, err
	}
	output, isComplete := s.ReadScreen(5000)
	return map[string]any{"output": output, "is_alive": s.IsAlive(), "is_complete": isComplete}, nil
}

type SendControlParams struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key"`
}

var controlKeys = map[string]string{
	"ctrl+c": "\x03",
	"ctrl+d": "\x04",
	"ctrl+z": "\x1a",
	"ctrl+l": "\x0c",
	"ctrl+r": "\x12",
	"enter":  "\r",
	"tab":    "\t",
	"escape": "\x1b",
	"up":     "\x1b[A",
	"down":   "\x1b[B",
	"left":   "\x1b[D",
	"right":  "\x1b[C",
}

func (h *Handler) SendControl(params json.RawMessage) (any, error) {
	var p SendControlParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	seq, ok := controlKeys[p.Key]
	if !ok {
		return nil, fmt.Errorf("unknown control key %q, supported: %v", p.Key, supportedKeys())
	}
	s, err := h.mgr.Get(p.SessionID)
	if err != nil {
		return nil, err
	}
	if err := s.WriteRaw(seq); err != nil {
		return nil, err
	}
	output, isComplete := s.ReadScreen(5000)
	return map[string]any{"output": output, "is_alive": s.IsAlive(), "is_complete": isComplete}, nil
}

func supportedKeys() []string {
	keys := make([]string, 0, len(controlKeys))
	for k := range controlKeys {
		keys = append(keys, k)
	}
	return keys
}

func (h *Handler) ListSessions(_ json.RawMessage) (any, error) {
	return h.mgr.List(), nil
}

func (h *Handler) CloseSession(params json.RawMessage) (any, error) {
	var p SessionIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if err := h.mgr.Close(p.SessionID); err != nil {
		return nil, err
	}
	return map[string]bool{"success": true}, nil
}
