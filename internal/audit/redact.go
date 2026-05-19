package audit

import "regexp"

// redactRule pairs a compiled pattern with its replacement template.
type redactRule struct {
	re          *regexp.Regexp
	replacement string
}

var redactRules = []redactRule{
	// key=value or key: value — redacts everything on the line after the separator
	// so multi-word values (e.g. token: foo bar) are fully covered.
	{
		re:          regexp.MustCompile(`(?i)((?:password|passwd|secret|token|api[_-]?key|access[_-]?key|auth[_-]?token)\s*[=:]\s*)[^\n]+`),
		replacement: `${1}[REDACTED]`,
	},
	// HTTP Authorization header value (Bearer, Basic, Token schemes)
	{
		re:          regexp.MustCompile(`(?i)(Authorization:\s*(?:Bearer|Basic|Token)\s+)\S+`),
		replacement: `${1}[REDACTED]`,
	},
	// PEM private key blocks (RSA, EC, OPENSSH, or generic)
	{
		re:          regexp.MustCompile(`-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----[\s\S]*?-----END (?:[A-Z ]+ )?PRIVATE KEY-----`),
		replacement: `[PRIVATE KEY REDACTED]`,
	},
}

// Redact replaces known credential patterns in s with safe placeholders.
// It is applied to command strings and output snippets before they are
// written to the audit log so that secrets which leak into shell commands
// or command output are not stored in plaintext.
func Redact(s string) string {
	for _, rule := range redactRules {
		s = rule.re.ReplaceAllString(s, rule.replacement)
	}
	return s
}
