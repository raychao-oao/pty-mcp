package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/raychao-oao/pty-mcp/internal/buffer"
	"github.com/raychao-oao/pty-mcp/internal/pty"
	"github.com/raychao-oao/pty-mcp/internal/session"
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
	Persistent bool   `json:"persistent"`  // use ai-tmux persistent session
	Command    string `json:"command"`     // initial command in persistent mode
	SessionID  string `json:"session_id"`  // reattach to an existing ai-tmux session
	LogFile    string `json:"log_file"`    // append all output to this file path
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
	logFile, err := openLogFile(p.LogFile)
	if err != nil {
		return nil, err
	}

	var s session.Session
	var sessionType string

	if p.Persistent || p.SessionID != "" {
		if logFile != nil {
			logFile.Close() // remote sessions don't support log_file yet
			logFile = nil
		}
		s, err = session.NewRemoteSSHSession(cfg, p.Command, p.SessionID)
		sessionType = "remote"
	} else {
		s, err = session.NewSSHSessionWithLog(cfg, logFile)
		sessionType = "ssh"
	}

	if err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, err
	}
	target := fmt.Sprintf("%s@%s", p.User, p.Host)
	if err := h.mgr.Add(s, target); err != nil {
		return nil, err
	}
	return map[string]string{"session_id": s.ID(), "type": sessionType, "target": target}, nil
}

type ListRemoteParams struct {
	Host       string `json:"host"`
	Port       string `json:"port"`
	User       string `json:"user"`
	Password   string `json:"password"`
	KeyPath    string `json:"key_path"`
	IgnoreHost bool   `json:"ignore_host_key"`
}

func (h *Handler) ListRemoteSessions(params json.RawMessage) (any, error) {
	var p ListRemoteParams
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
	sessions, err := session.ListRemoteAiTmuxSessions(cfg)
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

type CreateLocalParams struct {
	Command string `json:"command"`  // default /bin/bash
	LogFile string `json:"log_file"` // append all output to this file path
}

func (h *Handler) CreateLocalSession(params json.RawMessage) (any, error) {
	var p CreateLocalParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Command == "" {
		p.Command = "/bin/bash"
	}
	logFile, err := openLogFile(p.LogFile)
	if err != nil {
		return nil, err
	}
	s, err := session.NewLocalSessionWithLog(p.Command, logFile)
	if err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, err
	}
	if err := h.mgr.Add(s, p.Command); err != nil {
		return nil, err
	}
	return map[string]string{"session_id": s.ID(), "type": "local", "command": p.Command}, nil
}

type CreateSerialParams struct {
	Device   string `json:"device"`
	BaudRate int    `json:"baud_rate"`
	LogFile  string `json:"log_file"` // append all output to this file path
}

func (h *Handler) CreateSerialSession(params json.RawMessage) (any, error) {
	var p CreateSerialParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	logFile, err := openLogFile(p.LogFile)
	if err != nil {
		return nil, err
	}
	s, err := session.NewSerialSessionWithLog(p.Device, p.BaudRate, logFile)
	if err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, err
	}
	target := fmt.Sprintf("%s@%d", p.Device, p.BaudRate)
	if err := h.mgr.Add(s, target); err != nil {
		return nil, err
	}
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
	// RemoteSession: pass timeout_ms to ai-tmux server's send_input
	if rs, ok := s.(*session.RemoteSession); ok {
		if err := rs.WriteWithTimeout(p.Input, p.TimeoutMs); err != nil {
			return nil, err
		}
		output, isComplete := rs.ReadScreen(p.TimeoutMs)
		return map[string]any{"output": output, "cursor": rs.Buffer().Snapshot(), "is_alive": rs.IsAlive(), "is_complete": isComplete}, nil
	}
	if err := s.Write(p.Input); err != nil {
		return nil, err
	}
	output, isComplete := s.ReadScreen(p.TimeoutMs)
	return map[string]any{"output": output, "cursor": s.Buffer().Snapshot(), "is_alive": s.IsAlive(), "is_complete": isComplete}, nil
}

type SessionIDParams struct {
	SessionID string `json:"session_id"`
}

type ReadOutputParams struct {
	SessionID    string  `json:"session_id"`
	Timeout      float64 `json:"timeout"`
	WaitFor      string  `json:"wait_for"`
	ContextLines int     `json:"context_lines"`
	TailLines    int     `json:"tail_lines"`
	SinceCursor  *int64  `json:"since_cursor"`
	MaxBytes     *int    `json:"max_bytes"`
}

type WaitForResult struct {
	Matched     bool   `json:"matched"`
	MatchLine   string `json:"match_line,omitempty"`
	Context     string `json:"context,omitempty"`
	Cursor      int64  `json:"cursor"`
	Error       string `json:"error,omitempty"`
	Tail        string `json:"tail,omitempty"`
	Warning     string `json:"warning,omitempty"`
	IsTruncated bool   `json:"is_truncated,omitempty"`
	IsAlive     bool   `json:"is_alive"`
}

type WaitForParams struct {
	WaitFor      string
	Timeout      time.Duration
	ContextLines int
	TailLines    int
}

// openLogFile opens path for appending (creates if not exists). Returns nil if path is empty.
func openLogFile(path string) (*os.File, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log_file %q: %w", path, err)
	}
	return f, nil
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func waitForPattern(rb *buffer.RingBuffer, isAlive func() bool, params WaitForParams) WaitForResult {
	// 1. Compile regex, fallback to plain text on error
	re, err := regexp.Compile(params.WaitFor)
	usePlainMatch := (err != nil)
	warning := ""
	if usePlainMatch {
		warning = "invalid regex, falling back to plain text match"
	}

	ctx, cancel := context.WithTimeout(context.Background(), params.Timeout)
	defer cancel()

	// Start from markSnapshot — where ReadScreen last left off.
	// Only match "unread" output, not stale data from earlier commands.
	snapshot := rb.MarkSnapshot()
	truncated := false
	var remainder string
	maxCollected := params.ContextLines + params.TailLines + 100
	if maxCollected < 200 {
		maxCollected = 200
	}
	collected := make([]string, 0, maxCollected)

	// Check existing buffer content first
	existing := rb.ReadSince(snapshot)
	if rb.IsTruncated(snapshot) {
		truncated = true
	}
	// Advance snapshot to current position so we only wait for new data
	snapshot = rb.Snapshot()
	if existing != "" {
		lines := strings.Split(existing, "\n")
		if len(lines) > 0 && !strings.HasSuffix(existing, "\n") {
			remainder = lines[len(lines)-1]
			lines = lines[:len(lines)-1]
		}
		for _, line := range lines {
			clean := pty.StripANSI(line)
			collected = append(collected, clean)
			if len(collected) > maxCollected {
				collected = collected[len(collected)-maxCollected:]
			}
			matched := false
			if usePlainMatch {
				matched = strings.Contains(clean, params.WaitFor)
			} else {
				matched = re.MatchString(clean)
			}
			if matched {
				return buildMatchResult(clean, collected, params.ContextLines, warning, truncated, isAlive())
			}
		}
	}

	// Main loop: wait for new data
	for {
		if !rb.Wait(ctx) {
			return buildTimeoutResult(collected, params, warning, truncated, isAlive())
		}

		chunk := rb.ReadSince(snapshot)
		if rb.IsTruncated(snapshot) {
			truncated = true
		}
		snapshot = rb.Snapshot()

		if chunk == "" {
			continue
		}

		chunk = remainder + chunk
		remainder = ""

		lines := strings.Split(chunk, "\n")
		if len(lines) > 0 && !strings.HasSuffix(chunk, "\n") {
			remainder = lines[len(lines)-1]
			lines = lines[:len(lines)-1]
		}

		for _, line := range lines {
			clean := pty.StripANSI(line)
			collected = append(collected, clean)
			if len(collected) > maxCollected {
				collected = collected[len(collected)-maxCollected:]
			}
			matched := false
			if usePlainMatch {
				matched = strings.Contains(clean, params.WaitFor)
			} else {
				matched = re.MatchString(clean)
			}
			if matched {
				return buildMatchResult(clean, collected, params.ContextLines, warning, truncated, isAlive())
			}
		}

		if !isAlive() {
			return WaitForResult{
				Matched:     false,
				Error:       "session terminated",
				Tail:        tailFromCollected(collected, params.TailLines),
				Warning:     warning,
				IsTruncated: truncated,
				IsAlive:     false,
			}
		}
	}
}

func buildMatchResult(matchLine string, collected []string, contextLines int, warning string, truncated bool, alive bool) WaitForResult {
	result := WaitForResult{
		Matched:   true,
		MatchLine: matchLine,
		IsAlive:   alive,
	}
	if warning != "" {
		result.Warning = warning
	}
	if truncated {
		result.IsTruncated = true
	}
	if contextLines > 0 {
		idx := len(collected) - 1
		start := idx - contextLines
		if start < 0 {
			start = 0
		}
		end := idx + contextLines + 1
		if end > len(collected) {
			end = len(collected)
		}
		result.Context = strings.Join(collected[start:end], "\n")
	}
	return result
}

func buildTimeoutResult(collected []string, params WaitForParams, warning string, truncated bool, alive bool) WaitForResult {
	result := WaitForResult{
		Matched: false,
		Error:   fmt.Sprintf("timeout after %ds", int(params.Timeout.Seconds())),
		IsAlive: alive,
	}
	if warning != "" {
		result.Warning = warning
	}
	if truncated {
		result.IsTruncated = true
	}
	if params.TailLines > 0 {
		result.Tail = tailFromCollected(collected, params.TailLines)
	}
	return result
}

func tailFromCollected(collected []string, n int) string {
	if n <= 0 || len(collected) == 0 {
		return ""
	}
	start := len(collected) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(collected[start:], "\n")
}

func (h *Handler) ReadOutput(params json.RawMessage) (any, error) {
	var p ReadOutputParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	s, err := h.mgr.Get(p.SessionID)
	if err != nil {
		return nil, err
	}

	// Mode 3: wait_for pattern matching
	if p.WaitFor != "" {
		timeoutSec := p.Timeout
		if timeoutSec <= 0 {
			timeoutSec = 5
		}
		if timeoutSec > 600 {
			timeoutSec = 600
		}
		contextLines := clampInt(p.ContextLines, 0, 50)
		tailLines := clampInt(p.TailLines, 0, 100)

		// Start remote polling if needed
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec*float64(time.Second)))
		defer cancel()
		go s.PollRemote(ctx)

		result := waitForPattern(s.Buffer(), s.IsAlive, WaitForParams{
			WaitFor:      p.WaitFor,
			Timeout:      time.Duration(timeoutSec * float64(time.Second)),
			ContextLines: contextLines,
			TailLines:    tailLines,
		})
		// Advance mark to current position so next read_output starts fresh
		s.Buffer().Mark()
		result.Cursor = s.Buffer().Snapshot()
		return result, nil
	}

	// Mode 2: incremental read from cursor position
	if p.SinceCursor != nil {
		rb := s.Buffer()
		maxBytes := 0
		if p.MaxBytes != nil && *p.MaxBytes > 0 {
			maxBytes = *p.MaxBytes
		}
		output, newCursor, hasMore := rb.ReadSinceMax(*p.SinceCursor, maxBytes)
		output = pty.StripANSI(output)
		isTruncated := rb.IsTruncated(*p.SinceCursor)
		return map[string]any{
			"output":       output,
			"cursor":       newCursor,
			"has_more":     hasMore,
			"is_truncated": isTruncated,
			"is_alive":     s.IsAlive(),
		}, nil
	}

	// Mode 1: existing behavior with optional custom timeout
	timeoutMs := 5000
	if p.Timeout > 0 {
		ms := int(p.Timeout * 1000)
		if ms > 600000 {
			ms = 600000
		}
		timeoutMs = ms
	}
	output, isComplete := s.ReadScreen(timeoutMs)
	return map[string]any{"output": output, "cursor": s.Buffer().Snapshot(), "is_alive": s.IsAlive(), "is_complete": isComplete}, nil
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
	return map[string]any{"output": output, "cursor": s.Buffer().Snapshot(), "is_alive": s.IsAlive(), "is_complete": isComplete}, nil
}

func supportedKeys() []string {
	keys := make([]string, 0, len(controlKeys))
	for k := range controlKeys {
		keys = append(keys, k)
	}
	return keys
}

func (h *Handler) GetSessionState(params json.RawMessage) (any, error) {
	var p SessionIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	s, err := h.mgr.Get(p.SessionID)
	if err != nil {
		return nil, err
	}
	info := h.mgr.GetInfo(p.SessionID)
	return map[string]any{
		"session_id": s.ID(),
		"type":       s.Type(),
		"target":     info.Target,
		"is_alive":   s.IsAlive(),
		"cursor":     s.Buffer().Snapshot(),
		"created_at": info.CreatedAt,
		"last_used":  info.LastUsed,
	}, nil
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

func (h *Handler) DetachSession(params json.RawMessage) (any, error) {
	var p SessionIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if err := h.mgr.Detach(p.SessionID); err != nil {
		return nil, err
	}
	return map[string]bool{"success": true}, nil
}

type SendSecretParams struct {
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt"`
}

func (h *Handler) SendSecret(params json.RawMessage) (any, error) {
	var p SendSecretParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	s, err := h.mgr.Get(p.SessionID)
	if err != nil {
		return nil, err
	}

	prompt := p.Prompt
	if prompt == "" {
		prompt = "Enter secret: "
	}

	secret, err := readSecretFromUser(prompt)
	if err != nil {
		return nil, err
	}

	if err := s.WriteRaw(string(secret) + "\r"); err != nil {
		return nil, fmt.Errorf("write to session: %w", err)
	}

	return map[string]any{"success": true}, nil
}

// readSecretFromUser prompts the human operator for a secret without
// exposing it to the AI context. It tries GUI dialogs first (so the
// prompt is visible even inside a TUI like Claude Code), then falls
// back to /dev/tty for headless environments.
//
// Priority:
//  1. macOS       → osascript (native password dialog)
//  2. WSL2        → powershell.exe Get-Credential (Windows GUI dialog)
//  3. Linux + $DISPLAY + zenity → zenity --password
//  4. Linux + $DISPLAY + kdialog → kdialog --password
//  5. Fallback    → /dev/tty (works in plain terminals, not inside TUI)
func readSecretFromUser(prompt string) ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		if secret, err := readSecretOsascript(prompt); err == nil {
			return secret, nil
		}
	case "linux":
		if isWSL2() {
			if secret, err := readSecretPowerShell(prompt); err == nil {
				return secret, nil
			}
		}
		if os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
			if _, err := exec.LookPath("zenity"); err == nil {
				if secret, err := readSecretZenity(prompt); err == nil {
					return secret, nil
				}
			}
			if _, err := exec.LookPath("kdialog"); err == nil {
				if secret, err := readSecretKdialog(prompt); err == nil {
					return secret, nil
				}
			}
		}
	}
	return readSecretTTY(prompt)
}

func isWSL2() bool {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
}

func readSecretPowerShell(prompt string) ([]byte, error) {
	// Get-Credential pops a standard Windows password dialog (input is masked).
	// Username field is pre-filled with "secret" and not editable.
	// stdout returns the plaintext password.
	// Use PowerShell single-quoted string: only '' is special, preventing injection.
	escaped := strings.ReplaceAll(prompt, "'", "''")
	cmdStr := fmt.Sprintf(
		`$cred = Get-Credential -UserName "secret" -Message '%s'; $cred.GetNetworkCredential().Password`,
		escaped,
	)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-Command", cmdStr).Output()
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimRight(string(out), "\r\n")), nil
}

func readSecretOsascript(prompt string) ([]byte, error) {
	// Pass prompt via environment variable to avoid AppleScript string injection.
	script := `tell application "System Events" to display dialog (system attribute "PTY_MCP_PROMPT") with hidden answer default answer "" buttons {"OK"} default button "OK"`
	cmd := exec.Command("osascript", "-e", script)
	cmd.Env = append(os.Environ(), "PTY_MCP_PROMPT="+prompt)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	// Output: "button returned:OK, text returned:<secret>\n"
	result := strings.TrimSpace(string(out))
	const prefix = "button returned:OK, text returned:"
	if !strings.HasPrefix(result, prefix) {
		return nil, fmt.Errorf("osascript: unexpected output: %s", result)
	}
	return []byte(result[len(prefix):]), nil
}

func readSecretZenity(prompt string) ([]byte, error) {
	// --no-markup disables Pango markup processing in the prompt text.
	out, err := exec.Command("zenity", "--password", "--title=pty-mcp", "--no-markup", "--text="+prompt).Output()
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimRight(string(out), "\n")), nil
}

func readSecretKdialog(prompt string) ([]byte, error) {
	out, err := exec.Command("kdialog", "--password", prompt, "--title", "pty-mcp").Output()
	if err != nil {
		return nil, err
	}
	return []byte(strings.TrimRight(string(out), "\n")), nil
}

func readSecretTTY(prompt string) ([]byte, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open /dev/tty: %w", err)
	}
	defer tty.Close()

	// Restore terminal echo if we are interrupted by a signal.
	// Close sigCh after signal.Stop to unblock the goroutine on normal completion.
	if oldState, err := term.GetState(int(tty.Fd())); err == nil {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			if _, ok := <-sigCh; ok {
				term.Restore(int(tty.Fd()), oldState) //nolint:errcheck
			}
		}()
		defer func() {
			signal.Stop(sigCh)
			close(sigCh)
		}()
	}

	fmt.Fprint(tty, "\n[pty-mcp] "+prompt)
	secret, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	if err != nil {
		return nil, fmt.Errorf("read secret: %w", err)
	}
	return secret, nil
}
