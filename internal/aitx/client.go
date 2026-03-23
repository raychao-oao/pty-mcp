// internal/aitx/client.go
package aitx

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"time"
)

// RunClient bridges stdin/stdout to a Unix socket.
// Automatically starts the server if it is not running.
func RunClient(socketPath string) error {
	// ensure the server is running
	if err := ensureServer(socketPath); err != nil {
		return err
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("connect to ai-tmux server: %w", err)
	}
	defer conn.Close()

	// forward stdin to socket
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		for scanner.Scan() {
			conn.Write(scanner.Bytes())
			conn.Write([]byte("\n"))
		}
		conn.Close()
	}()

	// forward socket to stdout
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		os.Stdout.Write(scanner.Bytes())
		os.Stdout.Write([]byte("\n"))
	}

	return nil
}

// ensureServer checks whether the server is running and starts it if not
func ensureServer(socketPath string) error {
	// try to connect
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil // server is already running
	}

	// start server daemon
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	cmd := exec.Command(self, "server", "--socket", socketPath, "--idle-timeout", "1800")
	cmd.Stdout = nil
	cmd.Stderr = nil
	// use a new process group so the daemon survives independently
	cmd.SysProcAttr = newDaemonAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	cmd.Process.Release() // detach

	// wait for server to start
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			log.Printf("[ai-tmux] server started (pid released)")
			return nil
		}
	}

	return fmt.Errorf("server failed to start within 2s")
}

// ListSessions quickly lists sessions (for CLI use)
func ListSessions(socketPath string) error {
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err != nil {
		return fmt.Errorf("server not running at %s", socketPath)
	}
	defer conn.Close()

	req := Request{ID: "list", Method: "list_sessions"}
	json.NewEncoder(conn).Encode(req)

	var resp Response
	json.NewDecoder(conn).Decode(&resp)

	if resp.Error != "" {
		return fmt.Errorf("error: %s", resp.Error)
	}

	b, _ := json.MarshalIndent(resp.Result, "", "  ")
	fmt.Println(string(b))
	return nil
}
