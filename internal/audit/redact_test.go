package audit

import (
	"strings"
	"testing"
)

func TestRedact_Password(t *testing.T) {
	cases := []struct{ in, wantContains, wantNotContains string }{
		{"password=s3cr3t", "password=[REDACTED]", "s3cr3t"},
		{"PASSWORD=s3cr3t", "PASSWORD=[REDACTED]", "s3cr3t"},
		{"passwd: hunter2", "passwd: [REDACTED]", "hunter2"},
		{"token=ghp_abcdef123", "token=[REDACTED]", "ghp_abcdef123"},
		{"api_key=sk-1234", "api_key=[REDACTED]", "sk-1234"},
		{"access-key=AKIA1234", "access-key=[REDACTED]", "AKIA1234"},
		{"auth_token: mytoken123", "auth_token: [REDACTED]", "mytoken123"},
	}
	for _, tc := range cases {
		out := Redact(tc.in)
		if !strings.Contains(out, tc.wantContains) {
			t.Errorf("Redact(%q) = %q, want it to contain %q", tc.in, out, tc.wantContains)
		}
		if strings.Contains(out, tc.wantNotContains) {
			t.Errorf("Redact(%q) = %q, should NOT contain %q", tc.in, out, tc.wantNotContains)
		}
	}
}

func TestRedact_AuthorizationHeader(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Authorization: Bearer eyJhbGci", "Authorization: Bearer [REDACTED]"},
		{"authorization: basic dXNlcjpwYXNz", "authorization: basic [REDACTED]"},
		{"Authorization: Token abc123", "Authorization: Token [REDACTED]"},
	}
	for _, tc := range cases {
		out := Redact(tc.in)
		if out != tc.want {
			t.Errorf("Redact(%q)\n  got  %q\n  want %q", tc.in, out, tc.want)
		}
	}
}

func TestRedact_PrivateKey(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIE...\n-----END RSA PRIVATE KEY-----"
	out := Redact(pem)
	if out != "[PRIVATE KEY REDACTED]" {
		t.Errorf("Redact(RSA key) = %q, want [PRIVATE KEY REDACTED]", out)
	}

	openssh := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3Bl...\n-----END OPENSSH PRIVATE KEY-----"
	out = Redact(openssh)
	if out != "[PRIVATE KEY REDACTED]" {
		t.Errorf("Redact(OPENSSH key) = %q, want [PRIVATE KEY REDACTED]", out)
	}
}

func TestRedact_NoFalsePositives(t *testing.T) {
	safe := []string{
		"ls -la",
		"echo hello world",
		"grep -r pattern /etc",
		"Authorization: none",  // not a real auth scheme
		"this is a password hint",
		"password manager",
	}
	for _, s := range safe {
		out := Redact(s)
		if out != s {
			t.Errorf("Redact(%q) changed to %q (unexpected redaction)", s, out)
		}
	}
}
