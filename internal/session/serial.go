// internal/session/serial.go
package session

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.bug.st/serial"
	"github.com/raychao-oao/pty-mcp/internal/buffer"
	"github.com/raychao-oao/pty-mcp/internal/pty"
)

type SerialSession struct {
	id        string
	port      serial.Port
	buf       *buffer.RingBuffer
	alive     atomic.Bool
	done      chan struct{}
	closeOnce sync.Once
}

func isValidSerialDevice(device string) bool {
	if strings.Contains(device, "..") {
		return false
	}
	return strings.HasPrefix(device, "/dev/tty") || strings.HasPrefix(device, "/dev/cu.")
}

func NewSerialSession(device string, baudRate int) (*SerialSession, error) {
	if !isValidSerialDevice(device) {
		return nil, fmt.Errorf("invalid serial device %q: must start with /dev/tty or /dev/cu. (e.g. /dev/ttyUSB0, /dev/cu.usbserial-XXXX)", device)
	}
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
		id:   NewID(),
		port: port,
		buf:  buffer.NewRingBuffer(buffer.BufferSizeFromEnv()),
		done: make(chan struct{}),
	}
	s.alive.Store(true)

	go s.readLoop()

	pty.WaitForSettle(func() string {
		return s.buf.String()
	}, 300*time.Millisecond, 2*time.Second) // wait for initial output, ignore isComplete

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
				s.alive.Store(false)
				log.Printf("[pty-mcp] serial read error: %v", err)
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
	if !s.alive.Load() {
		return fmt.Errorf("session is not alive")
	}
	s.buf.Mark()
	_, err := s.port.Write([]byte(input + "\r\n"))
	return err
}

func (s *SerialSession) WriteRaw(data string) error {
	if !s.alive.Load() {
		return fmt.Errorf("session is not alive")
	}
	s.buf.Mark()
	_, err := s.port.Write([]byte(data))
	return err
}

func (s *SerialSession) ReadScreen(timeoutMs int) (string, bool) {
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	output, isComplete := pty.WaitForSettle(func() string {
		return s.buf.Since()
	}, 300*time.Millisecond, time.Duration(timeoutMs)*time.Millisecond)
	s.buf.AdvanceMarkBy(int64(len(output)))
	return pty.StripANSI(output), isComplete
}

func (s *SerialSession) IsAlive() bool {
	return s.alive.Load()
}

func (s *SerialSession) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.alive.Store(false)
		close(s.done)
		closeErr = s.port.Close()
	})
	return closeErr
}

func (s *SerialSession) Buffer() *buffer.RingBuffer { return s.buf }
func (s *SerialSession) PollRemote(_ context.Context) {} // no-op for serial
