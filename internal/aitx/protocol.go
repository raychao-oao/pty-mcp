// internal/aitx/protocol.go
package aitx

import (
	"encoding/json"
)

const SocketPath = "/tmp/ai-tmux.sock"

// Request 從 client 到 server
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Response 從 server 到 client
type Response struct {
	ID     string `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// --- Params 結構 ---

type CreateSessionParams struct {
	Command string `json:"command"` // 預設 "/bin/bash"
	Name    string `json:"name"`    // 可選，session 名稱
}

type SendInputParams struct {
	SessionID string `json:"session_id"`
	Input     string `json:"input"`
	TimeoutMs int    `json:"timeout_ms"`
}

type ReadOutputParams struct {
	SessionID string `json:"session_id"`
	TimeoutMs int    `json:"timeout_ms"`
}

type SendControlParams struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key"`
}

type SessionIDParams struct {
	SessionID string `json:"session_id"`
}

// --- Result 結構 ---

type SessionResult struct {
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
}

type OutputResult struct {
	Output     string `json:"output"`
	IsAlive    bool   `json:"is_alive"`
	IsComplete bool   `json:"is_complete"`
}

type SessionInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	IsAlive   bool   `json:"is_alive"`
	CreatedAt string `json:"created_at"`
	LastUsed  string `json:"last_used"`
}
