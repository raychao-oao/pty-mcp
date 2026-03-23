package main

import (
	"pty-mcp/internal/mcp"
	"pty-mcp/internal/session"
)

func main() {
	mgr := session.NewManager(1800) // 30 min idle timeout
	handler := mcp.NewHandler(mgr)
	mcp.Serve(handler)
}
