# Changelog

All notable changes to pty-mcp are documented here.

## [v0.9.1] - 2026-05-16

### Fixed
- **pwsh PTY encoding corruption on macOS** — commands containing `$true`, `$false`, `~`, or Unicode characters no longer produce `d@@@@: The term ... is not recognized` errors
  - Root cause: PSReadLine sends `ESC[6n` cursor-position queries; without a response it renders with garbage offsets, producing corrupted sequences that `StripANSI` could not clean
  - Fix: read goroutine now intercepts `ESC[6n` and replies `ESC[1;1R`; `Remove-Module PSReadLine` is then injected at startup (safe now that DSR responses are in place)
  - Additional: pwsh launched with `-NoLogo -NoProfile`; added `CLICOLOR=0` and `TERM_PROGRAM=` env vars; `$PSStyle.Progress` OSC indicator disabled; plain-text prompt function installed
  - `StripANSI` enhanced to handle OSC+ST, DCS, SOS/PM/APC sequences and calls `strings.ToValidUTF8` before regex

## [v0.9.0] - 2026-04-28

### Added
- `prepare_secret` tool — pre-stage a secret before a password prompt appears; stored in session buffer and automatically sent when a password prompt is detected (no agent round-trip needed)
- `line_ending` param on `prepare_secret` — agent specifies the line ending to append (`\r` default, `\r\n`, `\n`); handles device-specific requirements without hardcoding
- Settle detection before auto-sending buffered secret — waits up to 2s for output to stabilize so the device has switched to no-echo mode before the secret is sent
- `send_secret` now checks for a buffered secret first (from `prepare_secret`) before showing a GUI dialog

### Fixed
- Serial session `Write` now appends `\r` instead of `\r\n` — prevents stray `\n` from being interpreted as an empty password submission on serial console devices

## [v0.8.0] - 2026-04-13

### Added
- **Audit log** — optional voluntary operation log for `send_input` commands
- `pty-mcp audit init` — create config file (`~/.config/pty-mcp/config`) and generate shared token
- `pty-mcp audit enable` — uncomment `audit-url` in config (runs `init` first if no config exists)
- `pty-mcp audit disable` — comment out `audit-url`, preserving config and token
- `pty-mcp audit serve --port PORT --log FILE` — run HTTP collector; appends JSONL
- Two-phase audit: CmdEntry (before execution) + OutputEntry (after output), linked by `cmd_id`
- best-effort mode (default): async queue, non-blocking; strict mode: rejects `send_input` if log cannot be delivered
- `send_secret` is never logged

> **Note:** This is a voluntary, self-reporting operation log. It is not a substitute for system-level audit tools (auditd, SSH session recording, etc.).

## [v0.7.2] - 2026-04-07

### Fixed
- `CLAUDE_PLUGIN_ROOT` guard in `install.sh` — early exit if not set (was silently pointing to `/bin`)
- `grep | awk` pipeline in checksum verification now uses `|| true` to prevent `set -e` exit on no match
- `|| echo "unknown"` fallback in version check pipeline prevents silent exit
- `trap 'rm -f ...' EXIT` — temp files cleaned up on any exit (success or failure)
- curl timeouts added: `--connect-timeout 10 --max-time 25 --retry 2` for binary download
- SessionStart hook timeout increased: 30s → 60s

## [v0.7.1] - 2026-04-07

### Fixed
- `logRotator` thread safety — added `sync.Mutex`; Write/Close/rotate all locked
- `rotate()` nil dereference on `OpenFile` failure — now logs error and returns instead of panic
- `wait_for` timeout no longer marks buffer position, preserving unread output for subsequent reads
- Timeout tail output now includes last partial line (e.g. shell prompt)

## [v0.7.0] - 2026-04-07

### Added
- `wait_for` and `wait_for_timeout` params in `send_input` — combine send + wait in one tool call
- `timed_out: true` field in wait result — explicit boolean, agent doesn't need to parse error strings
- Log rotation: `log_max_size` (MB) and `log_max_files` params on all `create_*_session` tools
- `list_remote_sessions` `status` filter — client-side filtering by session status

### Fixed
- `wait_for` prompt matching now checks `remainder` (incomplete lines without trailing newline) — patterns like `console>` or `#` now match immediately instead of timing out

## [v0.6.0] - 2026-04-04

### Added
- `send_input` `raw` param — `raw=true` skips appending `\n`, for interactive menus (router CLI, BIOS, Sophos)
- `send_input` returns `cursor_start` / `cursor_end` for command boundary tracking
- Prompt classifier (`internal/session/classifier.go`) — classifies last 2 KB of output into: `at_prompt`, `password_prompt`, `confirmation`, `pager`, `running`, `unknown`
- `get_session_state` returns `state`, `awaiting_secret`, `last_prompt`
- `UnmarshalMcpArgs` helper using `mitchellh/mapstructure` with `WeaklyTypedInput: true` — fixes MCP clients sending all params as JSON strings

## [v0.5.0] - 2026-04-03

### Added
- `max_bytes` param in `read_output` — chunked incremental reads; `has_more: true` signals more unread data
- `log_file` param on all `create_*_session` tools — PTY output tee'd to file via `io.MultiWriter`
- Default ring buffer size: 256 KB → 1 MB; max: 4 MB → 32 MB

### Fixed
- Cursor now reflects bytes actually read (not total written)
- `ReadSinceMax` clamps future cursor to `rb.written` (prevents stuck cursor)
- `is_truncated` check in `waitForPattern` was always false

## [v0.4.0] - 2026-03-30

### Added
- `get_session_state` tool — returns session metadata and buffer cursor position
- Cursor-based incremental reads: `since_cursor` param in `read_output`
- Structured error codes: `ToolError{Code, Message, Retryable}` with `classifyError()` heuristic
- Typed errors: `SessionNotFoundError`, `SessionLimitError`

## [v0.3.1] - 2026-03-27

### Fixed
- AppleScript / PowerShell injection in `send_secret`; `zenity --no-markup`
- Goroutine leak fix
- Session limits: 50 active / 100 total
- `crypto/rand` session IDs
- Serial device path validation
- SSH `known_hosts` enforcement
- SHA256SUMS added to releases; `install.sh` verifies checksum

## [v0.3.0] - 2026-03-26

### Added
- `send_secret` tool — platform-native GUI password dialog (macOS: osascript, WSL2: Get-Credential, Linux: zenity/kdialog, headless: /dev/tty); password never exposed to AI context or logs
- Claude Code plugin packaging — `claude plugin marketplace add raychao-oao/pty-mcp`
- Community files: CONTRIBUTING, CODE_OF_CONDUCT, SECURITY, issue/PR templates

## [v0.2.0] - 2026-03-24

### Added
- `wait_for` param in `read_output` — blocks until regex pattern appears in output
- `context_lines` param — returns matched line plus N lines of surrounding context
- `tail_lines` param — returns last N lines on timeout
- Ring buffer (bounded memory) — prevents OOM on long-running sessions

## [v0.1.0] - 2026-03-23

### Added
- Initial release
- `create_local_session` — interactive local PTY (bash, python3, node, etc.)
- `create_ssh_session` — SSH to remote hosts; reads `~/.ssh/config` aliases
- `create_serial_session` — serial port devices (IoT, network gear)
- `send_input`, `read_output`, `send_control` — interact with sessions
- `list_sessions`, `close_session` — session lifecycle management
- `detach_session`, `list_remote_sessions` — persistent sessions via ai-tmux daemon
- Settle detection — waits for output to stabilize before returning
- GitHub Actions CI — auto-builds 8 binaries (macOS/Linux × amd64/arm64) on tag
