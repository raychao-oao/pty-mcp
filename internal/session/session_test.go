package session_test

import (
	"testing"
	"pty-mcp/internal/session"
)

func TestSessionManager_Empty(t *testing.T) {
	mgr := session.NewManager(5 * 60)
	if len(mgr.List()) != 0 {
		t.Fatal("expected empty session list")
	}
}

func TestSessionManager_GetNonExistent(t *testing.T) {
	mgr := session.NewManager(300)
	_, err := mgr.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
}
