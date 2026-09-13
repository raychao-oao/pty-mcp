package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNegotiateProtocolVersion(t *testing.T) {
	cases := []struct{ requested, want string }{
		{"2025-11-25", "2025-11-25"},
		{"2025-06-18", "2025-06-18"},
		{"2025-03-26", "2025-03-26"},
		{"2024-11-05", "2024-11-05"},
		{"2024-10-07", "2024-10-07"},
		{"1999-01-01", serverProtocolVersion}, // unknown -> server's newest
		{"", serverProtocolVersion},           // absent -> server's newest
	}
	for _, c := range cases {
		params, _ := json.Marshal(map[string]any{"protocolVersion": c.requested})
		if got := negotiateProtocolVersion(params); got != c.want {
			t.Errorf("requested %q: got %q, want %q", c.requested, got, c.want)
		}
	}
}

// A strict client (e.g. the MCP TypeScript SDK) rejects any protocolVersion it
// did not ask for, so initialize over HTTP must echo a supported request.
func TestHTTPInitializeEchoesClientVersion(t *testing.T) {
	srv := newHTTPTestServer(t, "")
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	resp, out := doReq(t, http.MethodPost, srv.URL, body,
		map[string]string{"Accept": "application/json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, out)
	}
	if !strings.Contains(out, `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("expected echoed protocolVersion 2025-06-18, got %s", out)
	}
}
