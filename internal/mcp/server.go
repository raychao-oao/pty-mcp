package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/raychao-oao/pty-mcp/internal/session"
)

// Version is set from main via ldflags (-X main.version) forwarded at startup.
var Version = "dev"

// maxConcurrentToolCalls bounds how many tool calls actually execute at once.
// Requests beyond this queue on a semaphore acquired inside each request's own
// goroutine, so the stdin reader is never blocked and can always dispatch new
// work or process a cancellation notification immediately.
const maxConcurrentToolCalls = 32

// shutdownGracePeriod bounds how long Serve waits for in-flight requests to
// finish after stdin closes, before exiting anyway.
const shutdownGracePeriod = 10 * time.Second

// request.ID and response.ID are kept as raw JSON (not unmarshaled into `any`)
// so a request's ID round-trips exactly as sent — the MCP/JSON-RPC spec allows
// both string and number IDs, and converting through Go's `any` (float64 for
// numbers) can make distinct IDs collide (e.g. the string "1" and the number 1).
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

var toolsList = []map[string]any{
	{"name": "create_ssh_session", "description": "Open an interactive SSH session (supports key/password auth and SSH config aliases)", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"host":            map[string]any{"type": "string", "description": "SSH host IP or hostname"},
			"port":            map[string]any{"type": "string", "description": "SSH port (default: 22)"},
			"user":            map[string]any{"type": "string"},
			"password":        map[string]any{"type": "string", "description": "Optional if using key auth"},
			"key_path":        map[string]any{"type": "string", "description": "SSH private key path (default: ~/.ssh/id_ed25519, id_rsa)"},
			"ignore_host_key": map[string]any{"type": "boolean", "description": "Skip known_hosts check (not recommended)"},
			"persistent": map[string]any{"type": "boolean", "description": "Use ai-tmux for persistent session (survives SSH disconnect)"},
			"command":    map[string]any{"type": "string", "description": "Initial command for persistent session (default: /bin/bash)"},
			"session_id": map[string]any{"type": "string", "description": "Attach to existing ai-tmux session by ID (use list_remote_sessions to find IDs)"},
			"log_file":      map[string]any{"type": "string", "description": "File path to append all session output. Useful when output may exceed buffer size (e.g. long-running scripts). File is created if it doesn't exist."},
			"log_max_size":  map[string]any{"type": "integer", "description": "Max log file size in MB before rotation (0 = no rotation, default: 0)"},
			"log_max_files": map[string]any{"type": "integer", "description": "Max number of rotated log files to keep (default: 3)"},
		},
		"required": []string{"host", "user"},
	}},
	{"name": "create_local_session", "description": "Open a local interactive terminal session (bash, python3, node, etc.). WARNING: Executes as the current user with full local system access — this is by design for legitimate sysadmin automation. Only use on trusted systems.", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":  map[string]any{"type": "string", "description": "Command to run (default: /bin/bash). Examples: /bin/bash, python3, node"},
			"log_file":      map[string]any{"type": "string", "description": "File path to append all session output. Useful when output may exceed buffer size. File is created if it doesn't exist."},
			"log_max_size":  map[string]any{"type": "integer", "description": "Max log file size in MB before rotation (0 = no rotation, default: 0)"},
			"log_max_files": map[string]any{"type": "integer", "description": "Max number of rotated log files to keep (default: 3)"},
		},
	}},
	{"name": "create_serial_session", "description": "Open a serial port session. Device path must start with /dev/tty or /dev/cu. (e.g. /dev/ttyUSB0, /dev/cu.usbserial-XXXX)", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"device":    map[string]any{"type": "string", "description": "Serial device path (must start with /dev/tty or /dev/cu.)"},
			"baud_rate": map[string]any{"type": "integer", "description": "Baud rate (default: 9600)"},
			"log_file":      map[string]any{"type": "string", "description": "File path to append all session output. File is created if it doesn't exist."},
			"log_max_size":  map[string]any{"type": "integer", "description": "Max log file size in MB before rotation (0 = no rotation, default: 0)"},
			"log_max_files": map[string]any{"type": "integer", "description": "Max number of rotated log files to keep (default: 3)"},
		},
		"required": []string{"device"},
	}},
	{"name": "send_input", "description": "Send input and wait for output to settle. Returns cursor_start/cursor_end for command boundary tracking, and is_complete (false = timeout, use read_output for remaining output). If wait_for is set, blocks until the pattern matches (combines send_input + read_output wait_for in one call).", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id":       map[string]any{"type": "string"},
			"input":            map[string]any{"type": "string"},
			"timeout_ms":       map[string]any{"type": "integer", "description": "Max wait time in ms (default: 5000, max: 30000)"},
			"raw":              map[string]any{"type": "boolean", "description": "If true, send input exactly as-is without appending a newline. Use for interactive menus and single-character inputs (e.g. menu selections, y/n prompts). Follow with send_control('enter') when ready to submit."},
			"wait_for":         map[string]any{"type": "string", "description": "Regex pattern to wait for after sending input. Combines send_input + read_output(wait_for=...) into one tool call."},
			"wait_for_timeout": map[string]any{"type": "number", "description": "Timeout in seconds for wait_for (default: 10, max: 600)"},
		},
		"required": []string{"session_id", "input"},
	}},
	{"name": "read_output", "description": "Read output from a session. Three modes: (1) default: wait for output to settle, (2) since_cursor: incremental read from a cursor position (returns only new output), (3) wait_for: block until a regex pattern matches. Mode 2 response includes has_more (true = more unread data, call again with new cursor) and is_truncated (true = data was overwritten before you read it).", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id":    map[string]any{"type": "string", "description": "Session ID to read from"},
			"timeout":       map[string]any{"type": "number", "description": "Max wait time in seconds (default: 5, max: 600)"},
			"since_cursor":  map[string]any{"type": "integer", "description": "Read only output written after this cursor position. Get cursor from previous read_output/send_input/get_session_state responses."},
			"max_bytes":     map[string]any{"type": "integer", "description": "Maximum bytes to return in a single read (mode 2 only). If output exceeds this, has_more=true and you should call again with the returned cursor. Recommended: 32768 (32KB) to avoid large context usage."},
			"wait_for":      map[string]any{"type": "string", "description": "Regex pattern to wait for. Falls back to plain text match if regex is invalid."},
			"context_lines": map[string]any{"type": "integer", "description": "Lines before/after matched line to include (default: 0, max: 50). Only with wait_for."},
			"tail_lines":    map[string]any{"type": "integer", "description": "On timeout, include last N lines of output (default: 0, max: 100). Only with wait_for."},
		},
		"required": []string{"session_id"},
	}},
	{"name": "prepare_secret", "description": "Pre-stage a secret (password/passphrase) for a session. Shows a GUI dialog NOW so the operator can enter the secret before a password prompt appears. The secret is stored in a buffer and automatically sent when a password prompt is detected — no further agent action needed. Use this before connecting to devices with short password timeouts (e.g. serial console). The buffered secret is never logged.", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id":  map[string]any{"type": "string"},
			"prompt":      map[string]any{"type": "string", "description": "Prompt shown to the user (default: \"Enter secret: \")"},
			"line_ending": map[string]any{"type": "string", "description": "Line ending appended after the secret (default: \"\\r\"). Use \"\\r\\n\" for serial consoles that require CR+LF, \"\\n\" for Linux terminals."},
		},
		"required": []string{"session_id"},
	}},
	{"name": "send_secret", "description": "Prompt the human user to type a secret (password/passphrase) directly into a GUI dialog. The value is sent to the PTY session without ever appearing in AI context or logs. IMPORTANT: only call this when the session is actively waiting for a password input (echo is off) — e.g. an SSH/sudo/getpass prompt. Do NOT call this on an idle shell prompt. If prepare_secret was called earlier for this session, uses the buffered secret without showing a dialog.", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"prompt":     map[string]any{"type": "string", "description": "Prompt shown to the user (default: \"Enter secret: \")"},
		},
		"required": []string{"session_id"},
	}},
	{"name": "send_control", "description": "Send a control key (ctrl+c, ctrl+d, enter, tab, up, down, etc.)", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{"session_id": map[string]any{"type": "string"}, "key": map[string]any{"type": "string"}},
		"required": []string{"session_id", "key"},
	}},
	{"name": "get_session_state", "description": "Get detailed state of a session: type, target, is_alive, cursor, and classified state (at_prompt/password_prompt/confirmation/pager/running/unknown), awaiting_secret, last_prompt. Use cursor with read_output(since_cursor=...) for incremental reads.", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
		},
		"required": []string{"session_id"},
	}},
	{"name": "list_sessions", "description": "List all active sessions", "inputSchema": map[string]any{"type": "object"}},
	{"name": "list_remote_sessions", "description": "List persistent sessions on a remote ai-tmux server (use session_id to reattach). Optionally filter by status.", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"host":            map[string]any{"type": "string", "description": "SSH host IP or hostname"},
			"port":            map[string]any{"type": "string", "description": "SSH port (default: 22)"},
			"user":            map[string]any{"type": "string"},
			"password":        map[string]any{"type": "string", "description": "Optional if using key auth"},
			"key_path":        map[string]any{"type": "string", "description": "SSH private key path"},
			"ignore_host_key": map[string]any{"type": "boolean"},
			"status":          map[string]any{"type": "string", "description": "Filter by session status (e.g. 'running', 'idle')"},
		},
		"required": []string{"host", "user"},
	}},
	{"name": "close_session", "description": "Close a session (also terminates remote PTY)", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{"session_id": map[string]any{"type": "string"}},
		"required": []string{"session_id"},
	}},
	{"name": "detach_session", "description": "Detach from a persistent session but keep the remote PTY running (reattach via list_remote_sessions + session_id)", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{"session_id": map[string]any{"type": "string"}},
		"required": []string{"session_id"},
	}},
	{"name": "resize_session", "description": "Resize the terminal window (rows x cols) for a session. Affects how TUI tools (top, less, vim, etc.) lay out output. Serial sessions are not supported.", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"rows":       map[string]any{"type": "integer", "description": "Terminal height in rows (e.g. 40)"},
			"cols":       map[string]any{"type": "integer", "description": "Terminal width in columns (e.g. 220)"},
		},
		"required": []string{"session_id", "rows", "cols"},
	}},
	{"name": "get_credential_bundle", "description": "Generate a signed ConsumerBundle for use with cred-mcp's vault_seal tool. The bundle contains only public keys and is safe to pass to the AI. The session private key is held in memory for a matching inject_secret call. Call this before request_authorization + vault_seal on cred-mcp.", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"consumer_id": map[string]any{"type": "string", "description": "Consumer identity (default: \"pty-mcp\")"},
			"ttl_seconds": map[string]any{"type": "integer", "description": "Bundle validity in seconds (default: 300, max: 3600)"},
		},
	}},
	{"name": "inject_secret", "description": "Decrypt a SealedBox from cred-mcp and write the plaintext directly into a PTY session. The plaintext never appears in AI context or tool results — only {success:true} is returned. Call after vault_seal on cred-mcp.", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pty_session_id": map[string]any{"type": "string", "description": "ID of the PTY session to inject the secret into"},
			"sealed_box":     map[string]any{"type": "object", "description": "SealedBox JSON object returned by cred-mcp's vault_seal tool"},
		},
		"required": []string{"pty_session_id", "sealed_box"},
	}},
}

// inflightEntry tracks one in-flight request's cancel func and whether it has
// been cancelled. Guarded by inflightRegistry.mu (not its own lock) so that
// "mark cancelled" and "decide whether to send a response" linearize.
type inflightEntry struct {
	cancel    context.CancelFunc
	cancelled bool
}

// inflightRegistry maps a request's raw JSON id (as a string key) to the
// entries currently processing it. A slice per key (not a single entry)
// defensively handles a client reusing an id before the prior request with
// that id finished — cancel() cancels all of them, finish() pops one.
type inflightRegistry struct {
	mu      sync.Mutex
	entries map[string][]*inflightEntry
}

func newInflightRegistry() *inflightRegistry {
	return &inflightRegistry{entries: make(map[string][]*inflightEntry)}
}

func (r *inflightRegistry) add(key string, cancel context.CancelFunc) *inflightEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := &inflightEntry{cancel: cancel}
	r.entries[key] = append(r.entries[key], e)
	return e
}

// cancel marks every in-flight entry for key as cancelled and invokes its
// cancel func. No-op if the id is unknown (already finished, or never existed).
func (r *inflightRegistry) cancel(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries[key] {
		e.cancelled = true
		e.cancel()
	}
}

// finish removes one entry for key and reports whether it was cancelled — the
// caller must not send a response for the request in that case, per the MCP
// cancellation notification spec.
func (r *inflightRegistry) finish(key string, e *inflightEntry) (wasCancelled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := r.entries[key]
	for i, cand := range entries {
		if cand == e {
			entries = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	if len(entries) == 0 {
		delete(r.entries, key)
	} else {
		r.entries[key] = entries
	}
	return e.cancelled
}

type cancelParams struct {
	RequestID json.RawMessage `json:"requestId"`
	ID        json.RawMessage `json:"id"` // legacy alias accepted defensively
}

func handleCancellation(inflight *inflightRegistry, raw json.RawMessage) {
	var p cancelParams
	if err := json.Unmarshal(raw, &p); err != nil {
		log.Printf("cancel notification: parse error: %v", err)
		return
	}
	key := string(p.RequestID)
	if len(p.RequestID) == 0 {
		key = string(p.ID)
	}
	if key == "" || key == "null" {
		return
	}
	inflight.cancel(key)
}

func Serve(h *Handler) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // max 10MB
	log.SetOutput(os.Stderr)
	log.Println("pty-mcp server started")

	respCh := make(chan response, 64)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		encoder := json.NewEncoder(os.Stdout)
		for resp := range respCh {
			if err := encoder.Encode(resp); err != nil {
				log.Printf("encode error: %v", err)
			}
		}
	}()

	inflight := newInflightRegistry()
	sem := make(chan struct{}, maxConcurrentToolCalls)
	var wg sync.WaitGroup

	// The reader loop must never block on anything but stdin itself — a
	// cancellation notification has to reach handleCancellation immediately
	// even while another request is still running, or cancelling it is
	// pointless. So each request's own goroutine acquires the concurrency
	// semaphore, not this loop.
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...) // scanner reuses its buffer
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			log.Printf("parse error: %v", err)
			continue
		}

		if req.Method == "notifications/cancelled" || req.Method == "$/cancelRequest" {
			handleCancellation(inflight, req.Params)
			continue
		}

		isNotification := len(req.ID) == 0
		ctx, cancel := context.WithCancel(context.Background())

		var key string
		var entry *inflightEntry
		if !isNotification {
			key = string(req.ID)
			entry = inflight.add(key, cancel)
		}

		wg.Add(1)
		go func(req request) {
			defer wg.Done()
			defer cancel()

			if ctx.Err() != nil {
				// Already cancelled — check explicitly instead of relying on
				// the select below: if both cases are simultaneously ready,
				// Go picks between them pseudo-randomly, which could still
				// run a side-effecting handler despite cancellation.
				if !isNotification {
					inflight.finish(key, entry)
				}
				return
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				// Cancelled before we even got a concurrency slot: don't run
				// the handler at all (it may have side effects, e.g.
				// create_local_session) and don't respond.
				if !isNotification {
					inflight.finish(key, entry)
				}
				return
			}
			defer func() { <-sem }()

			resp := handle(ctx, h, &req)
			if isNotification {
				return
			}
			if inflight.finish(key, entry) {
				return // cancelled: MCP spec says don't respond
			}
			respCh <- resp
		}(req)
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		log.Printf("stdin error: %v", err)
	}

	// A RemoteSession's network read can't be interrupted by ctx cancellation
	// (see RemoteSession.ReadScreen) — a stalled connection could in theory
	// keep a goroutine alive forever. Don't let that wedge process shutdown:
	// give in-flight work a bounded grace period, then exit anyway. We
	// intentionally leak respCh/writerDone on the timeout path rather than
	// close them, since a goroutine that eventually does finish must not
	// send on a closed channel.
	shutdownDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		close(respCh)
		<-writerDone
	case <-time.After(shutdownGracePeriod):
		log.Printf("Serve: in-flight requests did not finish within %s, exiting anyway", shutdownGracePeriod)
	}
}

func handle(ctx context.Context, h *Handler, req *request) response {
	switch req.Method {
	case "initialize":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "pty-mcp", "version": Version},
		}}
	case "tools/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": toolsList}}
	case "tools/call":
		return handleToolCall(ctx, h, req)
	case "notifications/initialized":
		return response{}
	default:
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}}
	}
}

func handleToolCall(ctx context.Context, h *Handler, req *request) response {
	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errResp(req.ID, -32602, err.Error())
	}

	var result any
	var err error

	switch p.Name {
	case "create_ssh_session":
		result, err = h.CreateSSHSession(ctx, p.Arguments)
	case "create_local_session":
		result, err = h.CreateLocalSession(ctx, p.Arguments)
	case "create_serial_session":
		result, err = h.CreateSerialSession(ctx, p.Arguments)
	case "send_input":
		result, err = h.SendInput(ctx, p.Arguments)
	case "read_output":
		result, err = h.ReadOutput(ctx, p.Arguments)
	case "prepare_secret":
		result, err = h.PrepareSecret(ctx, p.Arguments)
	case "send_secret":
		result, err = h.SendSecret(ctx, p.Arguments)
	case "send_control":
		result, err = h.SendControl(ctx, p.Arguments)
	case "get_session_state":
		result, err = h.GetSessionState(ctx, p.Arguments)
	case "list_sessions":
		result, err = h.ListSessions(ctx, p.Arguments)
	case "list_remote_sessions":
		result, err = h.ListRemoteSessions(ctx, p.Arguments)
	case "close_session":
		result, err = h.CloseSession(ctx, p.Arguments)
	case "detach_session":
		result, err = h.DetachSession(ctx, p.Arguments)
	case "resize_session":
		result, err = h.ResizeSession(ctx, p.Arguments)
	case "get_credential_bundle":
		result, err = h.GetCredentialBundle(ctx, p.Arguments)
	case "inject_secret":
		result, err = h.InjectSecret(ctx, p.Arguments)
	default:
		return errResp(req.ID, -32601, fmt.Sprintf("unknown tool: %s", p.Name))
	}

	if err != nil {
		te := classifyError(err)
		b, _ := json.Marshal(te)
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(b)}},
			"isError": true,
		}}
	}
	b, _ := json.MarshalIndent(result, "", "  ")
	return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
	}}
}

func errResp(id json.RawMessage, code int, msg string) response {
	return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// classifyError maps known error types to structured ToolErrors.
func classifyError(err error) *ToolError {
	if te, ok := err.(*ToolError); ok {
		return te
	}
	var notFound *session.SessionNotFoundError
	if errors.As(err, &notFound) {
		return newToolError(ErrSessionNotFound, err.Error(), false)
	}
	var limitErr *session.SessionLimitError
	if errors.As(err, &limitErr) {
		return newToolError(ErrSessionLimit, err.Error(), true)
	}
	// Heuristic classification for errors from SSH, serial, etc.
	msg := err.Error()
	switch {
	case contains(msg, "ssh: unable to authenticate", "ssh: handshake failed", "no supported methods remain"):
		return newToolError(ErrSSHAuthFailed, msg, false)
	case contains(msg, "dial tcp", "connection refused", "no route to host", "i/o timeout"):
		return newToolError(ErrSSHConnFailed, msg, true)
	case contains(msg, "serial", "no such file or directory") && contains(msg, "/dev/"):
		return newToolError(ErrSerialFailed, msg, false)
	case contains(msg, "write to session", "broken pipe", "write:"):
		return newToolError(ErrWriteFailed, msg, false)
	}
	return newToolError("INTERNAL_ERROR", msg, false)
}

func contains(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
