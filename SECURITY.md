# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | ✅ |
| Older releases | ❌ |

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

To report a security vulnerability, open a [GitHub Security Advisory](https://github.com/raychao-oao/pty-mcp/security/advisories/new) (private disclosure).

Include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

We aim to respond within 7 days and will coordinate a fix and disclosure timeline with you.

## Security Considerations

pty-mcp provides shell access to AI agents. Please be aware:

- Run pty-mcp with the minimum required permissions
- Avoid exposing the MCP server over a network without authentication
- Consider running in a sandboxed environment (e.g., Docker) for untrusted workloads
