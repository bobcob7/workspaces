# 0059. The desktop app and Workspace Online show a stats bar and a Stats section, from one `stats_get` command that reads Claude Code's transcripts incrementally and the host's CPU, memory and disk

- Status: Proposed
- Date: 2026-11-12

## Context

### Problem

The user runs many agents at once, on several models, and can't see what
they cost or what they produce: how many workspaces are working, how many
tokens went where, how well the prompt cache is doing, whether the
machine is out of CPU, memory or disk, and above all which model writes
which code how fast. They want to be able to say "62% of Go lines were
written by Haiku agents at 9 lines a minute, while Sonnet needs 15
minutes for 100 lines of Go". How does the app gather those numbers,
cheaply and the same way in the Tauri app and in Workspace Online
([0053](0053-workspace-online.md))?

### What a solution needs

1. **Glanceable and always there**: a bar at the bottom of every
   section, in both apps, at desktop and 360px phone width.
2. **Detail on demand**: a Stats section with system CPU, memory and
   disk; requests, sessions, tokens and cost by model; tokens by kind
   (input, output, cache read, cache write); lines of code by language.
3. **Correlation**: per model and language, lines written, agent
   minutes, tokens and estimated cost, and the ratios that compare
   models: share of a language's lines, lines per minute, minutes per
   100 lines, tokens per line, cost per line.
4. **Cheap to refresh**: polling every few seconds must not re-read
   gigabytes of transcripts.
5. **Reuse what exists**: the listing the app already polls, Claude
   Code's transcripts, the existing command and transport plumbing; no
   new daemon, no new store a user has to manage.
6. **Small**: no new npm packages; at most one direct cargo dependency,
   already in the lock file.

### Relevant facts

- The app polls `list_entries` every 4 s (`pollInterval` in `App.tsx`);
  each `Entry` has `workspaceId` and `status`, so active and total
  workspace counts need no backend change[^app].
- No code in the repo reads token usage today. `internal/claude/transcript.go`
  reads only a transcript's last usage-limit error, and
  `desktop/src-tauri/src/transcript.rs` turns a transcript into
  conversation items; neither looks at `message.usage`[^grep-usage].
  `transcript.rs` already finds Claude Code's config dir as the parent of
  `jobs_dir` and reads whole lines from a byte offset (`read_from`), a
  pattern this ADR reuses[^transcript].
- Claude Code writes one JSONL file per session in
  `$CLAUDE_CONFIG_DIR/projects/<dir>/<session>.jsonl` (default
  `~/.claude`), and a subagent's in
  `projects/<dir>/<session>/subagents/<agent>.jsonl`. On the measuring
  host there were 112 files, 134.6 MB and 35,754 lines; a full Python
  parse of them took 4.05 s[^measured].
- An `assistant` line carries `message.model`, `message.id`,
  `requestId`, `timestamp` and `message.usage` with `input_tokens`,
  `output_tokens`, `cache_read_input_tokens`,
  `cache_creation_input_tokens`, `cache_creation.ephemeral_5m_input_tokens`,
  `cache_creation.ephemeral_1h_input_tokens` and
  `output_tokens_details.thinking_tokens`. Claude Code writes one line
  per content block, so a request with three blocks repeats its usage
  three times: 3,843 repeated `(message.id, requestId)` pairs, all with
  identical usage[^measured]. Models seen: `claude-haiku-5-5` (3,845
  lines), `claude-sonnet-5-5` (2,387), `claude-opus-5-5` (507) and
  `<synthetic>` (6, no real request)[^measured].
- A successful `Edit` or `Write` is answered by a `user` line whose
  `tool_result` block names the `tool_use_id`, and whose top-level
  `toolUseResult` has `filePath` and `structuredPatch` (hunks whose
  `lines` start with `+`, `-` or a space). A `Write` that creates a file
  has `type: "create"`, an empty `structuredPatch` and the whole
  `content`. 483 such results were seen, all with `structuredPatch`[^measured].
- Prices per million tokens (input, output, 5-minute cache write, 1-hour
  cache write, cache read): Opus 5.5 $4, $20, $5, $8, $0.20; Sonnet 5.5
  $2, $10, $2.50, $4, $0.10; Haiku 5.5 $0.10, $0.50, $0.125, $0.20,
  $0.01, and $0.50, $2.50, $0.625, $1, $0.05 for a request whose prompt
  is over 100K tokens; Fable 5.2 $8, $40, $10, $16, $0.20[^prices].
  Cache writes cost 1.25× (5 minutes) and 2× (1 hour) the input price.
- The `libc` crate 0.2.191 is already in `Cargo.lock` (pulled in by
  tokio and portable-pty). On Apple targets it exposes `sysctlbyname`,
  `mach_host_self`, `host_statistics` with `HOST_CPU_LOAD_INFO`,
  `host_statistics64` with `HOST_VM_INFO64` and `vm_statistics64`; on
  every Unix, `statvfs` and `getloadavg`[^libc]. `sysinfo` 0.40 needs
  Rust 1.95, above the crate's `rust-version = "1.90"`; 0.38 accepts 1.88
  but brings `objc2-core-foundation` and `objc2-io-kit` on macOS[^sysinfo].
- Every command goes through `call()` in `api.ts`, which picks Tauri's
  `invoke` or `POST /api/invoke/<command>`; a new command is one
  `#[cfg_attr(feature = "desktop", tauri::command)]` function, one
  `generate_handler!` entry, and one `dispatch` arm plus a `COMMANDS`
  name, checked against each other by `commands_match_generate_handler`[^dispatch].
- Below 720px the rail is a bottom navigation bar inside `.body` with
  `order: 2` and the safe-area inset as its bottom padding[^rail].

## Decision

We will add one backend command, `stats_get`, in a new
`desktop/src-tauri/src/stats/` module and a small `host.rs`, and two
frontend pieces sharing one slice: a **stats bar** at the bottom of the
app and a **Stats** section in the rail. Go (`ws`) doesn't change. The
exact JSON is in [the reference](../design/stats/reference.md); the tasks
are in [tasks.md](../design/stats/tasks.md).

### What the bar and the section show

**Bar** (always shown, last child of the app root), for **today** (local
midnight to now):

| Item | Source |
|------|--------|
| `3 / 14 workspaces` (active / total) | `agents.entries`, no backend |
| `1.2M tokens` | `usage.totals.totalTokens` |
| `cache 93%` | `usage.totals.cacheHitRatio` |
| `$4.12` | `usage.totals.costUsd` |
| `CPU 23%`, `RAM 61%`, `disk 48%` | `system` |

Below 720px only the first three are shown, on one 24px line. Above
720px the bar is a button ("Open Stats") that chooses the Stats section.

**Stats section** (rail entry between Loops and Settings, `⌘5`; Settings
becomes `⌘6`). Its list column picks the window: Today, Last 7 days,
Last 30 days, All. Its pane shows cards: System; Usage by model; Tokens by
kind; Lines by language; Productivity (model × language table, with one
sentence per language in the user's own form, such as "Go: Haiku 5.5
wrote 62% of lines at 9.1 lines/min; Sonnet 5.5 takes 15 min per 100
lines").

### Metric definitions

All counts are over the **window**: requests and edits whose hour bucket
(UTC hour of the line's `timestamp`) starts at or after
`floor_hour(sinceMs)`; `sinceMs = null` is everything still on disk.

- **Request**: an `assistant` line with `message.usage` and a model
  other than `<synthetic>`, counted once per `(message.id, requestId)`
  (later repeats are skipped). Sidechain lines count: they're real
  requests.
- **Tokens**: `inputTokens` = `input_tokens` (uncached input);
  `outputTokens` = `output_tokens`; `thinkingTokens` =
  `output_tokens_details.thinking_tokens` (a part of output, not added
  again); `cacheWriteTokens` = `ephemeral_5m + ephemeral_1h`, or
  `cache_creation_input_tokens` when the split is absent;
  `cacheReadTokens` = `cache_read_input_tokens`; `totalTokens` = input +
  output + cache write + cache read.
- **Cache hit ratio** = cacheRead / (input + cacheRead + cacheWrite);
  null when that's 0.
- **Cost (USD, estimated)** per request = Σ tokens × the model's rate in
  the price table above; 5m and 1h writes priced separately; Haiku 5.5
  uses its long-context rates when input + cache read + cache write >
  100,000. A model is matched by id prefix (`claude-haiku-5-5`,
  `claude-sonnet-5-5`, `claude-opus-5-5`, `claude-fable-5-2`); any other
  model costs 0, is marked `priced: false`, and adds a warning. Costs
  are API-equivalent estimates, not what a subscription bills.
- **Session**: one transcript file (a subagent's file is its own
  session). `sessions` for a model counts files with at least one of its
  requests in the window.
- **Agent minutes (active time)**: within one file, take the
  `timestamp`s of its `user` and `assistant` lines in file order. Each
  gap between consecutive timestamps of 0 to 300 s (`IDLE_CAP`) is
  active time; a longer gap is idle and counts 0. The gap is added to
  the hour bucket of the later line, under the file's **current model**:
  the model of the latest request seen in that file, counting the later
  line itself when it is a new request (so the wait for a reply counts
  for the model that replied). Gaps between lines that all come before
  the file's first request count nowhere. A line without a parsable
  `timestamp` leaves the previous timestamp in place. Sessions running at the same time
  each count their own time, so agent minutes can exceed wall-clock
  minutes; that's intended (it's agent effort, the cost of parallelism).
- **Lines of code**: for each `tool_use` named `Edit`, `MultiEdit`,
  `Write` or `NotebookEdit` in an `assistant` line, remember
  `(tool_use_id → model, file path)`. When a `user` line's `tool_result`
  answers it and `is_error` isn't true, count:
  - if `toolUseResult.structuredPatch` is a non-empty array: `linesAdded`
    = hunk lines starting with `+`, `linesRemoved` = those starting with
    `-`;
  - else if `toolUseResult.type == "create"` and `content` is a string:
    `linesAdded` = its line count;
  - else from the `tool_use` input: `Write` `content`, `Edit`
    `new_string`/`old_string`, `MultiEdit` each `edits[].new_string`/
    `old_string`, `NotebookEdit` `new_source` (added only).
  Line count of a string = number of `\n`, plus 1 if it's non-empty and
  doesn't end with `\n`. Each answered call is one **edit**, attributed
  to the model that made the call and the hour of the result. An
  unanswered call (session killed) counts nothing.
- **Language** of a file path: by lowercased extension, or by base name
  for `Makefile` and `Dockerfile`, from the table in the reference
  (`go` → Go, `rs` → Rust, `ts`/`tsx` → TypeScript, `md` → Markdown, …);
  anything else is `Other`.
- **Productivity rows** (model × language). Within each file and model,
  agent minutes, tokens and cost are split across languages in
  proportion to lines changed (added + removed) in that language; a file
  and model with no changed lines gives nothing to any language (its
  numbers stay in the by-model totals). Then per row:
  - `shareOfLanguage` = linesAdded / all models' linesAdded for that
    language;
  - `locPerMinute` = linesAdded / activeMinutes;
  - `minutesPer100Loc` = 100 × activeMinutes / linesAdded;
  - `tokensPerLoc` = tokens / linesAdded;
  - `costPerLoc` = costUsd / linesAdded;
  each null when its denominator is 0.

Lines are lines the agent's edits added, not lines that survived to a
merge: a line rewritten three times counts three times. That's the honest
reading of a transcript, and the UI calls them "lines written".

### Reading transcripts: incremental cache

`stats::Cache` lives in `AppState` (`Arc`, a `std::sync::Mutex` inside)
and is shared by both builds. It holds, per transcript path, a
`FileState`:

- `len`, `modified` (mtime) and, on Unix, `(dev, ino)` from the last
  read; `offset`, the byte after the last complete line read;
- the parser's carry-over: `current_model`, `last_ts_ms`, the last 32
  request keys (repeats are adjacent), and pending tool calls (at most
  1,000, oldest dropped);
- `buckets`: `BTreeMap<(hour_ms, model), Bucket>` with requests, the five
  token counts, thinking tokens, cost, active ms, and per language
  `{linesAdded, linesRemoved, edits}`.

A refresh lists `projects/*/*.jsonl` and
`projects/*/*/subagents/*.jsonl` under the Claude config dir (the parent
of `jobs_dir`), then per file:

1. Unchanged `len` and `modified`: skip, no read.
2. Shorter than `offset`, or another `(dev, ino)`: drop its state, read
   from 0.
3. Otherwise seek to `offset`, read to EOF, parse only up to the last
   `\n`, and advance `offset`.

Files no longer listed are dropped, so "All" means what Claude Code
still keeps (its `cleanupPeriodDays`). A line is JSON-parsed only if it
contains `"type":"assistant"` or `"type":"user"` (Claude Code writes
compact JSON), which skips the large `api-request-blob` and bookkeeping
lines. A refresh within 5 s of the last one reuses the buckets
(`MIN_REFRESH`), so the bar and the section polling together scan once.
Aggregating the buckets for a window is in memory and runs on every
call. The command runs in `spawn_blocking`.

The cache is in memory only: after a restart the first call reads every
transcript once (measured 4 s in Python for 135 MB; Rust's serde_json
with the prefilter is expected well under that, and the bar shows "—"
until it returns). Persisting it is deferred until a user has enough
transcripts for that to hurt.

### System stats: `/proc` on Linux, Mach and sysctl on macOS, through `libc`

`host.rs` returns `HostStats` and keeps the previous CPU tick sample in
`stats::Cache` so CPU% is the busy share **between two calls** (null on
the first call):

| Metric | Linux | macOS |
|--------|-------|-------|
| CPU ticks | first `cpu` line of `/proc/stat`: busy = user + nice + system + irq + softirq + steal, total = busy + idle + iowait | `host_statistics(mach_host_self(), HOST_CPU_LOAD_INFO)`: busy = user + system + nice, total = busy + idle |
| CPU count | `std::thread::available_parallelism` | same |
| Load average | `libc::getloadavg` | same |
| Memory | `/proc/meminfo` `MemTotal`, used = `MemTotal − MemAvailable` | `sysctlbyname("hw.memsize")`; used = total − (free + inactive + speculative + purgeable) × page size from `host_statistics64(HOST_VM_INFO64)` and `sysconf(_SC_PAGESIZE)` |
| Disk | `libc::statvfs` on `$WS_CONFIG_DIR`, else `$HOME`: total = `f_blocks × f_frsize`, used = total − `f_bavail × f_frsize` | same |

In the Workspace Online container these are the container's view: the
host's CPUs and memory unless cgroups limit them (`/proc/meminfo` isn't
namespaced), and the `/data` volume's disk when `WS_CONFIG_DIR=/data/ws`.

Parsing `/proc/stat` and `/proc/meminfo` is pure and unit-tested on
fixture text; the Mach calls are about 40 lines of `unsafe` behind
`#[cfg(target_os = "macos")]`, compiled by the macOS CI job. Any
platform or call that fails gives nulls, never an error: the bar
shouldn't fail because a number is missing.

### Transport and polling

- **Command**: `stats_get(since_ms: Option<i64>) -> Stats`; JSON args
  `{"sinceMs": 1731369600000}` or `{"sinceMs": null}`. Registered in
  `generate_handler!` and as a `dispatch` arm (`"stats_get"`, added to
  `COMMANDS`), so the desktop app calls it with `invoke` and Workspace
  Online with `POST /api/invoke/stats_get`, both behind the same
  auth as every other command. No new route, event or WebSocket frame.
  The only error is `internal` (the blocking task panicked); a missing
  Claude config dir or a failed system call gives zeros or nulls and a
  line in `warnings`, so the bar always has the numbers it can get.
- **Frontend**: `api.statsGet(sinceMs)`; `features/stats/statsSlice.ts`
  keeps `results` keyed by window (`today`, `7d`, `30d`, `all`), each
  `{ data, error, loading }`, plus the chosen `window`. `sinceMs` is
  computed in the browser (`today` = local midnight), so the user's time
  zone wins over the server's.
- **Polling**: the bar fetches `today` on mount and every 15 s
  (`statsPollInterval`); the Stats pane fetches its window on mount, on
  window change, and every 15 s while mounted. Both skip a tick while
  `document.hidden` or while that window's fetch is in flight. A failed
  fetch keeps the last data and shows "—" only when there is none.

### Why this option

1. **Glanceable**: the bar uses data the app already has plus one cheap
   call; it fits 360px by dropping to three items.
2. **Detail**: one response carries every card, so the section is one
   fetch per window.
3. **Correlation**: lines, minutes, tokens and cost are all bucketed by
   file and model, so they can be split by language within a session,
   which is what makes "Haiku: 100 Go lines in 11 minutes" a real
   measurement rather than two unrelated totals. Weak spot: a session
   that edits Go and Markdown splits its time by lines, not by the time
   each edit really took.
4. **Cheap**: an unchanged file costs one `stat`; a growing one, only
   its new bytes. Weak spot: the first call after a restart reads
   everything.
5. **Reuse**: the transcripts Claude Code already writes, the listing
   the app already polls, the existing command plumbing and the existing
   config-dir lookup; no daemon, no store.
6. **Small**: `libc` becomes a direct dependency at the version already
   locked; no npm package; the charts are CSS bars, not a chart library.

## Alternatives

### `ws stats --json` in Go, called by the desktop backend

**Pros**

- `ws` stays the one place that knows workspaces; the CLI gets stats too.

**Cons**

- A new process per poll can't keep an in-memory cache, so it would need
  an on-disk cache with locking, written by several callers; that's the
  "new store" criterion 5 avoids. Transcript reading already lives in the
  Rust backend (`transcript.rs`). It can be added later on top of the same
  definitions.

### The `sysinfo` crate for system stats

**Pros**

- One tested API for CPU, memory and disk on every OS; no `unsafe` in the
  repo.

**Cons**

- 0.39+ needs Rust 1.95 (above the crate's 1.90); 0.38 pulls in
  `objc2-core-foundation` and `objc2-io-kit` on macOS for four numbers.
  `libc` is already compiled into both binaries.

### Ask Anthropic's usage or cost APIs

**Pros**

- Exact billed numbers.

**Cons**

- Needs an Admin API key a subscription user doesn't have, gives no
  per-session or per-language data, and needs the network.

### Per-request records instead of hourly buckets

**Pros**

- Exact window edges.

**Cons**

- Memory grows with every request (millions for a heavy user); hourly
  buckets per file and model stay small and are exact to the hour.

### A Prometheus exporter or Grafana dashboard

**Pros**

- History and charts for free where Grafana runs.

**Cons**

- A new daemon and a server most desktop users don't have; doesn't put
  numbers in the app.

## Consequences

- New `desktop/src-tauri/src/stats/` (`mod.rs`, `ingest.rs`, `lang.rs`,
  `price.rs`, `time.rs`, `cache.rs`, `aggregate.rs`) and `host.rs`;
  `AppState` gains `stats: Arc<stats::Cache>`; `libc = "0.2"` becomes a
  direct dependency. New frontend `features/stats/` (slice, bar, list,
  pane, cards, pure `format`/`insights` helpers); `Section` gains
  `"stats"`; the Tauri mock answers `stats_get`.
- Easier: answering "which model should write this" with numbers; seeing
  a full disk or swapping host before agents fail.
- Harder: the transcript format is undocumented, so a Claude Code update
  can rename a field. Unknown fields are ignored; a missing `usage`
  shows as zero tokens, not an error. The fixtures in `ingest.rs` tests
  pin the format seen on 2026-11-12, and the price table must be updated
  by hand when prices change (one `const` table in `price.rs`).
- Conformance: Rust unit tests for line parsing (dedup, synthetic,
  cache split, long-context Haiku, edits by patch, create and fallback,
  errors ignored, active-time gaps), incremental reads (append, shrink,
  replace, removal, throttle), aggregation and every ratio's null case,
  `/proc` parsers; a dispatch test for `stats_get`; vitest for the
  slice, formatting, insights, the bar at desktop and phone, and the
  section. Screenshots at 1280 and 360px from `test/tauri-mock.js`.
- **Unverified**: the Rust time for a first full read (expected under
  1 s for the 135 MB measured here); the macOS Mach calls until the
  macOS CI job and a real Mac run them; whether hour buckets are fine for
  users in half-hour time zones ("Today" can start up to 30 minutes
  early there).

## References

[^app]: `desktop/src/App.tsx` (`pollInterval = 4000`, `fetchEntries`) and `desktop/src/api.ts` (`Entry.workspaceId`, `Entry.status`), read 2026-11-12 at `b2b239d`.
[^grep-usage]: `grep -rn -i "usage\|input_tokens\|cache_read" desktop/src desktop/src-tauri/src internal` on 2026-11-12 at `b2b239d`: only usage-limit and CLI usage-error matches.
[^transcript]: `desktop/src-tauri/src/transcript.rs` (`read_from`, `transcript_path`) and `commands.rs` (`conversation`: "let config_dir = jobs_dir.parent()"), read 2026-11-12 at `b2b239d`.
[^measured]: Measured 2026-11-12 on the ws-online pod (Linux 6.18, 48 CPUs, 128 GB), Python 3 with `json`, over every `*.jsonl` under `~/.claude/projects`, once each: counts of line types, models, tool names, repeated `(message.id, requestId)` usage, and `toolUseResult` shapes. The host's transcripts are mostly from orchestrated builds of this repo, so the model mix is not typical.
[^prices]: Anthropic, `claude-api` skill bundled with Claude Code 2.1.322, "Current Models (cached: 2026-11-03)" (accessed 2026-11-12): "Successor to Claude Opus 5 … at a lower price ($4 / $20 per MTok, cache reads $0.20)"; Sonnet 5.5 "same prices ($2 / $10 per MTok), except cache reads at $0.10"; Haiku 5.5 "Prices above are for prompts up to 100K tokens ($0.50 / $2.50 beyond)"; Fable 5.2 "$8 / $40 per MTok, cache reads $0.20". Cache-write multipliers and Haiku's cache rates from the Agent Handbook's "Procedure: multi-agent feature build" `metrics.py` rate table (2026-10-06).
[^libc]: `libc` 0.2.191 source, `src/unix/bsd/apple/mod.rs` (`host_cpu_load_info`, `vm_statistics64`, `HOST_CPU_LOAD_INFO`, `HOST_VM_INFO64`, `host_statistics`, `host_statistics64`, `mach_host_self`, `sysctlbyname`) and `src/unix/mod.rs` (`statvfs`), read 2026-11-12; `desktop/src-tauri/Cargo.lock` `name = "libc" version = "0.2.191"`.
[^sysinfo]: `cargo info sysinfo@0.40.3` ("rust-version: 1.95"), `sysinfo@0.39.0` ("1.95"), `sysinfo@0.38.0` ("1.88"), run 2026-11-12; feature lists from the same output.
[^dispatch]: ADR [0053](0053-workspace-online.md) "Transport", and `desktop/src-tauri/src/online/dispatch.rs` (`COMMANDS`, `commands_match_generate_handler`), read 2026-11-12 at `b2b239d`.
[^rail]: `desktop/src/features/rail/Rail.module.css` (`@media (max-width: 720px)`: `order: 2`, `padding: 2px 6px calc(2px + env(safe-area-inset-bottom))`), read 2026-11-12 at `b2b239d`.
