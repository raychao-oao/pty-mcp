package mcp

import (
	"context"
	"encoding/json"
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
// did not ask for, so initialize must echo a supported request.
func TestInitializeEchoesClientVersion(t *testing.T) {
	req := &request{
		Method: "initialize",
		Params: json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}`),
	}
	resp := handle(context.Background(), nil, req)
	res, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", resp.Result)
	}
	if got := res["protocolVersion"]; got != "2025-06-18" {
		t.Fatalf("protocolVersion = %v, want 2025-06-18", got)
	}
}
