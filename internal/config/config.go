package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Audit holds audit log settings loaded from the config file.
type Audit struct {
	URL   string
	User  string
	Mode  string
	Token string
}

// LoadAudit reads audit settings from the config file.
// Search order:
//  1. ~/.config/pty-mcp/config  (XDG / os.UserConfigDir)
//  2. ~/.pty-mcp                (legacy fallback)
//
// Returns nil, nil if no config file exists or audit-url is not set.
// Returns an error if the config file has unsafe permissions or is malformed.
func LoadAudit() (*Audit, error) {
	primary, legacy, err := configPaths()
	if err != nil {
		return nil, nil // can't determine home dir; treat as no config
	}

	kv, usedPath, err := readKV(primary)
	if err != nil {
		return nil, err
	}
	if kv == nil {
		kv, usedPath, err = readKV(legacy)
		if err != nil {
			return nil, err
		}
		if kv != nil {
			fmt.Fprintf(os.Stderr, "[pty-mcp] config: %s is deprecated, consider moving to %s\n", usedPath, primary)
		}
	}

	if kv == nil || kv["audit-url"] == "" {
		return nil, nil
	}

	return &Audit{
		URL:   kv["audit-url"],
		User:  kv["audit-user"],
		Mode:  kv["audit-mode"],
		Token: kv["audit-token"],
	}, nil
}

// PrimaryPath returns the primary config file path (~/.config/pty-mcp/config).
// Returns an empty string if the home directory cannot be determined.
func PrimaryPath() string {
	p, _, _ := configPaths()
	return p
}

// configPaths returns (primary, legacy) config file paths.
func configPaths() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "pty-mcp", "config"), filepath.Join(home, ".pty-mcp"), nil
}

// readKV parses a key=value config file after checking permissions.
// Returns nil map (no error) if the file does not exist.
func readKV(path string) (map[string]string, string, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, path, nil
	}
	if err != nil {
		return nil, path, err
	}

	// Require no group/other access on Unix when a token is present.
	// Windows has no meaningful Unix permissions — skip the check.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, path, fmt.Errorf(
			"config file %s has unsafe permissions; run: chmod 600 %s", path, path,
		)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, path, err
	}
	defer f.Close()

	kv := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || text[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(text, "=")
		if !ok {
			return nil, path, fmt.Errorf("config %s:%d: expected key=value, got %q", path, line, text)
		}
		kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return kv, path, scanner.Err()
}
