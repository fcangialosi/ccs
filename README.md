# ccs

Browse and resume [Claude Code](https://claude.ai/code) sessions from any directory.

Claude Code scopes `--resume` to the directory you're in. `ccs` aggregates sessions from all directories into one fuzzy-searchable TUI, with first-class support for named sessions (`claude -n "my-session"`).

## Install

Requires Go 1.22+.

```
make build          # produces ./ccs binary
make install        # copies to /usr/local/bin
```

Then source the shell wrapper in your `.zshrc` / `.bashrc`:

```bash
source ~/dev/ccs/ccs.sh
```

The wrapper lets `ccs` change your working directory to the session's project before printing the resume command.

## Usage

```
ccs              # interactive picker (named sessions by default)
ccs -a           # interactive picker (all sessions)
ccs -l           # plain text list (named only)
ccs -a -l        # plain text list (all)
```

### TUI keys

| Key      | Action                       |
|----------|------------------------------|
| `/`      | Fuzzy filter                 |
| `tab`    | Toggle named / all sessions  |
| `ctrl+a` | Toggle active-only filter   |
| `enter`  | Select session               |
| `q`/`esc`| Quit                         |

## How it works

Session data lives in `~/.claude/projects/<dir>/`. `ccs` reads:

- **`sessions-index.json`** — fast index of older sessions (summary, message count, timestamps)
- **`*.jsonl`** — full session logs; used for sessions not yet indexed, and to find `custom-title` entries (the `--name` flag)
- **`~/.claude/sessions/*.json`** — PID files for currently running sessions; used to show the active indicator (green dot)

## How to name a session

Start Claude Code with the `-n` flag:

```
claude -n "my-feature-work"
```

Named sessions appear first by default in `ccs` and are highlighted in purple.
