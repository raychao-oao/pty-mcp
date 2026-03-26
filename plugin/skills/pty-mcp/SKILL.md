---
name: pty-mcp
description: Use pty-mcp MCP tools to manage interactive PTY sessions — local shell, SSH, serial port, and persistent remote sessions. Activate when the user asks to run commands on a server, connect via SSH, interact with network devices, monitor logs, or needs a stateful terminal session.
version: 0.3.0
---

# pty-mcp — Interactive PTY Sessions

pty-mcp gives AI agents a stateful terminal: directory changes, environment variables, and running processes persist across tool calls.

## Core Tools

| Tool | Description |
|------|-------------|
| `create_local_session` | Start a local shell (bash, python3, node…) |
| `create_ssh_session` | SSH to a remote host |
| `create_serial_session` | Connect to a serial port device |
| `send_input` | Send a command, wait for output to settle |
| `read_output` | Read output; optionally wait for a regex pattern |
| `send_secret` | Prompt the human for a password via GUI dialog (never exposed to AI) |
| `send_control` | Send ctrl+c, ctrl+d, arrow keys, tab… |
| `detach_session` | Disconnect but keep remote PTY running (ai-tmux) |
| `list_sessions` | List active sessions |
| `close_session` | Terminate a session |

## Common Patterns

### SSH + sudo password
```
create_ssh_session(host: "server", user: "admin")
send_input(session_id, "sudo apt update")
read_output(session_id, wait_for: "password")
send_secret(session_id, prompt: "sudo password:")   # GUI dialog, AI never sees it
read_output(session_id, wait_for: "\\$", timeout: 60)
```

### Wait for process to finish
```
send_input(session_id, "make build")
read_output(session_id, wait_for: "Build complete|Error", timeout: 300, tail_lines: 20)
```

### Persistent session (survives disconnect)
```
create_ssh_session(host: "server", user: "admin", persistent: true)
send_input(session_id, "./run-migration.sh")
detach_session(session_id)   # safe to disconnect, script keeps running
# later: reattach with create_ssh_session(..., session_id: "existing-id")
```

### Serial / network device
```
create_serial_session(port: "/dev/ttyUSB0", baud: 9600)
read_output(session_id, wait_for: ">")
send_input(session_id, "show version")
```

## Important Rules

- **send_secret**: Only call when the session is actively waiting for password input (echo off). Do NOT call on an idle shell prompt.
- **wait_for**: Always set a `timeout` for long-running commands. Use `tail_lines` to get context on timeout.
- **Persistent sessions**: Require `ai-tmux` daemon on the remote server.
