package main

import (
	"fmt"
	"os"

	"github.com/raychao-oao/pty-mcp/internal/mcp"
	"github.com/raychao-oao/pty-mcp/internal/session"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Printf("pty-mcp %s\n", version)
			return
		case "--help", "-h":
			fmt.Println("pty-mcp - Interactive PTY sessions for AI agents")
			fmt.Println()
			fmt.Printf("Version: %s\n", version)
			fmt.Println()
			fmt.Println("Usage: pty-mcp [options]")
			fmt.Println()
			fmt.Println("Options:")
			fmt.Println("  --version, -v  Show version")
			fmt.Println("  --help, -h     Show this help")
			fmt.Println()
			fmt.Println("pty-mcp runs as an MCP server over stdio (JSON-RPC).")
			fmt.Println("Register with Claude Code:")
			fmt.Println("  claude mcp add pty-mcp -- /usr/local/bin/pty-mcp")
			return
		}
	}

	mgr := session.NewManager(1800) // 30 min idle timeout
	handler := mcp.NewHandler(mgr)
	mcp.Serve(handler)
}
