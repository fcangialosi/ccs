# ccs

TUI for browsing and resuming Claude Code sessions across all project directories.

## Structure

Single-file Go app. Everything is in `main.go`.

- **Data types** — `Session`, index/JSONL parse structs
- **List item** — `item` implements `bubbles/list.Item` (Title, Description, FilterValue)
- **Styles** — lipgloss style vars
- **Model** — bubbletea model with filter toggles (named-only, active-only)
- **Data loading** — `loadAllSessions()` reads from two sources:
  - `sessions-index.json` (fast, but stale — older sessions only)
  - `*.jsonl` files (fallback for un-indexed sessions, also provides `custom-title`)
  - `~/.claude/sessions/*.json` PID files (active session detection)
- **Main** — CLI flag parsing, TUI setup, prints `projectPath\nsessionID` on selection

Shell wrapper (`ccs.sh`) consumes that output: does the `cd` and prints the resume command.

## Development

```
make build    # build binary
make check    # vet + compile check
```

No tests yet. To verify changes, run `./ccs --list` (plain text, no TTY needed) and `./ccs` (interactive TUI).

## Key decisions

- Sessions-index is often stale (recent sessions missing). We always scan for JSONL files not in the index.
- Session names come from `type: "custom-title"` entries in the JSONL, set when users run `claude -n "name"`.
- Active detection: check if the PID in `~/.claude/sessions/<pid>.json` is alive via `kill(pid, 0)`.
- The binary outputs raw data (path + session ID); the shell wrapper handles `cd` because a child process can't change the parent's cwd.
- All bubbletea model methods use value receivers for consistency. `applyFilters()` returns the modified model.
