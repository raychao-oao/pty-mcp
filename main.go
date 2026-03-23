package main

import (
	"github.com/raychao-oao/pty-mcp/internal/mcp"
	"github.com/raychao-oao/pty-mcp/internal/session"
)

func main() {
	mgr := session.NewManager(1800) // 30 min idle timeout
	handler := mcp.NewHandler(mgr)
	mcp.Serve(handler)
}
