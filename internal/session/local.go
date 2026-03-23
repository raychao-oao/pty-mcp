// internal/session/local.go
package session

import (
	"github.com/raychao-oao/pty-mcp/internal/aitx"
)

// LocalSession wraps aitx.PTYSession to implement the Session interface for a local interactive terminal
type LocalSession struct {
	id  string
	pty *aitx.PTYSession
}

func NewLocalSession(command string) (*LocalSession, error) {
	id := NewID()
	pty, err := aitx.NewPTYSession(id, command, command)
	if err != nil {
		return nil, err
	}
	return &LocalSession{id: id, pty: pty}, nil
}

func (s *LocalSession) ID() string   { return s.id }
func (s *LocalSession) Type() string { return "local" }

func (s *LocalSession) Write(input string) error {
	return s.pty.Write(input)
}

func (s *LocalSession) WriteRaw(data string) error {
	return s.pty.WriteRaw(data)
}

func (s *LocalSession) ReadScreen(timeoutMs int) (string, bool) {
	return s.pty.ReadScreen(timeoutMs)
}

func (s *LocalSession) IsAlive() bool {
	return s.pty.IsAlive()
}

func (s *LocalSession) Close() error {
	return s.pty.Close()
}
