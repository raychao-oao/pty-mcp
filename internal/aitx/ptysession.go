// internal/aitx/ptysession.go
package aitx

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	logFile   io.WriteCloser
	readDone  chan struct{} // closed when the read goroutine exits
	alive     atomic.Bool
	closeOnce sync.Once
	createdAt time.Time
	lastUsed  atomic.Value // time.Time
}

func NewPTYSession(id, name, command string) (*PTYSession, error) {
	return newPTYSession(id, name, command, nil)
}

func NewPTYSessionWithLog(id, name, command string, logFile io.WriteCloser) (*PTYSession, error) {
	return newPTYSession(id, name, command, logFile)
}

func isPowerShell(command string) bool {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(command), ".exe"))
	return base == "pwsh" || base == "powershell"
}

func newPTYSession(id, name, command string, logFile io.WriteCloser) (*PTYSession, error) {
	if command == "" {
		command = "/bin/bash"
	}
	if name == "" {
		name = command
	}

	var cmd *exec.Cmd
	if isPowerShell(command) {
		// -NoLogo -NoProfile: skip startup banner and user profile scripts.
		// Profile scripts are the primary source of PSReadLine, oh-my-posh, and OSC 133/633
		// shell-integration hooks that inject garbage escape sequences into the PTY buffer.
		// Interactive features (Read-Host, browser OAuth) still work without a profile.
		cmd = exec.Command(command, "-NoLogo", "-NoProfile")
		// TERM=dumb + NO_COLOR stop .NET/PSStyle from emitting color/cursor sequences.
		// DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_CONTROL_CODES=0 stops the \x1b[6n DSR probe that
		// causes the outer terminal to write cursor-position replies into the PTY stdin.
		// TERM_PROGRAM= (empty) prevents profile integrations that key off Apple_Terminal/vscode.
		// CLICOLOR=0 disables BSD-style color for native tools called from pwsh.
		cmd.Env = append(os.Environ(),
			"TERM=dumb",
			"NO_COLOR=1",
			"CLICOLOR=0",
			"TERM_PROGRAM=",
			"DOTNET_SYSTEM_CONSOLE_ALLOW_ANSI_CONTROL_CODES=0",
			"VSCODE_SHELL_INTEGRATION=0",
		)
	} else {
		cmd = exec.Command(command)
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	}

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

	// dsrQuery is sent by PSReadLine / .NET Console before each readline to learn
	// the cursor position. Our Go process never displays anything, so the outer
	// terminal's response never reaches the PTY. With no answer, PSReadLine
	// computes garbage cursor offsets and produces corrupted VT sequences.
	// We intercept the query and reply with row=1,col=1 so PSReadLine renders cleanly.
	var (
		dsrQuery    = []byte("\x1b[6n")
		dsrResponse = []byte("\x1b[1;1R")
	)

	// read PTY output in background
	go func() {
		defer close(s.readDone)
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if n > 0 {
				chunk := tmp[:n]
				if bytes.Contains(chunk, dsrQuery) {
					ptmx.Write(dsrResponse)
					chunk = bytes.ReplaceAll(chunk, dsrQuery, nil)
				}
				if len(chunk) > 0 {
					s.writer.Write(chunk)
				}
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

	// Silence PSReadLine and PSStyle escape sequences.
	// Remove-Module PSReadLine: PSReadLine does character-by-character line editing using VT
	// sequences, polluting the buffer with redraw noise. The DSR intercept above now gives
	// .NET Console.ReadLine() valid cursor-position responses, so it works cleanly post-removal.
	if isPowerShell(command) {
		inject := []string{
			// Remove PSReadLine first so subsequent injections use plain Console.ReadLine().
			"Remove-Module PSReadLine -Force -ErrorAction SilentlyContinue\r",
			// Force plain-text output rendering (no ANSI color/bold escapes).
			"$PSStyle.OutputRendering = 'PlainText'\r",
			// Disable OSC progress indicator and switch to classic (non-VT) progress view.
			"$PSStyle.Progress.UseOSCIndicator = $false; $PSStyle.Progress.View = 'Classic'\r",
			// Install a minimal plain-text prompt to prevent OSC 133/633 shell-integration hooks.
			"function global:prompt { 'PS ' + (Get-Location) + '> ' }\r",
		}
		for _, line := range inject {
			ptmx.WriteString(line)
			ptyhelper.WaitForSettle(func() string {
				return s.buf.String()
			}, 300*time.Millisecond, 3*time.Second)
		}
		s.buf.Mark() // advance mark past all startup noise
	}

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
