package audit_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/raychao-oao/pty-mcp/internal/audit"
)

// captureServer returns a test HTTP server and a channel that receives each
// JSON body posted to /audit.
func captureServer(t *testing.T, token string) (*httptest.Server, <-chan []byte) {
	t.Helper()
	ch := make(chan []byte, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		ch <- body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

func TestSendCredential_BundleGenerated(t *testing.T) {
	srv, ch := captureServer(t, "tok123")
	c := audit.NewClient(audit.Config{URL: srv.URL, User: "ray", Token: "tok123", Mode: "best-effort"})
	defer c.Close()

	entry := audit.CredentialEntry{
		TS:           time.Now().UTC().Format(time.RFC3339Nano),
		User:         "ray",
		Event:        "bundle_generated",
		ConsumerID:   "pty-mcp",
		SessionKeyID: "abc123def456",
		ExpiresAt:    time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339Nano),
	}
	c.SendCredential(entry)

	// Wait for the entry to arrive (best-effort, async)
	select {
	case body := <-ch:
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
		}
		if m["event"] != "bundle_generated" {
			t.Errorf("event = %q, want bundle_generated", m["event"])
		}
		if m["user"] != "ray" {
			t.Errorf("user = %q, want ray", m["user"])
		}
		if m["session_key_id"] != "abc123def456" {
			t.Errorf("session_key_id = %q, want abc123def456", m["session_key_id"])
		}
		// must not contain any secret fields
		body_s := string(body)
		for _, forbidden := range []string{"plaintext", "ciphertext", "encapped_key", "private_key", "seed"} {
			if strings.Contains(body_s, forbidden) {
				t.Errorf("audit entry contains forbidden field %q: %s", forbidden, body_s)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for audit entry")
	}
}

func TestSendCredential_SecretInjected(t *testing.T) {
	srv, ch := captureServer(t, "tok123")
	c := audit.NewClient(audit.Config{URL: srv.URL, User: "ray", Token: "tok123", Mode: "best-effort"})
	defer c.Close()

	entry := audit.CredentialEntry{
		TS:           time.Now().UTC().Format(time.RFC3339Nano),
		User:         "ray",
		Event:        "secret_injected",
		ConsumerID:   "pty-mcp",
		SessionKeyID: "abc123def456",
		PtySessionID: "sess-xyz",
		ItemID:       "item-42",
		Purpose:      "ssh-login",
	}
	c.SendCredential(entry)

	select {
	case body := <-ch:
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if m["event"] != "secret_injected" {
			t.Errorf("event = %q, want secret_injected", m["event"])
		}
		if m["item_id"] != "item-42" {
			t.Errorf("item_id = %q, want item-42", m["item_id"])
		}
		if m["purpose"] != "ssh-login" {
			t.Errorf("purpose = %q, want ssh-login", m["purpose"])
		}
		if m["pty_session_id"] != "sess-xyz" {
			t.Errorf("pty_session_id = %q, want sess-xyz", m["pty_session_id"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for audit entry")
	}
}

func TestSendCredential_InjectFailed(t *testing.T) {
	srv, ch := captureServer(t, "tok123")
	c := audit.NewClient(audit.Config{URL: srv.URL, User: "ray", Token: "tok123", Mode: "best-effort"})
	defer c.Close()

	entry := audit.CredentialEntry{
		TS:           time.Now().UTC().Format(time.RFC3339Nano),
		User:         "ray",
		Event:        "inject_failed",
		ConsumerID:   "pty-mcp",
		SessionKeyID: "nonexistent",
		PtySessionID: "sess-xyz",
		Error:        "no credential found for session_id",
	}
	c.SendCredential(entry)

	select {
	case body := <-ch:
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if m["event"] != "inject_failed" {
			t.Errorf("event = %q, want inject_failed", m["event"])
		}
		if m["error"] == "" {
			t.Error("expected non-empty error field")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for audit entry")
	}
}

func TestSendCredential_NoopWhenNoURL(t *testing.T) {
	// Should not panic when URL is empty
	c := audit.NewClient(audit.Config{User: "ray"})
	defer c.Close()
	c.SendCredential(audit.CredentialEntry{Event: "bundle_generated"}) // must not panic
}
