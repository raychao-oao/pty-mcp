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

// RunClient 橋接 stdin/stdout ↔ Unix socket
// 如果 server 沒在跑，自動啟動
func RunClient(socketPath string) error {
	// 確保 server 在跑
	if err := ensureServer(socketPath); err != nil {
		return err
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("connect to ai-tmux server: %w", err)
	}
	defer conn.Close()

	// stdin → socket
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		for scanner.Scan() {
			conn.Write(scanner.Bytes())
			conn.Write([]byte("\n"))
		}
		conn.Close()
	}()

	// socket → stdout
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		os.Stdout.Write(scanner.Bytes())
		os.Stdout.Write([]byte("\n"))
	}

	return nil
}

// ensureServer 檢查 server 是否在跑，沒有就啟動
func ensureServer(socketPath string) error {
	// 嘗試連線
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil // server 已在跑
	}

	// 啟動 server daemon
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	cmd := exec.Command(self, "server", "--socket", socketPath, "--idle-timeout", "1800")
	cmd.Stdout = nil
	cmd.Stderr = nil
	// 用新 process group 讓 daemon 獨立存活
	cmd.SysProcAttr = newDaemonAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	cmd.Process.Release() // detach

	// 等 server 啟動
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

// ListSessions 快速列出 sessions（給 CLI 用）
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
