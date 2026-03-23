// internal/session/remote.go
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"pty-mcp/internal/aitx"
)

// RemoteSession 透過 ai-tmux client（SSH stdin/stdout）操作遠端 persistent session
type RemoteSession struct {
	id        string
	sessionID string // ai-tmux server 上的 session ID
	target    string
	stdin     io.Writer
	scanner   *bufio.Scanner
	mu        sync.Mutex // 保護 request/response 配對
	alive     atomic.Bool
	closeOnce sync.Once
	reqID      int
	closers    []io.Closer // SSH session + client，Close() 時一併關閉
	cacheMu    sync.Mutex // 保護 cachedOut
	cachedOut  *aitx.OutputResult // send_input 回傳的 output，供 ReadScreen 使用
}

// SetClosers 設定 Close() 時需要一併關閉的資源（SSH session, client）
func (r *RemoteSession) SetClosers(closers ...io.Closer) {
	r.closers = closers
}

func NewRemoteSession(id, target string, stdin io.Writer, stdout io.Reader, command string) (*RemoteSession, error) {
	r := &RemoteSession{
		id:      id,
		target:  target,
		stdin:   stdin,
		scanner: bufio.NewScanner(stdout),
	}
	r.scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	r.alive.Store(true)

	// 在遠端建立 session
	resp, err := r.call("create_session", aitx.CreateSessionParams{
		Command: command,
		Name:    target,
	})
	if err != nil {
		return nil, fmt.Errorf("create remote session: %w", err)
	}

	// 解析 session_id
	b, _ := json.Marshal(resp.Result)
	var result aitx.SessionResult
	json.Unmarshal(b, &result)
	r.sessionID = result.SessionID

	return r, nil
}

func (r *RemoteSession) call(method string, params any) (*aitx.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.reqID++
	reqID := fmt.Sprintf("r%d", r.reqID)

	paramsJSON, _ := json.Marshal(params)
	req := aitx.Request{
		ID:     reqID,
		Method: method,
		Params: json.RawMessage(paramsJSON),
	}

	b, _ := json.Marshal(req)
	b = append(b, '\n')
	if _, err := r.stdin.Write(b); err != nil {
		r.alive.Store(false)
		return nil, fmt.Errorf("write request: %w", err)
	}

	if !r.scanner.Scan() {
		r.alive.Store(false)
		return nil, fmt.Errorf("read response: connection closed")
	}

	var resp aitx.Response
	if err := json.Unmarshal(r.scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("remote error: %s", resp.Error)
	}

	return &resp, nil
}

func (r *RemoteSession) ID() string   { return r.id }
func (r *RemoteSession) Type() string { return "remote" }

func (r *RemoteSession) Write(input string) error {
	if !r.alive.Load() {
		return fmt.Errorf("session is not alive")
	}
	resp, err := r.call("send_input", aitx.SendInputParams{
		SessionID: r.sessionID,
		Input:     input,
	})
	if err != nil {
		return err
	}
	// 快取 send_input 回傳的 output，供後續 ReadScreen 使用
	b, _ := json.Marshal(resp.Result)
	var result aitx.OutputResult
	json.Unmarshal(b, &result)
	r.cacheMu.Lock()
	r.cachedOut = &result
	r.cacheMu.Unlock()
	return nil
}

// rawToKeyName 把 raw bytes 轉回 control key 名稱（reverse lookup）
var rawToKeyName = map[string]string{
	"\x03": "ctrl+c", "\x04": "ctrl+d", "\x1a": "ctrl+z",
	"\x0c": "ctrl+l", "\x12": "ctrl+r", "\r": "enter",
	"\t": "tab", "\x1b": "escape",
	"\x1b[A": "up", "\x1b[B": "down", "\x1b[D": "left", "\x1b[C": "right",
}

func (r *RemoteSession) WriteRaw(data string) error {
	if !r.alive.Load() {
		return fmt.Errorf("session is not alive")
	}
	// pty-mcp 的 tools.go 已將 key name 轉成 raw bytes，
	// 但 ai-tmux 的 send_control 需要 key name，所以做 reverse lookup
	key, ok := rawToKeyName[data]
	if !ok {
		return fmt.Errorf("unknown control sequence for remote session")
	}
	_, err := r.call("send_control", aitx.SendControlParams{
		SessionID: r.sessionID,
		Key:       key,
	})
	return err
}

func (r *RemoteSession) ReadScreen(timeoutMs int) (string, bool) {
	// 如果有 send_input 快取的 output，直接回傳
	r.cacheMu.Lock()
	if r.cachedOut != nil {
		out := r.cachedOut
		r.cachedOut = nil
		r.cacheMu.Unlock()
		return out.Output, out.IsComplete
	}
	r.cacheMu.Unlock()

	resp, err := r.call("read_output", aitx.ReadOutputParams{
		SessionID: r.sessionID,
		TimeoutMs: timeoutMs,
	})
	if err != nil {
		return "", false
	}

	b, _ := json.Marshal(resp.Result)
	var result aitx.OutputResult
	json.Unmarshal(b, &result)
	return result.Output, result.IsComplete
}

func (r *RemoteSession) IsAlive() bool {
	return r.alive.Load()
}

func (r *RemoteSession) Close() error {
	var closeErr error
	r.closeOnce.Do(func() {
		r.alive.Store(false)
		r.call("close_session", aitx.SessionIDParams{
			SessionID: r.sessionID,
		})
		// 關閉 SSH transport
		for _, c := range r.closers {
			if err := c.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
	})
	return closeErr
}
