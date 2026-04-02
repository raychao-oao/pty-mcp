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
	"github.com/raychao-oao/pty-mcp/internal/buffer"
	ptyhelper "github.com/raychao-oao/pty-mcp/internal/pty"
)

type PTYSession struct {
	id        string
	name      string
	command   string
	cmd       *exec.Cmd
	ptyFile   *os.File
	buf       *buffer.RingBuffer
	writer    io.Writer // = buf, or MultiWriter(buf, logFile)
	logFile   *os.File
	readDone  chan struct{} // closed when the read goroutine exits
	alive     atomic.Bool
	closeOnce sync.Once
	createdAt time.Time
	lastUsed  atomic.Value // time.Time
}

func NewPTYSession(id, name, command string) (*PTYSession, error) {
	return newPTYSession(id, name, command, nil)
}

func NewPTYSessionWithLog(id, name, command string, logFile *os.File) (*PTYSession, error) {
	return newPTYSession(id, name, command, logFile)
}

func newPTYSession(id, name, command string, logFile *os.File) (*PTYSession, error) {
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

	// set terminal size
	pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120})

	rb := buffer.NewRingBuffer(buffer.BufferSizeFromEnv())
	var w io.Writer = rb
	if logFile != nil {
		w = io.MultiWriter(rb, logFile)
	}

	s := &PTYSession{
		id:        id,
		name:      name,
		command:   command,
		cmd:       cmd,
		ptyFile:   ptmx,
		buf:       rb,
		writer:    w,
		logFile:   logFile,
		readDone:  make(chan struct{}),
		createdAt: time.Now(),
	}
	s.alive.Store(true)
	s.lastUsed.Store(time.Now())

	// read PTY output in background
	go func() {
		defer close(s.readDone)
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if n > 0 {
				s.writer.Write(tmp[:n])
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

	// detect process exit
	go func() {
		s.cmd.Wait()
		s.alive.Store(false)
	}()

	// wait for initial prompt
	ptyhelper.WaitForSettle(func() string {
		return s.buf.String()
	}, 300*time.Millisecond, 2*time.Second)

	return s, nil
}

func (s *PTYSession) ID() string      { return s.id }
func (s *PTYSession) Name() string    { return s.name }
func (s *PTYSession) Command() string { return s.command }
func (s *PTYSession) IsAlive() bool   { return s.alive.Load() }
func (s *PTYSession) CreatedAt() time.Time      { return s.createdAt }
func (s *PTYSession) LastUsed() time.Time        { return s.lastUsed.Load().(time.Time) }
func (s *PTYSession) Buffer() *buffer.RingBuffer { return s.buf }

func (s *PTYSession) Write(input string) error {
	if !s.alive.Load() {
		return fmt.Errorf("session is not alive")
	}
	s.buf.Mark()
	s.lastUsed.Store(time.Now())
	_, err := s.ptyFile.WriteString(input + "\r")
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
	s.buf.AdvanceMarkBy(int64(len(output)))
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
		if s.logFile != nil {
			<-s.readDone // wait for read goroutine to finish writing
			s.logFile.Close()
		}
	})
	return closeErr
}
