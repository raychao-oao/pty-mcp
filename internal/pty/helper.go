package pty

import (
	"context"
	"regexp"
	"strings"
	"time"
)

var ansiEscape = regexp.MustCompile(
	`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]` + // CSI sequences
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)` + // OSC terminated by BEL or ST
		`|\x1bP(?:[^\x1b]|\x1b[^\x5c])*\x1b\\` + // DCS sequences
		`|\x1b[\x58\x5e\x5f](?:[^\x1b]|\x1b[^\x5c])*\x1b\\` + // SOS/PM/APC sequences
		`|\x1b[()][AB012]` + // charset designation
		`|\x1b[\x20-\x2f]*[\x30-\x7e]` + // simple two-byte ESC sequences
		`|\r` +
		`|\x{fffd}`, // UTF-8 replacement character from invalid bytes
)

func StripANSI(s string) string {
	s = strings.ToValidUTF8(s, "")
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
// Empty output is never considered "settled" — we keep waiting for data until timeout.
func WaitForSettle(getOutput func() string, settle, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	last := getOutput()
	lastChange := time.Now()
	hasOutput := last != ""

	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		current := getOutput()

		if current != last {
			last = current
			lastChange = time.Now()
			hasOutput = hasOutput || current != ""
			continue
		}

		// Only settle when we've seen some output — empty doesn't count
		if hasOutput && time.Since(lastChange) >= settle {
			return current, true
		}

		if hasOutput && HasPrompt(StripANSI(current)) {
			return current, true
		}
	}

	return getOutput(), false
}

// WaitForSettleCtx is WaitForSettle with early exit when ctx is cancelled (e.g. the
// MCP client sent notifications/cancelled for this request). On cancellation it
// returns immediately with isComplete=false, same as a timeout.
func WaitForSettleCtx(ctx context.Context, getOutput func() string, settle, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	last := getOutput()
	lastChange := time.Now()
	hasOutput := last != ""

	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return getOutput(), false
		case <-timer.C:
		}
		timer.Reset(50 * time.Millisecond)

		current := getOutput()

		if current != last {
			last = current
			lastChange = time.Now()
			hasOutput = hasOutput || current != ""
			continue
		}

		if hasOutput && time.Since(lastChange) >= settle {
			return current, true
		}

		if hasOutput && HasPrompt(StripANSI(current)) {
			return current, true
		}
	}

	return getOutput(), false
}
