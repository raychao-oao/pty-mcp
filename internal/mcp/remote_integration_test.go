// internal/mcp/remote_integration_test.go
package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"testing"

	"github.com/raychao-oao/pty-mcp/internal/aitx"
	"github.com/raychao-oao/pty-mcp/internal/audit"
	"github.com/raychao-oao/pty-mcp/internal/session"
)

// fakeAiTmux creates in-process pipes simulating an ai-tmux server's stdin/stdout.
func fakeAiTmux(t *testing.T, handler func(aitx.Request) aitx.Response) (io.Writer, io.Reader) {
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

// TestSendInput_RemoteSession_WaitFor verifies that SendInput with wait_for set does NOT
// short-circuit through the RemoteSession fast path.  Instead it must fall through to the
// full wait_for logic (PollRemote + waitForPattern) and return a WaitForResult.
func TestSendInput_RemoteSession_WaitFor(t *testing.T) {
	probed := false
	inputSent := false

	stdin, stdout := fakeAiTmux(t, func(req aitx.Request) aitx.Response {
		switch req.Method {
		case "read_output":
			if !probed {
				// initial attach probe
				probed = true
				return aitx.Response{ID: req.ID, Result: aitx.OutputResult{IsAlive: true, IsComplete: true}}
			}
			// PollRemote keeps calling read_output; return the pattern once input was sent
			if inputSent {
				return aitx.Response{ID: req.ID, Result: aitx.OutputResult{
					Output: "server ready\n$ ", IsAlive: true, IsComplete: true,
				}}
			}
			return aitx.Response{ID: req.ID, Result: aitx.OutputResult{
				Output: "", IsAlive: true, IsComplete: false,
			}}
		case "send_input":
			inputSent = true
			// immediate response is empty; pattern arrives via PollRemote
			return aitx.Response{ID: req.ID, Result: aitx.OutputResult{
				Output: "", IsAlive: true, IsComplete: false,
			}}
		default:
			return aitx.Response{ID: req.ID, Error: "unexpected method: " + req.Method}
		}
	})

	rs, err := session.AttachRemoteSession("rs-1", "test-target", stdin, stdout, "remote-sess-1")
	if err != nil {
		t.Fatalf("AttachRemoteSession: %v", err)
	}

	mgr := session.NewManager(0) // no idle reaper
	if err := mgr.Add(rs, "test-target"); err != nil {
		t.Fatalf("mgr.Add: %v", err)
	}

	h := NewHandler(mgr, audit.NewClient(audit.Config{})) // no-op audit (empty URL)

	raw, _ := json.Marshal(SendInputParams{
		SessionID:      "rs-1",
		Input:          "start server",
		WaitFor:        "ready",
		WaitForTimeout: 5,
	})

	result, err := h.SendInput(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("SendInput: %v", err)
	}

	// The fast-path returns map[string]any; wait_for returns WaitForResult.
	// A wrong result type here means the early-return regression is back.
	wfr, ok := result.(WaitForResult)
	if !ok {
		t.Fatalf("expected WaitForResult, got %T — RemoteSession fast-path was not bypassed", result)
	}
	if !wfr.Matched {
		t.Errorf("expected matched=true, error: %s", wfr.Error)
	}
	if wfr.MatchLine != "server ready" {
		t.Errorf("match_line: want %q, got %q", "server ready", wfr.MatchLine)
	}
}

// TestSendControl_RemoteSession_OutputAvailable verifies that send_control output is
// cached so the MCP handler's ReadScreen call (in the response path) sees it.
func TestSendControl_RemoteSession_OutputAvailable(t *testing.T) {
	probed := false

	stdin, stdout := fakeAiTmux(t, func(req aitx.Request) aitx.Response {
		switch req.Method {
		case "read_output":
			if !probed {
				probed = true
				return aitx.Response{ID: req.ID, Result: aitx.OutputResult{IsAlive: true, IsComplete: true}}
			}
			return aitx.Response{ID: req.ID, Result: aitx.OutputResult{
				Output: "after-ctrl-c\n$ ", IsAlive: true, IsComplete: true,
			}}
		case "send_control":
			return aitx.Response{ID: req.ID, Result: aitx.OutputResult{
				Output: "^C\n$ ", IsAlive: true, IsComplete: true,
			}}
		default:
			return aitx.Response{ID: req.ID, Error: "unexpected method: " + req.Method}
		}
	})

	rs, err := session.AttachRemoteSession("rs-2", "test-target", stdin, stdout, "remote-sess-2")
	if err != nil {
		t.Fatalf("AttachRemoteSession: %v", err)
	}

	mgr := session.NewManager(0)
	if err := mgr.Add(rs, "test-target"); err != nil {
		t.Fatalf("mgr.Add: %v", err)
	}

	h := NewHandler(mgr, audit.NewClient(audit.Config{}))

	// send_control is issued by MCP via WriteRaw("\x03")
	raw, _ := json.Marshal(SendInputParams{
		SessionID: "rs-2",
		Input:     "\x03",
		Raw:       true,
	})

	result, err := h.SendInput(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("SendInput(ctrl+c): %v", err)
	}

	out, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if out["output"] != "^C\n$ " {
		t.Errorf("output: want %q, got %q", "^C\n$ ", out["output"])
	}
}
