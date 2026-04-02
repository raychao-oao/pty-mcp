package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/raychao-oao/pty-mcp/internal/session"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
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
			"log_file":   map[string]any{"type": "string", "description": "File path to append all session output. Useful when output may exceed buffer size (e.g. long-running scripts). File is created if it doesn't exist."},
		},
		"required": []string{"host", "user"},
	}},
	{"name": "create_local_session", "description": "Open a local interactive terminal session (bash, python3, node, etc.). WARNING: Executes as the current user with full local system access — this is by design for legitimate sysadmin automation. Only use on trusted systems.", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":  map[string]any{"type": "string", "description": "Command to run (default: /bin/bash). Examples: /bin/bash, python3, node"},
			"log_file": map[string]any{"type": "string", "description": "File path to append all session output. Useful when output may exceed buffer size. File is created if it doesn't exist."},
		},
	}},
	{"name": "create_serial_session", "description": "Open a serial port session. Device path must start with /dev/tty or /dev/cu. (e.g. /dev/ttyUSB0, /dev/cu.usbserial-XXXX)", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"device":    map[string]any{"type": "string", "description": "Serial device path (must start with /dev/tty or /dev/cu.)"},
			"baud_rate": map[string]any{"type": "integer", "description": "Baud rate (default: 9600)"},
			"log_file":  map[string]any{"type": "string", "description": "File path to append all session output. File is created if it doesn't exist."},
		},
		"required": []string{"device"},
	}},
	{"name": "send_input", "description": "Send a command and wait for output to settle. Returns is_complete (false = timeout, use read_output for remaining output)", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"input":      map[string]any{"type": "string"},
			"timeout_ms": map[string]any{"type": "integer", "description": "Max wait time in ms (default: 5000, max: 30000)"},
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
	{"name": "send_secret", "description": "Prompt the human user to type a secret (password/passphrase) directly into a GUI dialog. The value is sent to the PTY session without ever appearing in AI context or logs. IMPORTANT: only call this when the session is actively waiting for a password input (echo is off) — e.g. an SSH/sudo/getpass prompt. Do NOT call this on an idle shell prompt.", "inputSchema": map[string]any{
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
	{"name": "get_session_state", "description": "Get detailed state of a session: type, target, is_alive, buffer cursor position, timestamps. Use cursor with read_output(since_cursor=...) for incremental reads.", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
		},
		"required": []string{"session_id"},
	}},
	{"name": "list_sessions", "description": "List all active sessions", "inputSchema": map[string]any{"type": "object"}},
	{"name": "list_remote_sessions", "description": "List persistent sessions on a remote ai-tmux server (use session_id to reattach)", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"host":            map[string]any{"type": "string", "description": "SSH host IP or hostname"},
			"port":            map[string]any{"type": "string", "description": "SSH port (default: 22)"},
			"user":            map[string]any{"type": "string"},
			"password":        map[string]any{"type": "string", "description": "Optional if using key auth"},
			"key_path":        map[string]any{"type": "string", "description": "SSH private key path"},
			"ignore_host_key": map[string]any{"type": "boolean"},
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
}

func Serve(h *Handler) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // max 10MB
	encoder := json.NewEncoder(os.Stdout)
	log.SetOutput(os.Stderr)
	log.Println("pty-mcp server started")

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			log.Printf("parse error: %v", err)
			continue
		}
		resp := handle(h, &req)
		if resp.ID == nil && resp.Result == nil && resp.Error == nil {
			continue
		}
		if err := encoder.Encode(resp); err != nil {
			log.Printf("encode error: %v", err)
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		log.Printf("stdin error: %v", err)
	}
}

func handle(h *Handler, req *request) response {
	switch req.Method {
	case "initialize":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "pty-mcp", "version": "0.5.0"},
		}}
	case "tools/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": toolsList}}
	case "tools/call":
		return handleToolCall(h, req)
	case "notifications/initialized", "$/cancelRequest":
		return response{}
	default:
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}}
	}
}

func handleToolCall(h *Handler, req *request) response {
	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errResp(req.ID, -32602, err.Error())
	}

	var result any
	var err error

	switch p.Name {
	case "create_ssh_session":
		result, err = h.CreateSSHSession(p.Arguments)
	case "create_local_session":
		result, err = h.CreateLocalSession(p.Arguments)
	case "create_serial_session":
		result, err = h.CreateSerialSession(p.Arguments)
	case "send_input":
		result, err = h.SendInput(p.Arguments)
	case "read_output":
		result, err = h.ReadOutput(p.Arguments)
	case "send_secret":
		result, err = h.SendSecret(p.Arguments)
	case "send_control":
		result, err = h.SendControl(p.Arguments)
	case "get_session_state":
		result, err = h.GetSessionState(p.Arguments)
	case "list_sessions":
		result, err = h.ListSessions(p.Arguments)
	case "list_remote_sessions":
		result, err = h.ListRemoteSessions(p.Arguments)
	case "close_session":
		result, err = h.CloseSession(p.Arguments)
	case "detach_session":
		result, err = h.DetachSession(p.Arguments)
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

func errResp(id any, code int, msg string) response {
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
