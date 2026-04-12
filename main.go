package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/raychao-oao/pty-mcp/internal/audit"
	"github.com/raychao-oao/pty-mcp/internal/mcp"
	"github.com/raychao-oao/pty-mcp/internal/session"
)

var version = "dev"

func main() {
	// "audit" subcommand
	if len(os.Args) >= 2 && os.Args[1] == "audit" {
		runAudit(os.Args[2:])
		return
	}

	fs := flag.NewFlagSet("pty-mcp", flag.ExitOnError)
	auditURL  := fs.String("audit-url", "", "Audit collector URL (e.g. http://localhost:8080)")
	auditUser := fs.String("audit-user", "", "Operator identity for audit log")
	auditMode := fs.String("audit-mode", "best-effort", "Audit mode: best-effort or strict")
	showVer   := fs.Bool("version", false, "Show version")

	fs.Usage = func() {
		fmt.Println("pty-mcp - Interactive PTY sessions for AI agents")
		fmt.Printf("Version: %s\n\n", version)
		fmt.Println("Usage:")
		fmt.Println("  pty-mcp [options]              Run MCP server (stdio)")
		fmt.Println("  pty-mcp audit serve [options]  Run audit log collector")
		fmt.Println()
		fmt.Println("MCP server options:")
		fs.PrintDefaults()
		fmt.Println()
		fmt.Println("Environment:")
		fmt.Println("  PTY_MCP_AUDIT_TOKEN   Shared secret for audit authentication")
	}

	// Support legacy -v / -h shortcuts before flag parse
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v":
			fmt.Printf("pty-mcp %s\n", version)
			return
		case "-h":
			fs.Usage()
			return
		}
	}

	fs.Parse(os.Args[1:]) //nolint:errcheck

	if *showVer {
		fmt.Printf("pty-mcp %s\n", version)
		return
	}

	// Build audit client if --audit-url is set
	var auditClient *audit.Client
	if *auditURL != "" {
		auditClient = audit.NewClient(audit.Config{
			URL:   *auditURL,
			User:  *auditUser,
			Token: os.Getenv("PTY_MCP_AUDIT_TOKEN"),
			Mode:  *auditMode,
		})
		defer auditClient.Close()
	}

	mgr := session.NewManager(1800) // 30 min idle timeout
	handler := mcp.NewHandler(mgr, auditClient)
	mcp.Serve(handler)
}

func runAudit(args []string) {
	if len(args) == 0 || args[0] != "serve" {
		fmt.Fprintln(os.Stderr, "Usage: pty-mcp audit serve --port PORT --log FILE")
		os.Exit(1)
	}

	fs := flag.NewFlagSet("pty-mcp audit serve", flag.ExitOnError)
	port    := fs.String("port", "8080", "Port to listen on")
	logPath := fs.String("log", "", "Path to JSONL audit log file (required)")
	fs.Parse(args[1:]) //nolint:errcheck

	if *logPath == "" {
		fmt.Fprintln(os.Stderr, "error: --log is required")
		os.Exit(1)
	}

	srv, err := audit.NewServer(os.Getenv("PTY_MCP_AUDIT_TOKEN"), *logPath)
	if err != nil {
		log.Fatalf("audit server: %v", err)
	}
	defer srv.Close()

	if err := srv.Serve(":" + *port); err != nil {
		log.Fatalf("audit server: %v", err)
	}
}
