# ccs

TUI for browsing and resuming Claude Code sessions across all project directories.

## Structure

Single-file Go app. Everything is in `main.go`.

- **Data types** — `Session`, index/JSONL parse structs
- **List item** — `item` implements `bubbles/list.Item`. Title = age + name/first-prompt; Description = path + msg count + `↳ ` latest user message. Age column shows whichever timestamp the active sort uses (`item.sortTime`, set in `applyFilters`).
- **Styles** — lipgloss style vars
- **Model** — bubbletea model with filter toggles (named-only `tab`, active-only `ctrl+a`) and a sort toggle (`s`: recent ⇄ created; `sortMode` / `sortItems`)
- **Data loading** — `loadAllSessions()` builds a job list, then scans every session's JSONL **in parallel** (`runtime.NumCPU()` workers) — startup was ~2.9s sequential, ~0.8s parallel. Sources:
  - `sessions-index.json` (fast metadata: created, first prompt, summary, path) — supplemented per session by `extractMeta` (custom title, latest prompt, last-interacted time; the index has none of these)
  - `*.jsonl` files (full load for un-indexed sessions, via `loadFromJSONL`)
  - `~/.claude/sessions/*.json` PID files (active session detection)
  - `scanJSONL` reads with a growing `bufio.Reader` (not a fixed-buffer `Scanner`) so multi-MB lines never truncate a scan — the old 1MB `Scanner` limit silently dropped every session whose file had a >1MB line (~half of them).
- **Main** — CLI flag parsing, TUI setup, prints `projectPath\nsessionID` on selection

Shell wrapper (`ccs.sh`) consumes that output: does the `cd` and prints the resume command.

## Development

```
make build    # build binary
make check    # vet + compile check
```

`go test ./...` covers the search matching logic (`search_test.go`). For UI changes, verify with `./ccs --list` (plain text, no TTY needed) and `./ccs` (interactive TUI). The TUI can be driven headlessly under tmux: `tmux new-session -d -x 120 -y 35 "./ccs -a"`, `tmux send-keys`, `tmux capture-pane -p -e` (`-e` keeps escape codes — useful for catching render corruption).

## Key decisions

- Sessions-index is often stale (recent sessions missing). We always scan for JSONL files not in the index.
- Session names come from `type: "custom-title"` entries in the JSONL, set when users run `claude -n "name"`.
- **Latest message**: the `type: "last-prompt"` entry (field `lastPrompt`) is Claude Code's own clean record of the most recent user prompt — preferred for display. Fallback: the last user message where `realPrompt()` holds (filters out empty tool-result envelopes and `<command-*>`/`<local-command-*>`/`Caveat:` wrappers). `last-prompt` entries carry **no timestamp**, so "last interacted" (`Session.LastMsg`) comes from that last real user message's timestamp.
- **Sort**: `recent` (default) orders by `LastMsg` (last user interaction); `created` orders by `Created` (first user message). The age column and ordering both follow the active mode.
- Active detection: check if the PID in `~/.claude/sessions/<pid>.json` is alive via `kill(pid, 0)`.
- The binary outputs raw data (path + session ID); the shell wrapper handles `cd` because a child process can't change the parent's cwd.
- All bubbletea model methods use value receivers for consistency. `applyFilters()` returns the modified model.
- **Search** (`/` or `ctrl+f`) is a single unified box over names *and* JSONL contents. Bubbles' built-in list filtering is deliberately **disabled** (`SetFilteringEnabled(false)`): its match-highlighter splices style codes into our already-ANSI-styled `Title()` strings and corrupts the render. Names/metadata filter instantly on keystroke; full-content matches stream in from a debounced background scan (`searchContents`, parallel across CPUs, seq-tagged so stale scans are dropped). `fileContainsAll` reads in bounded chunks with overlap — no line-length limit. up/down navigate while the input is focused; enter resumes the highlighted match. Future scaling ideas (ripgrep-if-installed, persistent index) are in `docs/search-scaling.md`.
