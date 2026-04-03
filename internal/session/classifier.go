package session

import (
	"regexp"
	"strings"
)

// ClassifyResult holds the result of classifying the last terminal output.
type ClassifyResult struct {
	State          string // "at_prompt" | "password_prompt" | "confirmation" | "pager" | "running" | "unknown"
	AwaitingSecret bool
	LastPrompt     string // last non-empty line (ANSI stripped)
}

var (
	reANSI           = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b[()][0-9A-Z]|\r`)
	rePasswordPrompt = regexp.MustCompile(`(?i)(password|passphrase)(\s+for\s+\S+)?\s*:[ ]*$`)
	reSudoPrompt     = regexp.MustCompile(`(?i)\[sudo\]\s+password`)
	reSSHHostKey     = regexp.MustCompile(`(?i)Are you sure you want to continue connecting`)
	reConfirmation   = regexp.MustCompile(`(?i)(\(yes/no[^)]*\)|\(y/n\)|shutdown\s*\(s/s|are you sure|continue connecting)`)
	rePager          = regexp.MustCompile(`(?m)(^\(END\)\s*$|^--More--|lines \d+-\d+/\d+)`)
	reShellPrompt    = regexp.MustCompile(`[$#>%]\s*$`)
	reMenuSelect     = regexp.MustCompile(`(?i)(select\s+\w+.*:|enter\s+\w+.*:|\[\d[^\]]*\]:)\s*$`)
)

// ClassifyOutput analyzes the last portion of terminal output and returns the current state.
// Handles ANSI escape codes internally.
func ClassifyOutput(raw string) ClassifyResult {
	// Strip ANSI and carriage returns
	output := reANSI.ReplaceAllString(raw, "")

	// Use last 2KB for classification
	if len(output) > 2048 {
		output = output[len(output)-2048:]
	}

	// Get last non-empty line
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	lastLine := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastLine = strings.TrimSpace(lines[i])
			break
		}
	}

	// Password / secret prompt — check last line and full window for sudo pattern
	if reSudoPrompt.MatchString(output) || rePasswordPrompt.MatchString(lastLine) {
		return ClassifyResult{State: "password_prompt", AwaitingSecret: true, LastPrompt: lastLine}
	}

	// SSH host key or confirmation
	if reSSHHostKey.MatchString(output) || reConfirmation.MatchString(lastLine) {
		return ClassifyResult{State: "confirmation", LastPrompt: lastLine}
	}

	// Pager (less/more)
	if rePager.MatchString(output) {
		return ClassifyResult{State: "pager", LastPrompt: lastLine}
	}

	// Shell or interactive menu prompt
	if reShellPrompt.MatchString(lastLine) || reMenuSelect.MatchString(lastLine) {
		return ClassifyResult{State: "at_prompt", LastPrompt: lastLine}
	}

	if lastLine != "" {
		return ClassifyResult{State: "running", LastPrompt: lastLine}
	}
	return ClassifyResult{State: "unknown"}
}
