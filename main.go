package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"

	"github.com/raychao-oao/pty-mcp/internal/audit"
	"github.com/raychao-oao/pty-mcp/internal/config"
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
	auditURL  := fs.String("audit-url", "", "Audit collector URL (overrides config file)")
	auditUser := fs.String("audit-user", "", "Operator identity for audit log (overrides config file)")
	auditMode := fs.String("audit-mode", "", "Audit mode: best-effort or strict (overrides config file)")
	showVer   := fs.Bool("version", false, "Show version")

	fs.Usage = func() {
		fmt.Println("pty-mcp - Interactive PTY sessions for AI agents")
		fmt.Printf("Version: %s\n\n", version)
		fmt.Println("Usage:")
		fmt.Println("  pty-mcp [options]              Run MCP server (stdio)")
		fmt.Println("  pty-mcp audit init             Create config file and generate token")
		fmt.Println("  pty-mcp audit serve [options]  Run audit log collector")
		fmt.Println()
		fmt.Println("MCP server options (all optional; config file takes effect when omitted):")
		fs.PrintDefaults()
		fmt.Println()
		fmt.Println("Config file (audit settings):")
		fmt.Println("  ~/.config/pty-mcp/config   (primary, must be chmod 600)")
		fmt.Println("  ~/.pty-mcp                 (legacy fallback)")
		fmt.Println()
		fmt.Println("Config file format:")
		fmt.Println("  audit-url=http://localhost:8080")
		fmt.Println("  audit-user=ray")
		fmt.Println("  audit-mode=best-effort")
		fmt.Println("  audit-token=<shared-secret>")
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

	// Resolve audit config: CLI flags take priority over config file.
	auditCfg, err := resolveAuditConfig(*auditURL, *auditUser, *auditMode)
	if err != nil {
		log.Fatalf("pty-mcp: %v", err)
	}

	var auditClient *audit.Client
	if auditCfg != nil {
		auditClient = audit.NewClient(audit.Config{
			URL:   auditCfg.URL,
			User:  auditCfg.User,
			Token: auditCfg.Token,
			Mode:  auditCfg.Mode,
		})
		defer auditClient.Close()
	}

	mgr := session.NewManager(1800) // 30 min idle timeout
	handler := mcp.NewHandler(mgr, auditClient)
	mcp.Serve(handler)
}

// resolveAuditConfig returns the effective audit config.
// CLI flags take priority; if --audit-url is not set, the config file is used.
func resolveAuditConfig(flagURL, flagUser, flagMode string) (*config.Audit, error) {
	if flagURL != "" {
		// CLI flags provided — use them directly (token from env var or config file).
		fileCfg, _ := config.LoadAudit()
		token := ""
		if fileCfg != nil {
			token = fileCfg.Token
		}
		if env := os.Getenv("PTY_MCP_AUDIT_TOKEN"); env != "" {
			token = env // env var overrides file token
		}
		mode := flagMode
		if mode == "" {
			mode = "best-effort"
		}
		return &config.Audit{
			URL:   flagURL,
			User:  flagUser,
			Mode:  mode,
			Token: token,
		}, nil
	}
	// No CLI flag — try config file.
	return config.LoadAudit()
}

func runAudit(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  pty-mcp audit init             Create config file and generate token")
		fmt.Fprintln(os.Stderr, "  pty-mcp audit serve --port PORT --log FILE  Run audit log collector")
		os.Exit(1)
	}

	switch args[0] {
	case "init":
		runAuditInit(args[1:])
	case "serve":
		runAuditServe(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown audit subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func runAuditInit(args []string) {
	fs := flag.NewFlagSet("pty-mcp audit init", flag.ExitOnError)
	force := fs.Bool("force", false, "Overwrite existing config")
	fs.Parse(args) //nolint:errcheck

	configBase, err := os.UserConfigDir()
	if err != nil {
		log.Fatalf("audit init: %v", err)
	}
	dir := filepath.Join(configBase, "pty-mcp")
	cfgPath := filepath.Join(dir, "config")

	if !*force {
		if _, err := os.Stat(cfgPath); err == nil {
			fmt.Fprintf(os.Stderr, "Config already exists: %s\n", cfgPath)
			fmt.Fprintf(os.Stderr, "Use --force to overwrite.\n")
			os.Exit(1)
		}
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Fatalf("audit init: create directory: %v", err)
	}

	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		log.Fatalf("audit init: generate token: %v", err)
	}
	token := hex.EncodeToString(b[:])

	username := os.Getenv("USER")
	if u, err := user.Current(); err == nil && u.Username != "" {
		username = u.Username
	}

	content := "# pty-mcp audit configuration\n" +
		"# File must be chmod 600\n" +
		"\n" +
		"# Collector URL — uncomment and set to enable audit\n" +
		"# audit-url=http://your-collector:9099\n" +
		"\n" +
		"# Your identity in the audit log\n" +
		"audit-user=" + username + "\n" +
		"\n" +
		"# best-effort: log if possible, don't block on failure\n" +
		"# strict: reject send_input if audit entry cannot be delivered\n" +
		"audit-mode=best-effort\n" +
		"\n" +
		"# Shared token — keep this secret, share only with the collector admin\n" +
		"audit-token=" + token + "\n"

	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		log.Fatalf("audit init: write config: %v", err)
	}

	fmt.Printf("Created: %s\n\n", cfgPath)
	fmt.Printf("Next steps:\n\n")
	fmt.Printf("  1. Edit the config and uncomment audit-url:\n")
	fmt.Printf("       %s\n\n", cfgPath)
	fmt.Printf("  2. Share this token with whoever runs the collector:\n")
	fmt.Printf("       %s\n\n", token)
	fmt.Printf("  3. The collector admin starts the server with:\n")
	fmt.Printf("       PTY_MCP_AUDIT_TOKEN=%s \\\n", token)
	fmt.Printf("         pty-mcp audit serve --port 9099 --log /var/log/pty-mcp-audit.jsonl\n\n")
	fmt.Printf("Restart Claude Code to apply the new config.\n")
}

func runAuditServe(args []string) {
	fs := flag.NewFlagSet("pty-mcp audit serve", flag.ExitOnError)
	port    := fs.String("port", "8080", "Port to listen on")
	logPath := fs.String("log", "", "Path to JSONL audit log file (required)")
	fs.Parse(args) //nolint:errcheck

	if *logPath == "" {
		fmt.Fprintln(os.Stderr, "error: --log is required")
		os.Exit(1)
	}

	// Token: config file first, env var fallback.
	token := ""
	if fileCfg, err := config.LoadAudit(); err != nil {
		log.Fatalf("pty-mcp: %v", err)
	} else if fileCfg != nil {
		token = fileCfg.Token
	}
	if env := os.Getenv("PTY_MCP_AUDIT_TOKEN"); env != "" {
		token = env
	}

	srv, err := audit.NewServer(token, *logPath)
	if err != nil {
		log.Fatalf("audit server: %v", err)
	}
	defer srv.Close()

	if err := srv.Serve(":" + *port); err != nil {
		log.Fatalf("audit server: %v", err)
	}
}
