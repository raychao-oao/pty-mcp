// cmd/ai-tmux/main.go
package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/raychao-oao/pty-mcp/internal/aitx"
)

var version = "dev"

func main() {
	log.SetOutput(os.Stderr)

	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Printf("ai-tmux %s\n", version)
			return
		}
	}

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: ai-tmux <server|client|list> [options]\n")
		os.Exit(1)
	}

	socketPath := aitx.SocketPath
	// simple flag parsing
	for i, arg := range os.Args {
		if arg == "--socket" && i+1 < len(os.Args) {
			socketPath = os.Args[i+1]
		}
	}

	switch os.Args[1] {
	case "server":
		idleTimeout := 1800
		for i, arg := range os.Args {
			if arg == "--idle-timeout" && i+1 < len(os.Args) {
				if v, err := strconv.Atoi(os.Args[i+1]); err == nil {
					idleTimeout = v
				}
			}
		}
		if err := aitx.RunServer(socketPath, idleTimeout); err != nil {
			log.Fatalf("server error: %v", err)
		}
	case "client":
		if err := aitx.RunClient(socketPath); err != nil {
			log.Fatalf("client error: %v", err)
		}
	case "list":
		if err := aitx.ListSessions(socketPath); err != nil {
			log.Fatalf("list error: %v", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
