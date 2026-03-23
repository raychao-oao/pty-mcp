package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
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
	{"name": "create_ssh_session", "description": "開啟 SSH 互動式 session（支援 key/password 認證）", "inputSchema": map[string]any{
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
		},
		"required": []string{"host", "user"},
	}},
	{"name": "create_serial_session", "description": "開啟 Serial port session", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"device":    map[string]any{"type": "string", "description": "Serial device path, e.g. /dev/tty.usbserial-XXXX"},
			"baud_rate": map[string]any{"type": "integer", "description": "Baud rate (default: 9600)"},
		},
		"required": []string{"device"},
	}},
	{"name": "send_input", "description": "送入指令並等待輸出 settle。回傳 is_complete 表示指令是否完成（false 表示 timeout，可用 read_output 取得後續輸出）", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"input":      map[string]any{"type": "string"},
			"timeout_ms": map[string]any{"type": "integer", "description": "Max wait time in ms (default: 5000, max: 30000)"},
		},
		"required": []string{"session_id", "input"},
	}},
	{"name": "read_output", "description": "讀取目前 session 畫面（不送入任何指令）", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{"session_id": map[string]any{"type": "string"}},
		"required": []string{"session_id"},
	}},
	{"name": "send_control", "description": "送入控制鍵（ctrl+c, ctrl+d, enter, tab, up, down...）", "inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{"session_id": map[string]any{"type": "string"}, "key": map[string]any{"type": "string"}},
		"required": []string{"session_id", "key"},
	}},
	{"name": "list_sessions", "description": "列出所有 active sessions", "inputSchema": map[string]any{"type": "object"}},
	{"name": "close_session", "description": "關閉 session", "inputSchema": map[string]any{
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
			"serverInfo":      map[string]any{"name": "pty-mcp", "version": "0.1.0"},
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
	case "create_serial_session":
		result, err = h.CreateSerialSession(p.Arguments)
	case "send_input":
		result, err = h.SendInput(p.Arguments)
	case "read_output":
		result, err = h.ReadOutput(p.Arguments)
	case "send_control":
		result, err = h.SendControl(p.Arguments)
	case "list_sessions":
		result, err = h.ListSessions(p.Arguments)
	case "close_session":
		result, err = h.CloseSession(p.Arguments)
	default:
		return errResp(req.ID, -32601, fmt.Sprintf("unknown tool: %s", p.Name))
	}

	if err != nil {
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
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
