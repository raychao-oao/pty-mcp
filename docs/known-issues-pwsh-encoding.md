# pwsh session encoding corruption on macOS

> **Status: FIXED** in v0.9.x (commit `115079c`)

## Summary

When opening a local pty-mcp session with `command: pwsh` on macOS, sending input containing certain characters (notably `$`, `~`, Unicode) caused the output to be garbled with replacement characters (e.g., `d@@@@: The term 'd@@@@' is not recognized...`).

## Root Cause

Two layered problems:

1. **DSR feedback loop**: PSReadLine sends `ESC[6n` (cursor position query) before each readline. pty-mcp's Go process never forwarded the outer terminal's `ESC[row;colR` response into the PTY, so PSReadLine received no answer and rendered with garbage cursor offsets — producing corrupted VT sequences that `StripANSI` could not clean.

2. **PSReadLine VT rendering**: Even with `TERM=dumb`, PSReadLine checks `SupportsVirtualTerminal` (not `$TERM`) and enables full VT line editing, generating character-by-character redraw sequences that polluted the buffer.

## Fix (v0.9.x+)

Applied in `internal/aitx/ptysession.go` and `internal/pty/helper.go`:

- **DSR interception**: Read goroutine detects `ESC[6n` in PTY output, responds `ESC[1;1R`, strips the query from the buffer. PSReadLine now renders cleanly.
- **`-NoLogo -NoProfile`**: Skip user profile scripts (primary source of OSC 133/633 shell-integration hooks).
- **`Remove-Module PSReadLine`**: Removes VT line editing after DSR is in place. `.NET Console.ReadLine()` now works correctly with DSR responses.
- **Extra env vars**: `CLICOLOR=0`, `TERM_PROGRAM=` (empty) suppress BSD color flags and terminal-specific probes.
- **Startup injection**: `$PSStyle.OutputRendering = 'PlainText'`, `$PSStyle.Progress.UseOSCIndicator = $false`, custom plain-text `prompt` function.
- **Enhanced `StripANSI`**: Handles OSC with ST terminator, DCS, SOS/PM/APC sequences; calls `strings.ToValidUTF8` before regex.

## Remaining Behavior

After `Remove-Module PSReadLine`, `.NET Console.ReadLine()` echoes each character incrementally. The `send_input` output contains a character-by-character build-up of the command followed by the actual result. This is cosmetically verbose but functionally correct — the result and prompt appear cleanly at the end.

## Date First Observed

2026-05-15

## Date Fixed

2026-05-16
