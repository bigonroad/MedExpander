# MedExpand

A Windows system-tray text expander, written in Go. Type an abbreviation
followed by a space anywhere in Windows and it's replaced with a full
snippet — built for clinical note templates, but general-purpose.

## Features

- Global text expansion via a low-level keyboard hook (works in any
  Windows app — EMR, Word, browser, etc.)
- Snippet placeholders: `{#cursor}`, `{#randomtext a|b|c}`, `{#insert name}`
- In-app snippet editor (auto-sorts alphabetically on save)
- Usage statistics (replacements, time saved, cost saved) logged to a
  plain CSV file
- A typing-speed test to calibrate the time-saved estimate
- Tray icon dashboard (left-click or double-click the tray icon)

## Why this repo exists

This is a self-compiled, unsigned Windows executable. Because it installs
a global keyboard hook and injects synthetic keystrokes (`SendInput`) to
perform expansion, Windows Defender and other AV engines can flag it as
a generic keylogger/trojan on heuristics alone — the *behavior* pattern
(hook + inject) is identical to real spyware even though this tool only
ever acts on the snippets you define yourself, with no network activity
and no exfiltration of anything you type.

Publishing the source here and pursuing code signing is intended to
establish provenance and build reputation with SmartScreen/Defender over
time, since signed + publicly-auditable binaries are scored very
differently than anonymous ones.

## Building

Requires Go 1.22+. Cross-compiles cleanly from Linux/macOS with no CGO
and no Windows toolchain required:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
  -trimpath -ldflags "-H=windowsgui -s -w" \
  -o MedExpand.exe .
```

The icon is embedded via the included `rsrc_windows_amd64.syso` (a
prebuilt Windows resource object containing the `.ico`). `go build`
automatically links any `*_windows_amd64.syso` file found in the package
directory — no extra build step needed. If you ever want to regenerate
it from `medexpand.ico` (e.g. after changing the icon), use
[`rsrc`](https://github.com/akavel/rsrc):

```bash
go install github.com/akavel/rsrc@latest
rsrc -ico medexpand.ico -o rsrc_windows_amd64.syso
```

## Running

Drop `MedExpand.exe` and a `snippets.txt` (see `snippets.example.txt` for
the format) in the same folder, then run the exe. It lives in the system
tray; right-click for the menu, left-click/double-click for the dashboard.

`stats.csv` and `snippets.txt` are both created next to the exe on first
run if they don't already exist, and are plain text so they're easy to
back up, version, or inspect.

## Known false-positive behavior

If Windows Defender or your browser flags this file, see the note above
— this is a heuristic false positive common to any self-built tool using
a global keyboard hook, not an indication of actual malicious behavior.
Restoring from Defender's Protection History and adding a folder
exclusion resolves it locally; code signing is the long-term fix.

_Free code signing provided by [SignPath.io](https://signpath.io/), certificate by [SignPath Foundation](https://signpath.org/)._
