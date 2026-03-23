// internal/session/serial.go
package session

import (
	"fmt"
	"time"

	"go.bug.st/serial"
	"pty-mcp/internal/pty"
)

type SerialSession struct {
	id    string
	port  serial.Port
	buf   lockedBuffer
	alive bool
	done  chan struct{}
}

func NewSerialSession(device string, baudRate int) (*SerialSession, error) {
	if baudRate == 0 {
		baudRate = 9600
	}
	mode := &serial.Mode{
		BaudRate: baudRate,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}

	port, err := serial.Open(device, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial %s: %w", device, err)
	}

	s := &SerialSession{
		id:    NewID(),
		port:  port,
		alive: true,
		done:  make(chan struct{}),
	}

	go s.readLoop()

	pty.WaitForSettle(func() string {
		return s.buf.String()
	}, 300*time.Millisecond, 2*time.Second)

	return s, nil
}

func (s *SerialSession) readLoop() {
	tmp := make([]byte, 1024)
	for {
		select {
		case <-s.done:
			return
		default:
			n, err := s.port.Read(tmp)
			if err != nil {
				s.alive = false
				return
			}
			if n > 0 {
				s.buf.Write(tmp[:n])
			}
		}
	}
}

func (s *SerialSession) ID() string   { return s.id }
func (s *SerialSession) Type() string { return "serial" }

func (s *SerialSession) Write(input string) error {
	if !s.alive {
		return fmt.Errorf("session is not alive")
	}
	s.buf.Mark()
	_, err := s.port.Write([]byte(input + "\r\n"))
	return err
}

func (s *SerialSession) WriteRaw(data string) error {
	if !s.alive {
		return fmt.Errorf("session is not alive")
	}
	s.buf.Mark()
	_, err := s.port.Write([]byte(data))
	return err
}

func (s *SerialSession) ReadScreen() string {
	output := pty.WaitForSettle(func() string {
		return s.buf.Since()
	}, 300*time.Millisecond, 5*time.Second)
	return pty.StripANSI(output)
}

func (s *SerialSession) IsAlive() bool {
	return s.alive
}

func (s *SerialSession) Close() error {
	s.alive = false
	close(s.done)
	return s.port.Close()
}
