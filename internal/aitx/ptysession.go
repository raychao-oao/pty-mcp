// internal/aitx/ptysession.go
package aitx

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"
	ptyhelper "pty-mcp/internal/pty"
	"golang.org/x/sys/unix"
)

type PTYSession struct {
	id        string
	name      string
	command   string
	cmd       *exec.Cmd
	ptyFile   *os.File
	buf       safeBuffer
	alive     atomic.Bool
	closeOnce sync.Once
	createdAt time.Time
	lastUsed  atomic.Value // time.Time
}

// safeBuffer 線程安全 buffer（與 ssh.go 的 lockedBuffer 相同概念）
type safeBuffer struct {
	mu       sync.Mutex
	data     []byte
	snapshot int
}

func (sb *safeBuffer) Write(p []byte) (int, error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.data = append(sb.data, p...)
	return len(p), nil
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return string(sb.data)
}

func (sb *safeBuffer) Since() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	if sb.snapshot >= len(sb.data) {
		return ""
	}
	return string(sb.data[sb.snapshot:])
}

func (sb *safeBuffer) Mark() {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.snapshot = len(sb.data)
}

func NewPTYSession(id, name, command string) (*PTYSession, error) {
	if command == "" {
		command = "/bin/bash"
	}
	if name == "" {
		name = command
	}

	cmd := exec.Command(command)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start pty %q: %w", command, err)
	}

	// 設定終端大小
	pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120})

	// 關閉 PTY echo，避免 readline 程式（python, node）產生逐字回顯雜訊
	if attr, err := unix.IoctlGetTermios(int(ptmx.Fd()), unix.TIOCGETA); err == nil {
		attr.Lflag &^= unix.ECHO
		unix.IoctlSetTermios(int(ptmx.Fd()), unix.TIOCSETA, attr)
	}

	s := &PTYSession{
		id:        id,
		name:      name,
		command:   command,
		cmd:       cmd,
		ptyFile:   ptmx,
		createdAt: time.Now(),
	}
	s.alive.Store(true)
	s.lastUsed.Store(time.Now())

	// 背景讀取 PTY 輸出
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if n > 0 {
				s.buf.Write(tmp[:n])
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("[ai-tmux] pty read error for %s: %v", id, err)
				}
				s.alive.Store(false)
				return
			}
		}
	}()

	// 偵測 process 結束
	go func() {
		s.cmd.Wait()
		s.alive.Store(false)
	}()

	// 等初始 prompt
	ptyhelper.WaitForSettle(func() string {
		return s.buf.String()
	}, 300*time.Millisecond, 2*time.Second)

	return s, nil
}

func (s *PTYSession) ID() string      { return s.id }
func (s *PTYSession) Name() string    { return s.name }
func (s *PTYSession) Command() string { return s.command }
func (s *PTYSession) IsAlive() bool   { return s.alive.Load() }
func (s *PTYSession) CreatedAt() time.Time { return s.createdAt }
func (s *PTYSession) LastUsed() time.Time  { return s.lastUsed.Load().(time.Time) }

func (s *PTYSession) Write(input string) error {
	if !s.alive.Load() {
		return fmt.Errorf("session is not alive")
	}
	s.buf.Mark()
	s.lastUsed.Store(time.Now())
	_, err := s.ptyFile.WriteString(input + "\n")
	return err
}

func (s *PTYSession) WriteRaw(data string) error {
	if !s.alive.Load() {
		return fmt.Errorf("session is not alive")
	}
	s.buf.Mark()
	s.lastUsed.Store(time.Now())
	_, err := s.ptyFile.WriteString(data)
	return err
}

func (s *PTYSession) ReadScreen(timeoutMs int) (string, bool) {
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	s.lastUsed.Store(time.Now())
	output, isComplete := ptyhelper.WaitForSettle(func() string {
		return s.buf.Since()
	}, 300*time.Millisecond, time.Duration(timeoutMs)*time.Millisecond)
	s.buf.Mark()
	return ptyhelper.StripANSI(output), isComplete
}

func (s *PTYSession) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.alive.Store(false)
		if s.ptyFile != nil {
			s.ptyFile.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
	})
	return closeErr
}
