// internal/session/ssh.go
package session

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	ssh_config "github.com/kevinburke/ssh_config"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"pty-mcp/internal/pty"
)

type SSHConfig struct {
	Host       string
	Port       string
	User       string
	Password   string
	KeyPath    string
	IgnoreHost bool
}

type SSHSession struct {
	id        string
	client    *gossh.Client
	session   *gossh.Session
	stdin     io.WriteCloser
	buf       lockedBuffer
	alive     atomic.Bool
	closeOnce sync.Once
}

// lockedBuffer is a thread-safe bytes.Buffer for concurrent SSH stdout/stderr writes
type lockedBuffer struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	snapshot int
}

func (lb *lockedBuffer) Write(p []byte) (int, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return lb.buf.Write(p)
}

func (lb *lockedBuffer) String() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return lb.buf.String()
}

func (lb *lockedBuffer) Since() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	b := lb.buf.Bytes()
	if lb.snapshot >= len(b) {
		return ""
	}
	return string(b[lb.snapshot:])
}

func (lb *lockedBuffer) Mark() {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.snapshot = lb.buf.Len()
}

// SinceAndMark atomically reads since snapshot and advances the snapshot
func (lb *lockedBuffer) SinceAndMark() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	b := lb.buf.Bytes()
	if lb.snapshot >= len(b) {
		return ""
	}
	result := string(b[lb.snapshot:])
	lb.snapshot = len(b)
	return result
}

// resolveSSHConfig 從 ~/.ssh/config 填充未提供的欄位
func resolveSSHConfig(cfg *SSHConfig) {
	host := cfg.Host

	if hostname := ssh_config.Get(host, "HostName"); hostname != "" {
		cfg.Host = hostname
	}

	if cfg.Port == "" {
		if port := ssh_config.Get(host, "Port"); port != "" {
			cfg.Port = port
		}
	}

	if cfg.User == "" {
		if user := ssh_config.Get(host, "User"); user != "" {
			cfg.User = user
		}
	}

	if cfg.KeyPath == "" {
		if keyPath := ssh_config.Get(host, "IdentityFile"); keyPath != "" && keyPath != "~/.ssh/identity" {
			if len(keyPath) > 1 && keyPath[0] == '~' {
				if home, err := os.UserHomeDir(); err == nil {
					keyPath = filepath.Join(home, keyPath[1:])
				}
			}
			cfg.KeyPath = keyPath
		}
	}
}

func NewSSHSession(cfg SSHConfig) (*SSHSession, error) {
	resolveSSHConfig(&cfg)

	authMethods, err := buildAuthMethods(cfg)
	if err != nil {
		return nil, err
	}

	hostKeyCallback, err := buildHostKeyCallback(cfg)
	if err != nil {
		return nil, err
	}

	config := &gossh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}

	if cfg.Port == "" {
		cfg.Port = "22"
	}
	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	client, err := gossh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("new session: %w", err)
	}

	stdinPipe, err := sess.StdinPipe()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	s := &SSHSession{
		id:      NewID(),
		client:  client,
		session: sess,
		stdin:   stdinPipe,
	}
	s.alive.Store(true)

	sess.Stdout = &s.buf
	sess.Stderr = &s.buf

	if err := sess.RequestPty("xterm-256color", 40, 120, gossh.TerminalModes{
		gossh.ECHO:          1,
		gossh.TTY_OP_ISPEED: 14400,
		gossh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		s.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}

	if err := sess.Shell(); err != nil {
		s.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}

	// 偵測遠端斷線
	go func() {
		err := sess.Wait()
		if err != nil {
			log.Printf("[pty-mcp] ssh session ended: %v", err)
		}
		s.alive.Store(false)
	}()

	pty.WaitForSettle(func() string {
		return s.buf.String()
	}, 300*time.Millisecond, 3*time.Second) // 初始 prompt 等待，忽略 isComplete

	return s, nil
}

func buildAuthMethods(cfg SSHConfig) ([]gossh.AuthMethod, error) {
	var methods []gossh.AuthMethod

	keyPaths := []string{cfg.KeyPath}
	if cfg.KeyPath == "" {
		home, _ := os.UserHomeDir()
		keyPaths = []string{
			filepath.Join(home, ".ssh", "id_ed25519"),
			filepath.Join(home, ".ssh", "id_rsa"),
			filepath.Join(home, ".ssh", "id_ecdsa"),
		}
	}

	for _, kp := range keyPaths {
		if kp == "" {
			continue
		}
		data, err := os.ReadFile(kp)
		if err != nil {
			continue
		}
		signer, err := gossh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("parse key %s: %w", kp, err)
		}
		methods = append(methods, gossh.PublicKeys(signer))
	}

	if cfg.Password != "" {
		methods = append(methods, gossh.Password(cfg.Password))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no auth method available (no key found, no password provided)")
	}

	return methods, nil
}

func buildHostKeyCallback(cfg SSHConfig) (gossh.HostKeyCallback, error) {
	if cfg.IgnoreHost {
		return gossh.InsecureIgnoreHostKey(), nil
	}

	home, _ := os.UserHomeDir()
	knownHostsFile := filepath.Join(home, ".ssh", "known_hosts")

	if _, err := os.Stat(knownHostsFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "[pty-mcp] warning: %s not found, host key verification disabled\n", knownHostsFile)
		return gossh.InsecureIgnoreHostKey(), nil
	}

	cb, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts: %w", err)
	}
	return cb, nil
}

// buildClientConfig 建立 SSH client config（抽出共用邏輯）
func buildClientConfig(cfg SSHConfig) *gossh.ClientConfig {
	authMethods, err := buildAuthMethods(cfg)
	if err != nil {
		return nil
	}
	hostKeyCallback, err := buildHostKeyCallback(cfg)
	if err != nil {
		return nil
	}
	return &gossh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}
}

// HasAiTmux 檢查遠端是否有 ai-tmux binary
func HasAiTmux(cfg SSHConfig) bool {
	resolveSSHConfig(&cfg)

	config := buildClientConfig(cfg)
	if config == nil {
		return false
	}

	if cfg.Port == "" {
		cfg.Port = "22"
	}

	client, err := gossh.Dial("tcp", net.JoinHostPort(cfg.Host, cfg.Port), config)
	if err != nil {
		return false
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return false
	}
	defer sess.Close()

	err = sess.Run("which ai-tmux")
	return err == nil
}

// NewRemoteSSHSession SSH 連上遠端後啟動 ai-tmux client，建立 persistent session
func NewRemoteSSHSession(cfg SSHConfig, command string) (*RemoteSession, error) {
	resolveSSHConfig(&cfg)

	config := buildClientConfig(cfg)
	if config == nil {
		return nil, fmt.Errorf("failed to build SSH config")
	}

	if cfg.Port == "" {
		cfg.Port = "22"
	}

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	client, err := gossh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("new session: %w", err)
	}

	stdinPipe, err := sess.StdinPipe()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := sess.StdoutPipe()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := sess.Start("ai-tmux client"); err != nil {
		client.Close()
		return nil, fmt.Errorf("start ai-tmux: %w", err)
	}

	id := NewID()
	target := fmt.Sprintf("%s@%s", cfg.User, cfg.Host)
	if command == "" {
		command = "/bin/bash"
	}

	remote, err := NewRemoteSession(id, target, stdinPipe, stdoutPipe, command)
	if err != nil {
		sess.Close()
		client.Close()
		return nil, err
	}

	return remote, nil
}

func (s *SSHSession) ID() string   { return s.id }
func (s *SSHSession) Type() string { return "ssh" }

func (s *SSHSession) Write(input string) error {
	if !s.alive.Load() {
		return fmt.Errorf("session is not alive")
	}
	s.buf.Mark()
	_, err := fmt.Fprint(s.stdin, input+"\n")
	return err
}

func (s *SSHSession) WriteRaw(data string) error {
	if !s.alive.Load() {
		return fmt.Errorf("session is not alive")
	}
	s.buf.Mark()
	_, err := fmt.Fprint(s.stdin, data)
	return err
}

func (s *SSHSession) ReadScreen(timeoutMs int) (string, bool) {
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	output, isComplete := pty.WaitForSettle(func() string {
		return s.buf.Since()
	}, 300*time.Millisecond, time.Duration(timeoutMs)*time.Millisecond)
	s.buf.Mark()
	return pty.StripANSI(output), isComplete
}

func (s *SSHSession) IsAlive() bool {
	return s.alive.Load()
}

func (s *SSHSession) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.alive.Store(false)
		if s.stdin != nil {
			s.stdin.Close()
		}
		if s.session != nil {
			s.session.Close()
		}
		if s.client != nil {
			closeErr = s.client.Close()
		}
	})
	return closeErr
}
