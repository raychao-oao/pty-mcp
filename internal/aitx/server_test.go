// internal/aitx/server_test.go
package aitx_test

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/raychao-oao/pty-mcp/internal/aitx"
)

func TestServer_ListEmpty(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "ai-tmux.sock")

	errCh := make(chan error, 1)
	go func() {
		errCh <- aitx.RunServer(sock, 300)
	}()

	// Poll until the socket is ready, failing fast if RunServer errors.
	deadline := time.Now().Add(3 * time.Second)
	var conn net.Conn
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("RunServer failed to start: %v", err)
		default:
		}
		c, err := net.Dial("unix", sock)
		if err == nil {
			conn = c
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conn == nil {
		t.Fatal("server did not become ready within 3 s")
	}
	defer conn.Close()

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
