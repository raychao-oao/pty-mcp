// internal/session/remote_test.go
package session_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raychao-oao/pty-mcp/internal/aitx"
	"github.com/raychao-oao/pty-mcp/internal/session"
)

// fakeServer creates in-process pipes that simulate the SSH stdin/stdout channel
// RemoteSession communicates over. handler is called once per incoming request.
func fakeServer(t *testing.T, handler func(aitx.Request) aitx.Response) (stdin io.Writer, stdout io.Reader) {
	t.Helper()
	srvRead, cliWrite := io.Pipe()
	cliRead, srvWrite := io.Pipe()

	go func() {
		defer srvWrite.Close()
		sc := bufio.NewScanner(srvRead)
		sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		enc := json.NewEncoder(srvWrite)
		for sc.Scan() {
			var req aitx.Request
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
				return
			}
			enc.Encode(handler(req))
		}
	}()

	t.Cleanup(func() {
		cliWrite.Close()
		srvRead.Close()
	})

	return cliWrite, cliRead
}

// attachSession creates a RemoteSession via AttachRemoteSession, automatically
// handling the initial read_output probe before forwarding to handler.
func attachSession(t *testing.T, handler func(aitx.Request) aitx.Response) *session.RemoteSession {
	t.Helper()
	probed := false
	stdin, stdout := fakeServer(t, func(req aitx.Request) aitx.Response {
		if req.Method == "read_output" && !probed {
			probed = true
			return aitx.Response{ID: req.ID, Result: aitx.OutputResult{IsAlive: true, IsComplete: true}}
		}
		return handler(req)
	})
	rs, err := session.AttachRemoteSession("t", "target", stdin, stdout, "sess-1")
	if err != nil {
		t.Fatalf("AttachRemoteSession: %v", err)
	}
	return rs
}

// TestRemoteSession_WriteRaw_CachesOutput verifies that the OutputResult returned
// by send_control is cached so that the subsequent ReadScreen returns it without
// issuing an extra read_output RPC.
func TestRemoteSession_WriteRaw_CachesOutput(t *testing.T) {
	var mu sync.Mutex
	var calls []string

	rs := attachSession(t, func(req aitx.Request) aitx.Response {
		mu.Lock()
		calls = append(calls, req.Method)
		mu.Unlock()
		if req.Method == "send_control" {
			return aitx.Response{ID: req.ID, Result: aitx.OutputResult{
				Output: "^C\n$ ", IsAlive: true, IsComplete: true,
			}}
		}
		return aitx.Response{ID: req.ID, Error: "unexpected method: " + req.Method}
	})

	if err := rs.WriteRaw("\x03"); err != nil { // ctrl+c
		t.Fatalf("WriteRaw: %v", err)
	}

	// ReadScreen must drain the cache — no extra read_output RPC.
	out, isComplete := rs.ReadScreen(1000)
	if out != "^C\n$ " {
		t.Errorf("ReadScreen output: want %q, got %q", "^C\n$ ", out)
	}
	if !isComplete {
		t.Error("ReadScreen: want isComplete=true")
	}

	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "send_control" {
		t.Errorf("RPC calls: want [send_control], got %v", got)
	}
}

// TestRemoteSession_Write_CachesOutput verifies that the OutputResult returned by
// send_input is cached so ReadScreen returns it without an extra read_output RPC.
func TestRemoteSession_Write_CachesOutput(t *testing.T) {
	var mu sync.Mutex
	var calls []string

	rs := attachSession(t, func(req aitx.Request) aitx.Response {
		mu.Lock()
		calls = append(calls, req.Method)
		mu.Unlock()
		if req.Method == "send_input" {
			return aitx.Response{ID: req.ID, Result: aitx.OutputResult{
				Output: "hello\n$ ", IsAlive: true, IsComplete: true,
			}}
		}
		return aitx.Response{ID: req.ID, Error: "unexpected method: " + req.Method}
	})

	if err := rs.Write("echo hello"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	out, _ := rs.ReadScreen(1000)
	if out != "hello\n$ " {
		t.Errorf("ReadScreen output: want %q, got %q", "hello\n$ ", out)
	}

	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "send_input" {
		t.Errorf("RPC calls: want [send_input], got %v", got)
	}
}

// TestRemoteSession_ReadScreen_FallsBackToRPC verifies that ReadScreen issues a
// read_output RPC when there is no cached output from a prior Write/WriteRaw.
func TestRemoteSession_ReadScreen_FallsBackToRPC(t *testing.T) {
	var mu sync.Mutex
	var calls []string

	rs := attachSession(t, func(req aitx.Request) aitx.Response {
		mu.Lock()
		calls = append(calls, req.Method)
		mu.Unlock()
		return aitx.Response{ID: req.ID, Result: aitx.OutputResult{
			Output: "fresh\n$ ", IsAlive: true, IsComplete: true,
		}}
	})

	out, _ := rs.ReadScreen(1000)
	if out != "fresh\n$ " {
		t.Errorf("ReadScreen output: want %q, got %q", "fresh\n$ ", out)
	}

	mu.Lock()
	got := append([]string(nil), calls...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "read_output" {
		t.Errorf("RPC calls: want [read_output], got %v", got)
	}
}

// TestRemoteSession_PollRemote_FeedsBuffer verifies that PollRemote continuously
// calls ReadScreen and feeds output into the local buffer — the mechanism that
// makes wait_for work for remote sessions.
func TestRemoteSession_PollRemote_FeedsBuffer(t *testing.T) {
	rs := attachSession(t, func(req aitx.Request) aitx.Response {
		return aitx.Response{ID: req.ID, Result: aitx.OutputResult{
			Output: "poll-chunk", IsAlive: true, IsComplete: false,
		}}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go rs.PollRemote(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rs.Buffer().String(), "poll-chunk") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("PollRemote did not feed localBuf within 2 s")
}
