package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strings"

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
		// --audit-url provided: use config file as base, override with any explicit flags.
		fileCfg, _ := config.LoadAudit()
		token, cfgUser, cfgMode := "", "", ""
		if fileCfg != nil {
			token, cfgUser, cfgMode = fileCfg.Token, fileCfg.User, fileCfg.Mode
		}
		if env := os.Getenv("PTY_MCP_AUDIT_TOKEN"); env != "" {
			token = env
		}
		if flagUser != "" {
			cfgUser = flagUser
		}
		if flagMode != "" {
			cfgMode = flagMode
		}
		if cfgMode == "" {
			cfgMode = "best-effort"
		}
		return &config.Audit{URL: flagURL, User: cfgUser, Mode: cfgMode, Token: token}, nil
	}
	// No CLI flag — use config file entirely.
	return config.LoadAudit()
}

func runAudit(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  pty-mcp audit init             Create config file and generate token")
		fmt.Fprintln(os.Stderr, "  pty-mcp audit enable           Enable audit (init if needed)")
		fmt.Fprintln(os.Stderr, "  pty-mcp audit disable          Disable audit (keeps config and token)")
		fmt.Fprintln(os.Stderr, "  pty-mcp audit serve --port PORT --log FILE  Run audit log collector")
		os.Exit(1)
	}

	switch args[0] {
	case "init":
		runAuditInit(args[1:])
	case "enable":
		runAuditEnable()
	case "disable":
		runAuditDisable()
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

func runAuditEnable() {
	cfgPath := config.PrimaryPath()
	if cfgPath == "" {
		log.Fatal("audit enable: cannot determine config directory")
	}

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		fmt.Println("No config found — running audit init first...")
		fmt.Println()
		runAuditInit([]string{})
		fmt.Println()
		fmt.Printf("Edit audit-url in %s then run 'pty-mcp audit enable' again.\n", cfgPath)
		return
	}

	changed, placeholder, err := toggleAuditURL(cfgPath, true)
	if err != nil {
		log.Fatalf("audit enable: %v", err)
	}
	if !changed {
		fmt.Println("Audit is already enabled (audit-url is set).")
		return
	}
	if placeholder {
		fmt.Printf("Warning: audit-url still contains the placeholder URL.\n")
		fmt.Printf("Edit %s and set the real collector URL.\n\n", cfgPath)
	}
	fmt.Println("Audit enabled. Restart Claude Code to apply.")
}

func runAuditDisable() {
	cfgPath := config.PrimaryPath()
	if cfgPath == "" {
		log.Fatal("audit disable: cannot determine config directory")
	}

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		fmt.Println("No config found — audit is not configured.")
		return
	}

	changed, _, err := toggleAuditURL(cfgPath, false)
	if err != nil {
		log.Fatalf("audit disable: %v", err)
	}
	if !changed {
		fmt.Println("Audit is already disabled (audit-url is commented out).")
		return
	}
	fmt.Println("Audit disabled. Restart Claude Code to apply.")
}

// toggleAuditURL comments or uncomments the audit-url line in the config file.
// Returns (changed, isPlaceholder, error).
func toggleAuditURL(path string, enable bool) (bool, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false, err
	}

	var out bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(data))
	changed := false
	placeholder := false

	for scanner.Scan() {
		line := scanner.Text()
		if !changed {
			if enable && (strings.HasPrefix(line, "# audit-url=") || strings.HasPrefix(line, "#audit-url=")) {
				// Uncomment: strip leading "# " or "#"
				uncommented := strings.TrimPrefix(line, "# ")
				uncommented = strings.TrimPrefix(uncommented, "#")
				fmt.Fprintln(&out, uncommented)
				placeholder = strings.Contains(uncommented, "your-collector")
				changed = true
				continue
			}
			if !enable && strings.HasPrefix(line, "audit-url=") {
				// Comment out
				fmt.Fprintln(&out, "# "+line)
				changed = true
				continue
			}
		}
		fmt.Fprintln(&out, line)
	}
	if err := scanner.Err(); err != nil {
		return false, false, err
	}
	if !changed {
		return false, false, nil
	}
	return true, placeholder, os.WriteFile(path, out.Bytes(), 0600)
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
