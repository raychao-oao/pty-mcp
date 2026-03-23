// internal/session/ssh.go
package session

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

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
	id      string
	client  *gossh.Client
	session *gossh.Session
	stdin   io.WriteCloser
	buf     lockedBuffer
	alive   bool
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

func NewSSHSession(cfg SSHConfig) (*SSHSession, error) {
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
		alive:   true,
	}

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

	pty.WaitForSettle(func() string {
		return s.buf.String()
	}, 300*time.Millisecond, 3*time.Second)

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

func (s *SSHSession) ID() string   { return s.id }
func (s *SSHSession) Type() string { return "ssh" }

func (s *SSHSession) Write(input string) error {
	if !s.alive {
		return fmt.Errorf("session is not alive")
	}
	s.buf.Mark()
	_, err := fmt.Fprint(s.stdin, input+"\n")
	return err
}

func (s *SSHSession) WriteRaw(data string) error {
	if !s.alive {
		return fmt.Errorf("session is not alive")
	}
	s.buf.Mark()
	_, err := fmt.Fprint(s.stdin, data)
	return err
}

func (s *SSHSession) ReadScreen() string {
	output := pty.WaitForSettle(func() string {
		return s.buf.Since()
	}, 300*time.Millisecond, 5*time.Second)
	return pty.StripANSI(output)
}

func (s *SSHSession) IsAlive() bool {
	return s.alive
}

func (s *SSHSession) Close() error {
	s.alive = false
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.session != nil {
		s.session.Close()
	}
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}
