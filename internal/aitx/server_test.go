// internal/aitx/server_test.go
package aitx_test

import (
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"pty-mcp/internal/aitx"
)

func TestServer_ListEmpty(t *testing.T) {
	sock := "/tmp/ai-tmux-test.sock"
	os.Remove(sock)
	defer os.Remove(sock)

	go aitx.RunServer(sock, 300) // 300s idle timeout

	// 等 server 啟動
	time.Sleep(200 * time.Millisecond)

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 送 list_sessions
	req := aitx.Request{ID: "t1", Method: "list_sessions"}
	json.NewEncoder(conn).Encode(req)

	var resp aitx.Response
	json.NewDecoder(conn).Decode(&resp)

	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.ID != "t1" {
		t.Fatalf("expected id t1, got %s", resp.ID)
	}
}
