# Search scaling — notes & next ideas

Working notes for future improvements to content search. Nothing here is
urgent; the current implementation is fast enough today. This captures the
alternatives we discussed so they're not lost.

## Where things stand

Content search is an **in-process parallel scan** (`searchContents` in `main.go`):

- Fans out one goroutine per session, capped at `runtime.NumCPU()` in flight.
- Each file is read in bounded 1 MB chunks with a small overlap, ASCII-lowercased
  in place, and checked for all query terms (`fileContainsAll`). No line-length
  limit, bounded memory per file.
- Runs off the UI thread as a debounced `tea.Cmd`; results are seq-tagged so
  stale scans (from earlier keystrokes) are discarded.

Measured on the real store (~161 sessions, ~302 MB of JSONL):

- Warm (files in OS page cache): **~0.15–0.8 s** for a full scan.
- Cold: a few seconds (dominated by disk read).

**No external dependencies.** This keeps it a self-contained single-file Go app,
which is the whole point of the tool. Don't add deps lightly.

### When the current approach starts to hurt

- The store grows well past ~1 GB and cold scans get annoying.
- You search constantly and want zero perceptible latency even warm.
- You're on a spinning disk / network FS where cold reads are slow.

If none of those are true, leave it alone.

## Idea 1 — ripgrep if installed

Shell out to `rg` when it's on PATH, fall back to the in-process scan otherwise.
`rg` is extremely fast (multi-threaded, SIMD, mmap) and would make even cold
searches feel instant.

**Sketch:**

- At startup, probe once: `exec.LookPath("rg")`. Cache the bool on the model.
- `searchContentsRG(items, query)`:
  - Run `rg --json -l -i -F -e <term1> -e <term2> ... <files...>` — but note `rg`
    with multiple `-e` patterns is OR, not AND. For AND-of-terms, either:
    - run `rg -l -i -F <term1> <files>` to get the candidate file set, then
      `rg -l` again over that shrinking set for each subsequent term (cheap), or
    - pass all files and post-filter: for multi-term, intersect per-term `-l`
      results. Simplest correct option.
  - `-l` prints matching file paths; map path -> sessionID via the existing
    `jsonlPath` on each item.
  - Pass the file list explicitly (don't let `rg` walk `~/.claude`); we already
    know exactly which files correspond to listed sessions.
- Keep `searchContents` (in-process) as the fallback and as the source of truth
  for tests. `searchContentsRG` must return the **same** set — reuse the
  cross-check approach (compare rg result to in-process result over the real
  store) before trusting it.

**Tradeoffs:**

- (+) Fastest option, tiny code.
- (−) Behavior now depends on whether `rg` is present → two code paths to keep in
  sync (case-folding, term semantics, binary/again-line handling).
- (−) `rg`'s default smart-case / unicode handling differs subtly from our
  ASCII-only fold. Force `-i` (case-insensitive) + `-F` (fixed strings) to match
  our substring semantics as closely as possible.
- Gotcha: JSONL lines can be huge; `rg` handles this fine, but if we ever want
  line context (`--json` with match offsets) remember matches are inside giant
  one-line JSON blobs.

## Idea 2 — startup / persistent index

Build an index so searches are instant regardless of store size.

### 2a. In-memory index at launch

- On startup, read every session once and extract just the **text** we care about
  (user/assistant message text via `extractPromptText`-style parsing — skip
  base64 blobs, tool output noise, thumbnails). Store a single lowercased string
  per session in memory.
- Search becomes an in-memory `strings.Contains` over the cached strings —
  microseconds.

**Tradeoffs:**

- (+) Instant search, still no external deps.
- (−) Slower launch (must read all 302 MB up front — the thing we currently avoid
  by scanning lazily on demand).
- (−) Memory: even text-only extraction could be ~100+ MB resident. Fine on a dev
  box, wasteful for a picker you open for 3 seconds.
- (−) Deciding what counts as "content" (which JSON fields) is fiddly and risks
  missing matches the raw-bytes scan would catch.

### 2b. Persistent on-disk index (incremental)

- Keep an index file (e.g. `~/.cache/ccs/index.json` or a tiny SQLite/FTS DB)
  mapping sessionID -> extracted text (or an inverted term index).
- On launch, stat each JSONL; re-index only files whose mtime/size changed since
  last run. Most sessions are immutable once finished, so steady-state re-index
  cost is near zero.
- Search hits the index; only actively-growing sessions need a live tail-scan.

**Tradeoffs:**

- (+) Best of both: instant search AND fast launch after the first run.
- (+) Scales to any store size.
- (−) Most complexity: index format, invalidation, migration, corruption
  handling, keeping it consistent with the "always scan for un-indexed" ethos
  the tool already has for sessions-index.json.
- (−) SQLite FTS would pull in a dependency (cgo or a pure-Go driver) — against
  the single-file-no-deps grain. A hand-rolled JSON/gob index avoids that but is
  more code.

## Recommended path if/when this matters

1. First reach: **ripgrep-if-installed** (Idea 1). Biggest speedup for least code,
   and the in-process scan stays as the dependency-free fallback. Only downside
   is keeping two paths in sync — mitigate with the cross-check test.
2. If searches must be instant *without* relying on `rg`, or the store gets huge:
   **persistent incremental index** (Idea 2b), hand-rolled gob/JSON to stay
   dependency-free. Skip the pure in-memory index (2a) — it pays the full read
   cost every launch for no persistence benefit.

## Invariants to preserve in any rewrite

- Search must never block the UI thread (keep it a `tea.Cmd`, keep it seq-tagged).
- Multi-term = AND across the whole session (terms need not be adjacent).
- Case-insensitive (ASCII fold is fine; document if a variant changes this).
- Any new engine must return the same session set as `searchContents` — verify
  with a cross-check against the real store before trusting it (see how the
  temporary `TestCrossCheckAgainstNaive` did it during the initial rebuild).
