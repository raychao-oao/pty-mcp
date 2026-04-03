# pty-mcp Issues: Interactive Menu Timing (Sophos Firewall)

## Environment
- Device: Sophos XG135w, SFOS 18.5.4 / 19.0.2
- Connection: Serial (38400 baud) and SSH
- pty-mcp version: 0.5.0

## Problem Summary

Sophos firewall's interactive console menu has very tight input timing. `send_input` sends `"4\n"` but the menu processes `"4"` and immediately shows the next screen, then the trailing `"\n"` gets interpreted as a second (empty) input, causing "Invalid Menu Selection" and bouncing back to the main menu.

## Reproduction

### Menu Structure
```
Select Menu Number [0-7]: _
```
When user types `4` and presses Enter:
1. Menu receives `4` → enters sub-menu
2. Sub-menu briefly appears
3. Sub-menu receives `\n` → treats as empty/invalid input
4. Returns to main menu with "Invalid Menu Selection"

### Observed Behavior with send_input

```
send_input("4\n")  →  "4\n\n\nSophos Firmware Version...\n\nMain Menu\n...\n    Select Menu Number [0-7]:      Invalid Menu Selection"
```

The sub-menu (e.g., Device Console, Network Configuration) is never shown in the output because it flashes by and immediately exits due to the stray newline.

### Same Issue with Option 6 (Reboot)

Option 6 shows a sub-prompt with very short timeout:
```
Shutdown(S/s) or Reboot(R/r) Device  (S/s/R/r):  No (Enter) >
```
This prompt times out in ~2 seconds. By the time `send_input("R\n")` can be sent, the prompt has already timed out and returned to the main menu.

## Workarounds Used

1. **Advanced Shell worked once** — `send_input("5\n")` successfully entered Advanced Shell on the first attempt, but failed on subsequent attempts. The difference is unclear.

2. **Direct Linux commands** — Once inside Advanced Shell, regular commands (`df -h`, `ls`, `reboot`) work fine with `send_input`.

3. **JavaScript/evaluate_script** — For the Sophos Web UI, we used Chrome MCP's `evaluate_script` to click checkboxes directly instead of relying on the a11y tree click timing.

4. **User manual intervention** — For critical operations (reboot, menu navigation), asked the user to type directly on the console.

## Suggested Improvements

### 1. send_input without trailing newline
Add an option to send characters without appending `\n`:
```
send_input("4", raw=true)  // sends just "4", no newline
```
Then separately:
```
send_control("enter")  // sends Enter when ready
```

### 2. send_input with delayed Enter
Add an option to delay the Enter key after the main input:
```
send_input("4", enter_delay_ms=500)  // sends "4", waits 500ms, then sends \n
```

### 3. Interactive menu mode
A mode that sends single characters immediately without buffering:
```
send_char("4")  // sends just the character "4" immediately
```
This would let the menu process the character, then the caller can wait for the next prompt before sending more input.

### 4. wait_for + send pattern
Combine waiting for a prompt with sending input atomically:
```
wait_and_send(session_id, wait_for="Select Menu Number", input="4\n", timeout=10)
```
This ensures input is only sent when the prompt is actually waiting.

## Additional Context

- The same timing issue occurs on both serial (38400 baud) and SSH connections
- Sophos menus use a custom shell, not bash — they read single characters and have very short input timeouts
- The `send_control("enter")` tool exists but doesn't solve the core problem since `send_input` always appends a newline
- This is not specific to Sophos — any interactive menu system with tight timing (e.g., router CLI, BIOS setup) would have the same issue
