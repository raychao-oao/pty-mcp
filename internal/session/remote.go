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
	reqID     int
	closers   []io.Closer // SSH session + client，Close() 時一併關閉
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
	_, err := r.call("send_input", aitx.SendInputParams{
		SessionID: r.sessionID,
		Input:     input,
	})
	return err
}

func (r *RemoteSession) WriteRaw(data string) error {
	if !r.alive.Load() {
		return fmt.Errorf("session is not alive")
	}
	_, err := r.call("send_control", aitx.SendControlParams{
		SessionID: r.sessionID,
		Key:       data,
	})
	return err
}

func (r *RemoteSession) ReadScreen(timeoutMs int) (string, bool) {
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
