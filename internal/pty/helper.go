package pty

import (
	"regexp"
	"strings"
	"time"
)

var ansiEscape = regexp.MustCompile(
	`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07]*\x07|\x1b[()][AB012]|\r`,
)

func StripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

var commonPrompts = []*regexp.Regexp{
	regexp.MustCompile(`\$\s*$`),
	regexp.MustCompile(`#\s*$`),
	regexp.MustCompile(`>>>\s*$`),
	regexp.MustCompile(`>\s*$`),
	regexp.MustCompile(`=>\s*$`),
	regexp.MustCompile(`\[.*\]\s*[#$>]\s*$`),
	regexp.MustCompile(`(?i)select.*:\s*$`),
	regexp.MustCompile(`(?i)password.*:\s*$`),
	regexp.MustCompile(`(?i)login.*:\s*$`),
}

func HasPrompt(output string) bool {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) == 0 {
		return false
	}
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	for _, p := range commonPrompts {
		if p.MatchString(lastLine) {
			return true
		}
	}
	return false
}

// WaitForSettle waits for output to stabilize and returns (output, isComplete).
// isComplete=true means output settled or a prompt was detected; false means timeout.
func WaitForSettle(getOutput func() string, settle, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	last := getOutput()
	lastChange := time.Now()

	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		current := getOutput()

		if current != last {
			last = current
			lastChange = time.Now()
			continue
		}

		if time.Since(lastChange) >= settle {
			return current, true
		}

		if HasPrompt(StripANSI(current)) {
			return current, true
		}
	}

	return getOutput(), false
}
