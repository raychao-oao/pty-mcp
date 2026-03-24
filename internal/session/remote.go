// internal/session/remote.go
package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/raychao-oao/pty-mcp/internal/aitx"
	"github.com/raychao-oao/pty-mcp/internal/buffer"
)

// RemoteSession operates a remote persistent session via ai-tmux client (SSH stdin/stdout)
type RemoteSession struct {
	id        string
	sessionID string // session ID on the ai-tmux server
	target    string
	stdin     io.Writer
	scanner   *bufio.Scanner
	mu        sync.Mutex // protects request/response pairing
	alive     atomic.Bool
	closeOnce sync.Once
	reqID      int
	closers    []io.Closer // SSH session + client, closed together on Close()
	cacheMu    sync.Mutex // protects cachedOut
	cachedOut  *aitx.OutputResult // output returned by send_input, used by ReadScreen
	localBuf   *buffer.RingBuffer
	isPolling  atomic.Bool
}

// SetClosers sets resources (SSH session, client) to be closed together on Close()
func (r *RemoteSession) SetClosers(closers ...io.Closer) {
	r.closers = closers
}

// AttachRemoteSession reattaches to an existing ai-tmux session (does not create a new one)
func AttachRemoteSession(id, target string, stdin io.Writer, stdout io.Reader, remoteSessionID string) (*RemoteSession, error) {
	r := &RemoteSession{
		id:        id,
		sessionID: remoteSessionID,
		target:    target,
		stdin:     stdin,
		scanner:   bufio.NewScanner(stdout),
		localBuf:  buffer.NewRingBuffer(buffer.BufferSizeFromEnv()),
	}
	r.scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	r.alive.Store(true)

	// verify that the session exists and is alive
	resp, err := r.call("read_output", aitx.ReadOutputParams{
		SessionID: remoteSessionID,
		TimeoutMs: 1000,
	})
	if err != nil {
		return nil, fmt.Errorf("attach session %s: %w", remoteSessionID, err)
	}
	_ = resp // session exists

	return r, nil
}

func NewRemoteSession(id, target string, stdin io.Writer, stdout io.Reader, command string) (*RemoteSession, error) {
	r := &RemoteSession{
		id:       id,
		target:   target,
		stdin:    stdin,
		scanner:  bufio.NewScanner(stdout),
		localBuf: buffer.NewRingBuffer(buffer.BufferSizeFromEnv()),
	}
	r.scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	r.alive.Store(true)

	// create a session on the remote
	resp, err := r.call("create_session", aitx.CreateSessionParams{
		Command: command,
		Name:    target,
	})
	if err != nil {
		return nil, fmt.Errorf("create remote session: %w", err)
	}

	// parse session_id
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

// WriteWithTimeout sends input with a specific timeout for settle detection
func (r *RemoteSession) WriteWithTimeout(input string, timeoutMs int) error {
	if !r.alive.Load() {
		return fmt.Errorf("session is not alive")
	}
	resp, err := r.call("send_input", aitx.SendInputParams{
		SessionID: r.sessionID,
		Input:     input,
		TimeoutMs: timeoutMs,
	})
	if err != nil {
		return err
	}
	b, _ := json.Marshal(resp.Result)
	var result aitx.OutputResult
	json.Unmarshal(b, &result)
	r.cacheMu.Lock()
	r.cachedOut = &result
	r.cacheMu.Unlock()
	return nil
}

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
	// cache output returned by send_input for subsequent ReadScreen calls
	b, _ := json.Marshal(resp.Result)
	var result aitx.OutputResult
	json.Unmarshal(b, &result)
	r.cacheMu.Lock()
	r.cachedOut = &result
	r.cacheMu.Unlock()
	return nil
}

// rawToKeyName converts raw bytes back to control key names (reverse lookup)
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
	// tools.go in pty-mcp converts key names to raw bytes,
	// but ai-tmux send_control requires key names, so reverse lookup is needed
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
	// return cached output from send_input if available
	r.cacheMu.Lock()
	if r.cachedOut != nil {
		out := r.cachedOut
		r.cachedOut = nil
		r.cacheMu.Unlock()
		r.localBuf.Write([]byte(out.Output)) // feed local buffer
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
	r.localBuf.Write([]byte(result.Output)) // feed local buffer
	return result.Output, result.IsComplete
}

func (r *RemoteSession) IsAlive() bool {
	return r.alive.Load()
}

// Detach disconnects the local connection while keeping the remote session alive
func (r *RemoteSession) Detach() error {
	var detachErr error
	r.closeOnce.Do(func() {
		r.alive.Store(false)
		// do not send close_session — remote session stays alive
		for _, c := range r.closers {
			if err := c.Close(); err != nil && detachErr == nil {
				detachErr = err
			}
		}
	})
	return detachErr
}

func (r *RemoteSession) Close() error {
	var closeErr error
	r.closeOnce.Do(func() {
		r.alive.Store(false)
		r.call("close_session", aitx.SessionIDParams{
			SessionID: r.sessionID,
		})
		// close SSH transport
		for _, c := range r.closers {
			if err := c.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
	})
	return closeErr
}

// Buffer returns the local RingBuffer that accumulates output from ReadScreen calls.
func (r *RemoteSession) Buffer() *buffer.RingBuffer { return r.localBuf }

// PollRemote continuously polls the remote session for output, feeding the local buffer.
// It is safe to call concurrently — only one goroutine will poll at a time.
func (r *RemoteSession) PollRemote(ctx context.Context) {
	if !r.isPolling.CompareAndSwap(false, true) {
		return // already polling
	}
	defer r.isPolling.Store(false)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !r.alive.Load() {
				return
			}
			r.ReadScreen(500) // short timeout, result written to localBuf
		}
	}
}
