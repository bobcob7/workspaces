# Stats: implementation tasks

Implements [ADR 0059](../../adrs/0059-stats.md) to the contract in
[reference.md](reference.md). Each task builds, passes its tests, and can
be merged on its own. Each task is one commit on its own branch
(conventional commit, imperative mood, for example
`feat: parse token usage and edits from transcripts (ADR 0059)`).

**Order and parallelism**

| Task | Title | Depends on | Size | Parallel with |
| ---- | ----- | ---------- | ---- | ------------- |
| T1 | Rust: transcript line parser, languages, prices, time | nothing | medium | T2, T5 |
| T2 | Rust: host CPU, memory and disk (`host.rs`) | nothing | `small` | T1, T3, T5 |
| T3 | Rust: incremental transcript cache and aggregation | T1 | medium | T2, T5, T6 |
| T4 | Rust: `stats_get` command in both builds | T2, T3 | medium | T5, T6, T7 |
| T5 | Frontend data: API types, slice, polling hook, formatters, fakes, Tauri mock | nothing (contract in reference.md) | medium | T1, T2, T3, T4 |
| T6 | UI: stats bar and Stats section (System, Usage), working at 360px | T5 | large | T3, T4 |
| T7 | UI: model, token-kind, language and productivity cards; README | T6 | medium | T4 |

T1 → T3 → T4 is the Rust chain; T2 joins at T4. T5 → T6 → T7 is the
frontend chain. The chains touch disjoint files (`desktop/src-tauri/`
vs `desktop/src/` and `desktop/test/`), so T5 can start at once, and T6
and T7 can run beside T3 and T4. T1 ∥ T2: both add one `mod` line to
`lib.rs`, in different places (`mod host;` after `mod env;`,
`mod stats;` after `mod runner;`), so they rebase cleanly.

**Memory**: on an 8 GB host run at most one heavy task at a time (cargo
or vitest); pair a Rust task with a frontend one only when the host has
the memory (`free -m`).

**Read before any task:** the "Decision" section of ADR 0059 and all of
`reference.md`. When this file and those two disagree, they win.

## Rules for every task

- **Follow the existing code** in the files you edit: naming, a short
  doc comment on every `pub` item or exported symbol, no other comments
  unless the logic isn't obvious, file layout, test style. Prefer early
  returns. Keep functions small.
- **Rust** (`desktop/src-tauri/`): `cargo fmt` clean; `cargo clippy
  --all-targets -- -D warnings` and `cargo clippy --no-default-features
  --features online --all-targets -- -D warnings` clean. Unit tests in
  the file's `#[cfg(test)] mod tests { use super::*; … }`, one behaviour
  per `#[test]`, named as a snake_case sentence
  (`repeated_request_is_counted_once`); filesystem tests use `tempfile`
  (already a dev-dependency) and never real user paths. No
  `unwrap()`/`expect()` outside tests. Never panic on transcript content.
  **No new cargo dependency** except `libc` in T2.
- **TypeScript** (`desktop/src/`): strict, `readonly` fields, no `any`;
  components are `export const X = (props): JSX.Element =>`; styles in a
  CSS module using the existing variables (`--border`, `--panel`,
  `--text-muted`, `--accent`). Tests: vitest, `@testing-library/react`,
  `@testing-library/user-event`, helpers in `src/test/fixtures.ts` and
  `src/test/render.tsx` (`renderWithStore`). Query by role and accessible
  name. Pure modules get plain unit tests; slices are tested by calling
  the reducer. **No Playwright in tests. No new npm packages.**
- **Phone width:** every new button or link is at least 40px tall
  (`min-height: 40px`) under `@media (max-width: 720px)`, and nothing
  makes the page scroll sideways at 360px (wide tables scroll inside
  their own wrapper with `overflow-x: auto`).
- **Run heavy checks one at a time** and on the touched files first:
  `cargo test -j 2 stats::` / `npm test -- --maxWorkers=2 src/features/stats`.
  Commit and push before the full suite. If a command is killed, say so
  in your report instead of claiming it passed.
- Don't touch files a task doesn't list, except to fix a compile error
  the task caused (say so in your report).

---

## T1. Rust: transcript line parser, languages, prices, time

**Depends on:** nothing. **Parallel:** T1 ∥ T2 ∥ T5.

**Files:** new `desktop/src-tauri/src/stats/mod.rs`,
`stats/ingest.rs`, `stats/lang.rs`, `stats/price.rs`, `stats/time.rs`;
edit `desktop/src-tauri/src/lib.rs` (only: add `mod stats;` after
`mod runner;`).

Read first: `desktop/src-tauri/src/transcript.rs` (style, `serde_json::Value`
handling, `str_field`), the "Metric definitions" in the ADR, and
"Transcript lines the parser reads", "Price table" and "Languages" in
reference.md.

### `stats/mod.rs`

```rust
//! Token usage, lines of code and host load for the stats bar and the
//! Stats section (ADR 0059).

pub mod ingest;
pub mod lang;
pub mod price;
pub mod time;
```

(T3 and T4 add more to this file.) Until T4 wires the module up, the
crate-level `#![cfg_attr(not(feature = "desktop"), allow(dead_code))]`
does not cover the desktop build: add `#![allow(dead_code)]` as the
first line of `stats/mod.rs`'s inner attributes; T4 removes it.

### `stats/time.rs`

```rust
/// One hour in milliseconds.
pub const HOUR_MS: i64 = 3_600_000;

/// Parses an RFC 3339 UTC timestamp as Claude Code writes it
/// ("2026-11-12T20:00:01.123Z", also without fraction, or with "+00:00")
/// into Unix milliseconds. Any other offset, or a malformed string, is None.
pub fn parse_utc_ms(s: &str) -> Option<i64>;

/// The start of the UTC hour containing ms (works for negative ms too).
pub fn hour_floor(ms: i64) -> i64; // ms.div_euclid(HOUR_MS) * HOUR_MS
```

Implement the date part with the days-from-civil algorithm (Howard
Hinnant's `days_from_civil`); reject month 0/13, day 0/32, hour ≥ 24,
minute or second ≥ 60. Fractions of any length: use the first three
digits (pad with zeros). No new dependency.

Tests: whole seconds; fraction `.1`, `.123`, `.123456`; `+00:00`;
`1970-01-01T00:00:00Z` is 0; a leap day `2024-02-29T12:00:00Z`
(1709208000000); rejects `+02:00`, a missing `Z`, garbage; `hour_floor`
of a mid-hour value and of a negative value.

### `stats/lang.rs`

```rust
/// The language of a file path for the stats (ADR 0059), from the table in
/// docs/design/stats/reference.md; "Other" when unknown.
pub fn language_of(path: &str) -> &'static str;
```

Take the base name after the last `/` or `\`. Base-name rules first
(`Makefile`, `GNUmakefile` → "Make"; `Dockerfile` or starting with
`Dockerfile.` → "Dockerfile"), then the lowercased extension (after the
last `.`, none if the name starts with `.` and has no other dot). A
`match` on the extension is enough.

Tests: one per language family that has several extensions (`tsx`,
`mjs`, `hpp`, `yml`), case-insensitivity (`MAIN.GO`), `Makefile`,
`Dockerfile.dev`, a dotfile (`.bashrc` → Other), no extension, a Windows
path.

### `stats/price.rs`

```rust
/// USD per million tokens.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Rates {
    pub input: f64,
    pub output: f64,
    pub cache_write_5m: f64,
    pub cache_write_1h: f64,
    pub cache_read: f64,
}

/// A request's token counts, as read from `message.usage`.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct RequestTokens {
    pub input: u64,
    pub output: u64,
    pub thinking: u64,
    pub cache_write_5m: u64,
    pub cache_write_1h: u64,
    pub cache_read: u64,
}

/// Prompts above this many tokens use Haiku 5.5's long-context rates.
pub const HAIKU_LONG_PROMPT: u64 = 100_000;

/// The rates for model at this prompt size (input + cache read + cache
/// writes), or None when no rate is known.
pub fn rates_for(model: &str, prompt_tokens: u64) -> Option<Rates>;

/// The estimated cost in USD of one request; 0.0 when the model has no rates.
pub fn request_cost(model: &str, tokens: &RequestTokens) -> f64;

/// "opus", "sonnet", "haiku", "fable", "mythos" when the model id contains
/// one of them, else "other".
pub fn family(model: &str) -> &'static str;

/// A display name: "claude-haiku-5-5" → "Haiku 5.5", "claude-opus-4-8" →
/// "Opus 4.8"; the model id unchanged when it isn't `claude-<word>-<digits>…`.
pub fn label(model: &str) -> String;
```

Rates are a `const` table of `(prefix, Rates)` from reference.md; Haiku
long-context is a second Haiku entry chosen when `prompt_tokens >
HAIKU_LONG_PROMPT`. `label`: strip `claude-`, split on `-`; the first
part must be alphabetic (capitalise it) and the rest all digits (join
with `.`), else return `model`. `thinking` is not priced separately
(it's part of output).

Tests: each family's rates; Haiku at exactly 100,000 (short) and
100,001 (long); unknown model → None and cost 0; a cost computed by hand
(`claude-sonnet-5-5`, 1M input + 1M output + 1M 5m write + 1M 1h write +
1M read = 2 + 10 + 2.5 + 4 + 0.1 = 18.6); `family` for each word and
"other"; `label` for 5-5, 4-8, `claude-3-5-sonnet-20241022` (unchanged),
`<synthetic>` (unchanged).

### `stats/ingest.rs`

```rust
use std::collections::{BTreeMap, VecDeque};

/// Gaps longer than this between transcript lines are idle (ADR 0059).
pub const IDLE_CAP_MS: i64 = 300_000;
/// How many recent (message id, request id) keys are kept to skip repeats.
pub const MAX_RECENT_KEYS: usize = 32;
/// How many unanswered edit tool calls are kept; the oldest are dropped.
pub const MAX_PENDING_EDITS: usize = 1_000;

/// Lines of one language changed by edits.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct LangLines {
    pub added: u64,
    pub removed: u64,
    pub edits: u64,
}

/// What one model did in one UTC hour of one transcript.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct Bucket {
    pub requests: u64,
    pub tokens: crate::stats::price::RequestTokens, // summed
    pub cost_usd: f64,
    pub active_ms: i64,
    pub lines: BTreeMap<&'static str, LangLines>,  // by language
}

/// (UTC hour start in ms, model id).
pub type BucketKey = (i64, String);

/// An edit tool call waiting for its result.
#[derive(Debug, Clone, PartialEq)]
struct PendingEdit {
    model: String,
    language: &'static str,
    /// Lines counted from the call's input, used when the result has no patch.
    fallback: (u64, u64),
}

/// Reads one transcript's lines in order and keeps its buckets. Feed it
/// lines across several reads; it keeps what it needs between them.
#[derive(Debug, Default)]
pub struct Parser {
    pub buckets: BTreeMap<BucketKey, Bucket>,
    current_model: Option<String>,
    last_ts_ms: Option<i64>,
    recent_keys: VecDeque<(String, String)>,
    pending: VecDeque<(String, PendingEdit)>,
}

impl Parser {
    /// Feeds one line (without its newline). Lines that don't contain
    /// `"type":"assistant"` or `"type":"user"`, or don't parse, are ignored.
    pub fn feed_line(&mut self, line: &[u8]);
}

/// Line count of s: the number of '\n', plus 1 if s is non-empty and
/// doesn't end with '\n'.
pub fn count_lines(s: &str) -> u64;
```

Behaviour of `feed_line`, in this order, for a parsed line `e`:

1. `kind = e["type"]`; only `"assistant"` and `"user"` go on.
   `ts = parse_utc_ms(e["timestamp"])` (may be None).
2. **assistant**: `model = e["message"]["model"]`. If `model` is a
   string other than `"<synthetic>"` and `e["message"]["usage"]` is an
   object: key = (`message.id`, `requestId`) as strings (missing → "").
   If the key is in `recent_keys`, it's a repeat: don't count it.
   Otherwise push it (drop the oldest beyond `MAX_RECENT_KEYS`), set
   `current_model = model`, and when `ts` is Some add to bucket
   `(hour_floor(ts), model)`: `requests += 1`, the `RequestTokens` read
   as in the ADR (`cache_creation.ephemeral_5m_input_tokens` and
   `ephemeral_1h_input_tokens`; when both are absent, the whole
   `cache_creation_input_tokens` is 5m), and `cost_usd +=
   request_cost(model, &tokens)`. Then, for every content block with
   `type == "tool_use"` and `name` in `Edit`, `MultiEdit`, `Write`,
   `NotebookEdit` (and a non-synthetic model string), push
   `(block.id, PendingEdit { model, language_of(path), fallback })`
   where path = `input.file_path` or `input.notebook_path` and
   fallback = `Write`: (`count_lines(content)`, 0); `Edit`:
   (`count_lines(new_string)`, `count_lines(old_string)`); `MultiEdit`:
   the sums over `input.edits[]`; `NotebookEdit`:
   (`count_lines(new_source)`, 0). Drop the oldest beyond
   `MAX_PENDING_EDITS`.
3. **user**: for each block in `e["message"]["content"]` (when it's an
   array) with `type == "tool_result"` whose `tool_use_id` is pending:
   remove it from `pending`; if `is_error` is `true`, count nothing;
   else compute `(added, removed)`: when the line has exactly one
   `tool_result` block and `e["toolUseResult"]` is an object — if its
   `structuredPatch` is a non-empty array, count over every hunk's
   `lines` the strings starting with `+` and with `-`; else if its
   `type == "create"` and `content` is a string, `(count_lines(content), 0)`;
   otherwise use the pending `fallback`. Add to bucket
   `(hour_floor(t), pending.model)` with `t = ts.or(last_ts_ms)` (skip
   if both None): `lines[language] += (added, removed, edits + 1)`.
4. **Active time** (both kinds, after the steps above): if `ts` is Some,
   then if `last_ts_ms` is Some and `current_model` is Some and
   `0 <= ts - last <= IDLE_CAP_MS`, add `ts - last` to bucket
   `(hour_floor(ts), current_model).active_ms`; then `last_ts_ms = Some(ts)`.

The cheap prefilter: before parsing, return unless the bytes contain
`"type":"assistant"` or `"type":"user"` (a plain `windows()` search is
fine). Read numbers with `as_u64()`, treating missing as 0.

Tests (build lines with `serde_json::json!` and `to_string()`; a helper
`fn feed(p: &mut Parser, v: serde_json::Value)`):
- a request's tokens, cost and hour bucket;
- a repeated `(message.id, requestId)` is counted once;
- `<synthetic>` and lines without usage count no request;
- the 5m/1h split, and the fallback to `cache_creation_input_tokens`;
- thinking tokens are summed but not added to cost;
- an `Edit` answered with a `structuredPatch` counts `+`/`-` lines under
  the calling model and the file's language;
- a `Write` answered with `type: "create"` counts its content lines;
- an answer without `toolUseResult` uses the input fallback (`Edit`,
  `MultiEdit`, `NotebookEdit`);
- an `is_error: true` answer counts nothing and clears the pending call;
- an answer to an unknown `tool_use_id` counts nothing;
- active time: gaps ≤ 300 s count under the current model, a 301 s gap
  doesn't, gaps before the first request don't, the reply's own wait
  counts for the replying model, a model switch moves later gaps;
- state carries across calls (tool use in one `feed_line`, result in a
  later one);
- lines without the prefilter's text, and invalid JSON, change nothing;
- `count_lines` for `""`, `"a"`, `"a\n"`, `"a\nb"`.

**Acceptance:** all of the above pass; `Parser` never panics on any of
the test lines; nothing outside `stats/` and the one `lib.rs` line
changes.

**Verify:**

```bash
cd desktop/src-tauri
cargo fmt --check
cargo test -j 2 stats::
cargo clippy --all-targets -- -D warnings
cargo clippy --no-default-features --features online --all-targets -- -D warnings
```

---

## T2. Rust: host CPU, memory and disk — `small`

**Depends on:** nothing. **Parallel:** T2 ∥ T1, T2 ∥ T3, T2 ∥ T5.

**Files:** new `desktop/src-tauri/src/host.rs`; edit
`desktop/src-tauri/Cargo.toml` (add `libc = "0.2"` under
`[dependencies]`, after `portable-pty`); edit
`desktop/src-tauri/src/lib.rs` (only: `mod host;` after `mod env;`).
`Cargo.lock` changes only to list `libc` as a direct dependency (the
locked version stays 0.2.191).

Read first: the "System stats" section of the ADR.

```rust
//! The host's CPU, memory and disk for the stats (ADR 0059): /proc on
//! Linux, Mach and sysctl on macOS, statvfs and getloadavg on both.

/// Cumulative CPU ticks: busy and total, across all CPUs.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct CpuTicks {
    pub busy: u64,
    pub total: u64,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DiskStats { pub path: String, pub total_bytes: u64, pub used_bytes: u64 }

#[derive(Debug, Clone, Default, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct HostStats {
    pub cpu_percent: Option<f64>,
    pub cpu_count: Option<u32>,
    pub load_average: Option<[f64; 3]>,
    pub memory_total_bytes: Option<u64>,
    pub memory_used_bytes: Option<u64>,
    pub disk: Option<DiskStats>,
}

/// Parses the first `cpu ` line of /proc/stat: busy = user + nice + system
/// + irq + softirq + steal; total = busy + idle + iowait.
pub fn parse_proc_stat(text: &str) -> Option<CpuTicks>;

/// Parses /proc/meminfo: (MemTotal, MemTotal − MemAvailable), in bytes.
pub fn parse_meminfo(text: &str) -> Option<(u64, u64)>;

/// Busy share between two samples, 0–100, rounded to one decimal; None
/// when total didn't grow or busy went backwards.
pub fn cpu_percent(prev: CpuTicks, now: CpuTicks) -> Option<f64>;

/// The current CPU ticks (Linux: /proc/stat; macOS: HOST_CPU_LOAD_INFO).
pub fn read_cpu_ticks() -> Option<CpuTicks>;

/// (total, used) memory in bytes (Linux: /proc/meminfo; macOS: hw.memsize
/// and HOST_VM_INFO64).
pub fn read_memory() -> Option<(u64, u64)>;

/// statvfs of path: total = f_blocks × f_frsize, used = total − f_bavail × f_frsize.
pub fn read_disk(path: &std::path::Path) -> Option<DiskStats>;

/// The 1, 5 and 15 minute load averages from getloadavg.
pub fn load_average() -> Option<[f64; 3]>;

/// $WS_CONFIG_DIR when set and non-empty, else $HOME.
pub fn disk_path() -> Option<std::path::PathBuf>;

/// Takes one sample. prev is the CPU ticks from the last call; returns the
/// stats and the ticks to keep for the next call.
pub fn sample(prev: Option<CpuTicks>, disk: Option<&std::path::Path>) -> (HostStats, Option<CpuTicks>);
```

Platform code:

- `#[cfg(target_os = "linux")]` `read_cpu_ticks`/`read_memory` read the
  files with `std::fs::read_to_string` and call the parsers.
- `#[cfg(target_os = "macos")]`:
  - CPU: `let mut info: libc::host_cpu_load_info = std::mem::zeroed();
    let mut count = libc::HOST_CPU_LOAD_INFO_COUNT;
    libc::host_statistics(libc::mach_host_self(), libc::HOST_CPU_LOAD_INFO,
    &mut info as *mut _ as libc::host_info_t, &mut count)`; on
    `KERN_SUCCESS`, `cpu_ticks[CPU_STATE_USER] + [CPU_STATE_SYSTEM] +
    [CPU_STATE_NICE]` is busy and busy + `[CPU_STATE_IDLE]` is total.
  - Memory: `sysctlbyname(c"hw.memsize", …)` into a `u64`; then
    `host_statistics64(mach_host_self(), HOST_VM_INFO64, … vm_statistics64,
    HOST_VM_INFO64_COUNT)`; free = (free_count + inactive_count +
    speculative_count + purgeable_count) × `sysconf(_SC_PAGESIZE)`; used =
    total − min(free, total).
  - Each `unsafe` block gets a `// SAFETY:` line.
- Any other OS: those two return None.
- `read_disk`, `load_average` use `libc::statvfs` / `libc::getloadavg`
  under `#[cfg(unix)]`, None elsewhere. Use `u64::from`/`try_from` and
  `saturating_sub`, never `as` casts that could truncate silently
  (clippy pedantic isn't on, but keep it clean).
- `cpu_count`: `std::thread::available_parallelism()` as `u32`.
- Nothing calls this module until T4, and the desktop build has no
  crate-wide `dead_code` allowance, so start `host.rs` with
  `#![allow(dead_code)]` after its `//!` doc; T4 removes it.

Tests (pure functions only; no assertions on the real host's numbers
except "doesn't panic"):
- `parse_proc_stat` on a fixture `"cpu  10 2 3 100 5 1 1 1 0 0\ncpu0 …"`
  → busy 18 (10+2+3+1+1+1), total 123 (18+100+5); missing `cpu` line →
  None; short line → None;
- `parse_meminfo` on a fixture with `MemTotal: 16384 kB` and
  `MemAvailable: 4096 kB` → (16 MiB, 12 MiB); missing MemAvailable →
  None;
- `cpu_percent` 50% case, rounding to one decimal, zero delta → None,
  counter going backwards → None;
- `read_disk` on a `tempfile::tempdir()` gives total ≥ used (Unix);
- `sample(None, …)` gives `cpu_percent: None` and returns ticks on Linux;
  `sample(Some(ticks), …)` doesn't panic.

**Acceptance:** tests pass; `cargo build` passes for both features on
Linux; the macOS block compiles in the macOS CI job (if you can, also run
`rustup target add aarch64-apple-darwin && cargo check --lib
--no-default-features --features online --target aarch64-apple-darwin`;
if that fails for lack of a target toolchain, say so in the report).

**Verify:**

```bash
cd desktop/src-tauri
cargo fmt --check
cargo test -j 2 host::
cargo clippy --all-targets -- -D warnings
cargo clippy --no-default-features --features online --all-targets -- -D warnings
```

---

## T3. Rust: incremental transcript cache and aggregation

**Depends on:** T1. **Parallel:** T3 ∥ T2, T3 ∥ T5, T3 ∥ T6.

**Files:** new `desktop/src-tauri/src/stats/cache.rs`,
`stats/aggregate.rs`; edit `stats/mod.rs` (add `pub mod aggregate;` and
`pub mod cache;`).

Read first: `stats/ingest.rs` (T1), the "Reading transcripts" and
"Metric definitions" sections of the ADR, and the Rust table in
reference.md.

### `stats/cache.rs`

```rust
/// A refresh within this long of the last one reuses what was read.
pub const MIN_REFRESH_MS: i64 = 5_000;

#[derive(Debug, Clone, Default, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ScanInfo { pub files: u64, pub files_read: u64, pub bytes_read: u64, pub duration_ms: u64, pub cached: bool }

/// What was read of one transcript file.
#[derive(Debug, Default)]
pub struct FileState {
    len: u64,
    modified: Option<std::time::SystemTime>,
    /// (dev, ino) on Unix; None elsewhere.
    id: Option<(u64, u64)>,
    offset: u64,
    pub parser: Parser,
}

/// Every transcript's state, refreshed incrementally.
#[derive(Debug, Default)]
pub struct TranscriptCache {
    files: BTreeMap<PathBuf, FileState>,
    last_refresh_ms: Option<i64>,
}

impl TranscriptCache {
    /// Brings every transcript under config_dir/projects up to date
    /// (ADR 0059 steps 1–3), unless the last refresh was under
    /// MIN_REFRESH_MS before now_ms, which returns cached: true.
    pub fn refresh(&mut self, config_dir: &Path, now_ms: i64) -> ScanInfo;
    /// The parsers, one per transcript file.
    pub fn parsers(&self) -> impl Iterator<Item = &Parser>;
}

/// projects/*/*.jsonl and projects/*/*/subagents/*.jsonl under config_dir,
/// sorted; an unreadable directory is skipped.
pub fn transcript_files(config_dir: &Path) -> Vec<PathBuf>;
```

`refresh`: list files; drop states whose path is no longer listed; for
each file take `metadata()` (skip on error), compute `id` with
`std::os::unix::fs::MetadataExt` (`dev()`, `ino()`) under
`#[cfg(unix)]`; if `len`, `modified` and `id` equal the stored ones,
skip; if `len < offset` or `id` changed, replace the state with a fresh
one; then open, seek to `offset`, `read_to_end`, find the last `\n`,
feed every complete line to the parser, and set `offset += complete`,
`len`, `modified`, `id`. Count `files_read` and `bytes_read` (bytes of
complete lines). Measure `duration_ms` with `std::time::Instant`. A
read error leaves that file's state as it was.

Tests (all with `tempfile::tempdir()` as the config dir and helper
lines from T1's shape; `#[test]`, not async):
- finds top-level and `subagents` transcripts, ignores other files;
- a second refresh after `MIN_REFRESH_MS` with no change reads 0 files;
- a refresh inside `MIN_REFRESH_MS` returns `cached: true`;
- appending a line reads only the new bytes (`bytes_read` = its length
  + 1) and adds its request;
- a partial last line (no `\n`) isn't counted until it's completed;
- a file truncated and rewritten shorter is read again from 0 (no
  double counting);
- a deleted file disappears from `parsers()`.

### `stats/aggregate.rs`

Types exactly as in reference.md's Rust table (`TokenCounts`,
`ModelUsage`, `LanguageLines`, `Usage`, `ProductivityRow`), plus:

```rust
/// What `stats_get` reports about usage for a window.
#[derive(Debug, Clone, PartialEq)]
pub struct Aggregate {
    pub usage: Usage,
    pub productivity: Vec<ProductivityRow>,
    pub warnings: Vec<String>,
}

/// Sums the buckets of every parser whose hour is at or after
/// hour_floor(since_ms) (all of them when None), and derives the
/// productivity rows (ADR 0059 "Metric definitions").
pub fn aggregate<'a>(parsers: impl IntoIterator<Item = &'a Parser>, since_ms: Option<i64>) -> Aggregate;

/// a / b, or None when b is 0 (or not finite).
fn ratio(a: f64, b: f64) -> Option<f64>;
```

Algorithm:

1. For each parser (one session), for each bucket in the window, sum per
   model: `TokenCounts` (requests, tokens, cost), `active_ms`, and
   `lines` per language. Then per (session, model) with any request or
   active time: add to that model's `ModelUsage` (`sessions += 1` only if
   it had a request in the window). If the session's changed lines
   (added + removed, all languages) for that model, `W`, is > 0, for each
   language with `w = added + removed`: add to row (model, language)
   `linesAdded`, `linesRemoved`, `edits`, and `activeMinutes +=
   active_ms / 60000 × w / W`, `tokens += totalTokens × w / W` (as
   `f64`, rounded to the nearest integer at the end), `costUsd += cost × w / W`.
2. `totals` = sum of all models; `sessions` = sessions with any request in
   the window; `activeMinutes` = sum over models.
3. `cacheHitRatio` = `ratio(read, input + read + write)` on every
   `TokenCounts`.
4. Derived row fields with `ratio`: `shareOfLanguage` = linesAdded /
   Σ linesAdded of that language; `locPerMinute`, `minutesPer100Loc`
   (= 100 × minutes / lines), `tokensPerLoc`, `costPerLoc`.
5. `label`, `family`, `priced` (`price::rates_for(model, 0).is_some()`)
   from `price`. For each unpriced model with requests, a warning
   `"no price for <model>; its cost shows as $0"`.
6. Sort as reference.md says (byModel, byLanguage, productivity).

Tests (build `Parser`s by feeding T1-style lines, or construct
`Parser { buckets, ..Default::default() }` directly — make `buckets`
the only field you need):
- the window excludes buckets before `hour_floor(since_ms)` and includes
  the hour containing `since_ms`;
- totals and per-model counts; `sessions` counts files, not requests;
- cache hit ratio, and `None` with no input;
- one session with Go 30 lines and Markdown 10 lines changed and 40
  minutes active → Go row gets 30 minutes, Markdown 10; tokens and cost
  split the same way;
- a session with no edits adds nothing to productivity but counts in
  `byModel`;
- `shareOfLanguage` across two models (e.g. 62/38);
- every ratio is `None` when its denominator is 0;
- an unpriced model gives `priced: false`, cost 0 and one warning;
- sorting.

**Acceptance:** tests pass; the serialised JSON of an `Aggregate`'s
`usage` and a row has exactly the reference.md keys (add one test that
serialises a `ProductivityRow` and checks `minutesPer100Loc` and
`shareOfLanguage` keys exist).

**Verify:**

```bash
cd desktop/src-tauri
cargo fmt --check
cargo test -j 2 stats::
cargo clippy --all-targets -- -D warnings
cargo clippy --no-default-features --features online --all-targets -- -D warnings
```

---

## T4. Rust: `stats_get` in both builds

**Depends on:** T2, T3. **Parallel:** T4 ∥ T5, T4 ∥ T6, T4 ∥ T7.

**Files:** edit `desktop/src-tauri/src/stats/mod.rs`,
`desktop/src-tauri/src/host.rs` (only: remove the `#![allow(dead_code)]`
T2 added), `desktop/src-tauri/src/commands.rs` (only: `AppState` gains
`pub stats: Arc<crate::stats::Cache>`), `desktop/src-tauri/src/lib.rs`
(the `AppState` literal and `generate_handler!`),
`desktop/src-tauri/src/online/server.rs` (the `AppState` literal),
`desktop/src-tauri/src/online/dispatch.rs` (`COMMANDS`, the match arm,
the test `ctx_with`'s `AppState`, new tests).

Read first: `commands.rs` (`read_transcript` and `conversation`: how the
Claude config dir comes from `jobs_dir.parent()` and how blocking work
runs in `spawn_blocking`), `online/dispatch.rs` (the `args!` macro, the
`read_transcript` arm, the tests at the bottom), reference.md
"`stats_get`".

### `stats/mod.rs`

Remove the `#![allow(dead_code)]` T1 added. Add:

```rust
/// Everything the stats bar and the Stats section show (reference.md `Stats`).
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Stats {
    pub generated_at_ms: i64,
    pub since_ms: Option<i64>,
    pub system: crate::host::HostStats,
    pub usage: aggregate::Usage,
    pub productivity: Vec<aggregate::ProductivityRow>,
    pub scan: cache::ScanInfo,
    pub warnings: Vec<String>,
}

/// The transcript cache and the last CPU sample, shared by every caller.
#[derive(Debug, Default)]
pub struct Cache { inner: std::sync::Mutex<Inner> }

#[derive(Debug, Default)]
struct Inner { transcripts: cache::TranscriptCache, cpu: Option<crate::host::CpuTicks> }

impl Cache {
    /// Refreshes the transcripts under config_dir (none when None, with a
    /// warning "no Claude Code config directory"), samples the host, and
    /// aggregates the window starting at since_ms.
    pub fn stats(&self, config_dir: Option<&Path>, disk: Option<&Path>, since_ms: Option<i64>, now_ms: i64) -> Stats;
}

/// Token usage, lines of code and host load for a window (ADR 0059).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn stats_get(state: State<'_, AppState>, since_ms: Option<i64>) -> CmdResult<Stats>;
```

`stats_get`: clone `Arc<Cache>` and `jobs_dir`, then in
`tokio::task::spawn_blocking` call `cache.stats(jobs_dir.parent(),
host::disk_path().as_deref(), since_ms, commands::now_ms())`; a join
error becomes `AppError::new("internal", e.to_string())`. A poisoned
mutex: take the inner value (`lock().unwrap_or_else(|e| e.into_inner())`).
Import `State` from `crate::ipc`, `AppState` from `crate::commands`,
`AppError` from `crate::runner`, as `commands.rs` does; `CmdResult` is
private there, so use `Result<Stats, AppError>`.

### Wiring

- `commands.rs`: add the field with a doc comment `/// Transcript and
  host stats (ADR 0059).`
- `lib.rs`: `stats: Arc::new(stats::Cache::default()),` in the
  `AppState` literal; `stats::stats_get,` in `generate_handler!` after
  `loops::loops_memory_clear,`.
- `online/server.rs` and the test `AppState` in `online/dispatch.rs`:
  the same field.
- `online/dispatch.rs`: `"stats_get"` appended to `COMMANDS`; arm

  ```rust
  "stats_get" => {
      let since_ms = args!(args, { since_ms: Option<i64> });
      result(stats::stats_get(State::new(&ctx.state), since_ms).await)
  }
  ```

  The `args!` macro with one field returns the value itself, like
  `"cache_refs"`. serde's derive treats a missing `Option` field as
  `None`, so `{}` and `{"sinceMs": null}` both mean all time; a value
  that isn't an integer or null (`"yesterday"`) is `bad_args`.

### Tests

- `stats/mod.rs`: `Cache::stats` with `config_dir: None` returns zero
  totals and the warning; with a temp config dir holding one transcript
  (two requests, one edit) returns those totals, `scan.files == 1`, and a
  second call within 5 s has `scan.cached == true`; `since_ms` after the
  requests' hour gives zero requests.
- `online/dispatch.rs`: `commands_match_generate_handler` still passes
  (it compares `COMMANDS` to `lib.rs`); new `#[tokio::test]
  stats_get_returns_camel_case_stats`: `dispatch(&ctx, "stats_get",
  json!({"sinceMs": null}), None)` is `Ok`, and the value has keys
  `generatedAtMs`, `system`, `usage`, `productivity`, `scan`,
  `warnings`; `stats_get_treats_missing_since_ms_as_all_time`:
  `json!({})` is `Ok` with `sinceMs` null; `stats_get_rejects_a_bad_since_ms`:
  `json!({"sinceMs": "yesterday"})` gives `DispatchError::BadArgs(_)`.

**Acceptance:** both builds compile and pass; `stats_get` is reachable
through `dispatch` and listed in `generate_handler!`; no other command
changes.

**Verify:**

```bash
cd desktop/src-tauri
cargo fmt --check
cargo test -j 2 stats
cargo test -j 2 --no-default-features --features online dispatch
cargo clippy --all-targets -- -D warnings
cargo clippy --no-default-features --features online --all-targets -- -D warnings
```

---

## T5. Frontend data: types, client, slice, polling, formatters, fakes, mock

**Depends on:** nothing (contract in reference.md). **Parallel:** T5 ∥
T1, T2, T3, T4.

**Files:** edit `desktop/src/api.ts`, `desktop/src/store.ts`,
`desktop/src/test/fixtures.ts`, `desktop/test/tauri-mock.js`; new
`desktop/src/features/stats/statsSlice.ts`, `statsSlice.test.ts`,
`useStatsPolling.ts`, `useStatsPolling.test.tsx`, `format.ts`,
`format.test.ts`.

Read first: `api.ts` (the `api` object and the loop types),
`features/loops/loopDataSlice.ts` and its test (thunk and reducer
style), `thunk.ts`, `test/fixtures.ts` (`fakeApi`, `testStore`),
`test/tauri-mock.js` (`mock.handlers`), reference.md "Shapes".

### `api.ts`

Add every TypeScript type from reference.md "Shapes" verbatim (above
`export const api`), and

```ts
  statsGet: async (sinceMs: number | null): Promise<Stats> => call<Stats>("stats_get", { sinceMs }),
```

(always send the key, also when null).

### `features/stats/statsSlice.ts`

```ts
export type StatsWindow = "today" | "7d" | "30d" | "all";
/** In list order. */
export const statsWindows: readonly StatsWindow[] = ["today", "7d", "30d", "all"];
export const windowLabels: Readonly<Record<StatsWindow, string>> =
  { today: "Today", "7d": "Last 7 days", "30d": "Last 30 days", all: "All" };

export interface WindowResult {
  readonly data: Stats | null;
  readonly error: AppError | null;
  readonly loading: boolean;
}
export interface StatsState {
  /** The window the Stats section shows; the bar always shows today. */
  readonly window: StatsWindow;
  readonly results: Readonly<Record<StatsWindow, WindowResult>>;
}

/** sinceMs for a window at `now`: local midnight, now − 7 or 30 days, or null. */
export const sinceFor = (window: StatsWindow, now: Date): number | null;

export const fetchStats = createAppThunk<Stats, { readonly window: StatsWindow; readonly sinceMs: number | null }>("stats/fetch", …);

// reducers: windowChosen(StatsWindow)
export const { windowChosen } = statsSlice.actions;
export const statsReducer = statsSlice.reducer;
```

Pending sets that window's `loading: true` (data and error kept);
fulfilled sets `data`, `error: null`, `loading: false`; rejected sets
`error` (the `AppError`, or `{ code: "internal", message }`) and
`loading: false`, keeping `data`. The action type of `windowChosen` must
be exactly `"stats/windowChosen"` (T6 matches it by string). Register
`stats: statsReducer` in `store.ts`'s `rootReducer`.

### `features/stats/useStatsPolling.ts`

```ts
/** How often the bar and the Stats section refresh (ADR 0059). */
export const statsPollInterval = 15_000;

/** Fetches `window` now and every statsPollInterval while mounted, skipping
 * a tick while the document is hidden or that window is already loading. */
export const useStatsPolling = (window: StatsWindow): void;
```

Use `useAppDispatch`, `useAppStore` (read `loading` at tick time with
`store.getState()`, so the effect doesn't restart on every result),
`window.setInterval`/`clearInterval`, and `sinceFor(window, new Date())`
at each tick. The effect depends only on `window` and `dispatch`.

### `features/stats/format.ts`

```ts
export const formatCount = (n: number): string;      // 999, 12.3k, 1.2M, 3.4B (one decimal, trailing ".0" kept off: 12k not 12.0k)
export const formatPercent = (ratio: number | null): string;      // null → "—"; 0.934 → "93%"
export const formatPercentValue = (percent: number | null): string; // null → "—"; 23.44 → "23%"
export const formatUsd = (usd: number): string;      // 0 → "$0.00"; 0.004 → "<$0.01"; 4.123 → "$4.12"; 123.4 → "$123"
export const formatBytes = (bytes: number): string;  // 1024-based, one decimal: "12.3 GB", "512 MB", "900 B"
export const formatMinutes = (minutes: number): string; // 0.4 → "<1 min"; 15.2 → "15 min"; 125 → "2 h 5 min"
export const formatRate = (value: number | null, digits: number): string; // null → "—", else toFixed(digits)
export const usedRatio = (used: number | null, total: number | null): number | null; // null if either null or total 0
/** Workspaces with a workspace id; and of those, ones working, needing input, starting or running. */
export const workspaceCounts = (entries: readonly Entry[]): { readonly active: number; readonly total: number };
```

### Fakes and mock

- `test/fixtures.ts`: `export const statsSnapshot = (overrides: Partial<Stats> = {}): Stats`
  with a full, realistic value (two models: `claude-haiku-5-5` and
  `claude-sonnet-5-5`; languages Go, TypeScript, Markdown; non-null
  system numbers; one productivity row per model × Go and TypeScript);
  `fakeApi` gains `statsGet: vi.fn(async () => statsSnapshot())`.
- `test/tauri-mock.js`: `stats_get: ({ sinceMs }) => ({ … })` in
  `mock.handlers`, with the same kind of data as `statsSnapshot`
  (three models including `claude-opus-5-5`, five languages, `sinceMs`
  echoed, `scan.cached` false, `warnings: []`), scaled by the window: all
  token, line and minute numbers × 1 for today, × 5 for 7 days, × 18 for
  30 days and × 40 for null, so the screenshots differ by window.

### Tests

- `statsSlice.test.ts`: reducer for pending/fulfilled/rejected per
  window (others untouched), rejected keeps data, `windowChosen`;
  `sinceFor` with a fixed `Date` (local midnight; 7d; 30d; all → null).
- `useStatsPolling.test.tsx`: with `vi.useFakeTimers()`, a component
  calling the hook in `renderWithStore` with `testStore({ api })`: one
  `statsGet` call on mount with the right `sinceMs`; another after
  `statsPollInterval`; none while `document.hidden` is true (stub with
  `Object.defineProperty(document, "hidden", { configurable: true, get: () => true })`);
  none after unmount.
- `format.test.ts`: each formatter's examples above, and
  `workspaceCounts` with `entry()` fixtures (a shell with no workspace id
  is not counted; `needsInput` and `working` are active, `done` isn't).

**Acceptance:** typecheck passes (the `Api` type now has `statsGet`, and
`fakeApi` matches); the new tests pass; no component renders anything
new yet.

**Verify:**

```bash
cd desktop
npm ci
npm run typecheck
npm test -- --maxWorkers=2 src/features/stats
npm test -- --maxWorkers=2 --testTimeout=30000
node -e "const c={window:{},console};require('vm').runInNewContext(require('fs').readFileSync('test/tauri-mock.js','utf8'),c);Promise.resolve(c.window.__TAURI_INTERNALS__.invoke('stats_get',{sinceMs:null})).then(s=>{if(!s.usage||!s.usage.totals||!Array.isArray(s.productivity))process.exit(1);console.log('mock ok')})"
```

---

## T6. UI: stats bar and Stats section, at desktop and 360px

**Depends on:** T5. **Parallel:** T6 ∥ T3, T6 ∥ T4.

**Files:** new `desktop/src/features/stats/StatsBar.tsx`,
`StatsBar.module.css`, `StatsBar.test.tsx`, `StatsList.tsx`,
`StatsList.module.css`, `StatsPane.tsx`, `StatsPane.module.css`,
`StatsPane.test.tsx`, `SystemCard.tsx`, `UsageCard.tsx`, `Meter.tsx`;
edit `desktop/src/features/ui/uiSlice.ts`, `features/ui/uiSlice.test.ts`,
`features/rail/Rail.tsx`, `features/rail/Rail.test.tsx`,
`features/rail/Rail.module.css`, `desktop/src/App.tsx`,
`desktop/src/App.module.css`, `desktop/src/App.test.tsx`.

Read first: `App.tsx`, `App.module.css` (the `@media (max-width:
720px)` block), `features/rail/Rail.tsx` and `.module.css`,
`features/ui/uiSlice.ts` (`openedTypes`), `features/settings/SettingsList.tsx`
and `SettingsPane.module.css` (a section list and a card layout to
copy), T5's `statsSlice.ts`, `useStatsPolling.ts` and `format.ts`, ADR
"What the bar and the section show".

### Section plumbing

- `uiSlice.ts`: `Section` gains `"stats"`; add `"stats/windowChosen"` to
  `openedTypes` (so choosing a window shows the pane on a phone).
- `Rail.tsx`: `stats` icon (paths `<path d="M4 20V11" /><path d="M10 20V5" /><path d="M16 20v-6" /><path d="M21 20H3" />`),
  label "Stats"; `railOrder` and `shownSections` become
  `["agents", "repos", "reviews", "loops", "stats", "settings"]`, so ⌘5
  is Stats and ⌘6 Settings; update the doc comments that say ⌘1–⌘5.
- `App.tsx`: `stats: { actions: () => null, list: StatsList, pane: StatsPane }`
  in `sectionViews`; the `Warnings` switch returns
  `s.stats.results[s.stats.window].data?.warnings ?? noWarnings` for
  `"stats"`; render `<StatsBar />` as the **last child of the root
  `div`**, after `.body` and before the dialogs.
- `App.module.css`: `.sidebar[data-section="stats"] { width: 220px; }`
  next to the settings rule (and in the phone block, with settings).

### `StatsBar`

`export const StatsBar = (): JSX.Element` — calls
`useStatsPolling("today")`; reads `agents.entries` (via
`workspaceCounts`) and `stats.results.today.data`.

Markup: `<footer className={styles.bar} aria-label="Stats">` containing
`<span className={styles.item}>` items in this order, each with a
visible label:

1. `3 / 14 workspaces` (active / total) — always;
2. `1.2M tokens` (`formatCount(totals.totalTokens)`) — always;
3. `cache 93%` (`formatPercent(totals.cacheHitRatio)`) — always;
4. `$4.12` (`formatUsd(totals.costUsd)`) with `title="Estimated at API rates"` — `data-wide`;
5. `CPU 23%`, `RAM 61%`, `disk 48%` — each `data-wide`;
6. a MUI `<Button size="small">Open Stats</Button>` that dispatches
   `sectionChosen("stats")` — `data-wide`.

Before the first result the numbers read "—" (items 2–5); workspace
counts never wait. Items are separated by a 1px `--border` divider.

CSS: one row, `min-height: 28px`, `font-size: 12px`, `color:
var(--text-muted)`, `border-top: 1px solid var(--border)`, `background:
var(--panel)`, `white-space: nowrap`, `overflow: hidden`, gap 12px,
padding `0 12px`. Under `@media (max-width: 720px)`: `[data-wide] {
display: none; }`, `min-height: 24px`, `font-size: 11px`, gap 8px,
padding `0 10px env(safe-area-inset-bottom)`; and in `Rail.module.css`'s
phone block change the rail's bottom padding to `2px` (the bar now owns
the safe-area inset, since it is below the rail). The bar never makes
the page scroll sideways: items shrink with `min-width: 0` and the last
visible item ellipsises (`text-overflow: ellipsis`).

### `StatsList`

Like `SettingsList`: a `<nav aria-label="Stats windows">` with one
button per `statsWindows` entry (`windowLabels`), `aria-current="page"`
on the chosen one, dispatching `windowChosen(w)`. Buttons `min-height:
40px` on phones.

### `StatsPane`

`export const StatsPane = (): JSX.Element` — calls
`useStatsPolling(window)` for the chosen window; shows:

- a header: `<h1>` with the window label, and a muted line `Updated
  HH:MM · 112 transcripts` (from `generatedAtMs` and `scan.files`);
- an `Alert severity="error"` with the error message when there is an
  error and no data; "Loading…" when neither;
- cards (`<section aria-labelledby>` with an `<h2>`), in a CSS grid
  `repeat(auto-fill, minmax(280px, 1fr))`, one column under 720px with
  `min-width: 0` on cards:
  - **System** (`SystemCard`): CPU (percent, `cpuCount` cores, load
    average), Memory (used / total, bytes), Disk (used / total, path in
    muted text, ellipsised) — each a `Meter`;
  - **Usage** (`UsageCard`): requests, sessions, agent minutes
    (`formatMinutes`), total tokens, cache hit, estimated cost — a
    two-column `<dl>`.
- T7 adds four more cards to the same grid, after these two; render
  just `SystemCard` and `UsageCard` for now.

`Meter`: `({ label, ratio, detail }) => <div role="meter" aria-label={label}
aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(ratio*100)}>`
with a CSS bar (`--accent` fill on a `--border` track, 6px tall); when
`ratio` is null render `aria-valuetext="unknown"` and an empty track.

### Tests

- `uiSlice.test.ts`: `"stats/windowChosen"` sets `narrowPane` to
  `"detail"`; `sectionChosen("stats")` works.
- `Rail.test.tsx`: ⌘5 chooses Stats and ⌘6 Settings (update the old ⌘5
  test); the Stats button is listed between Loops and Settings.
- `StatsBar.test.tsx`: with `testStore({ api: fakeApi(), state })` and
  entries `[working, needsInput, done, shell-without-workspace]`, shows
  "2 / 3 workspaces"; after the fetch resolves shows the token, cache,
  cost, CPU texts from `statsSnapshot()`; shows "—" before it resolves
  (a `statsGet` that never resolves); "Open Stats" chooses the section;
  items 4–6 carry `data-wide` (the phone rule is CSS; check the
  attribute, not visibility).
- `StatsPane.test.tsx`: renders System and Usage headings and the
  meters' `aria-valuenow` from the snapshot; choosing "Last 7 days" in
  `StatsList` calls `statsGet` with a `sinceMs` 7 days back; an error
  with no data shows the alert.
- `App.test.tsx`: the app renders a `contentinfo`/footer named "Stats"
  (find by `getByRole("contentinfo", { name: "Stats" })`) in the Agents
  section; choosing the Stats rail button shows the "Today" heading.

**Acceptance:** the bar is visible in every section in the desktop
build and the web build (`main.tsx` renders `App` inside `WebRoot` there; its tests still
pass); at 360px the bar shows exactly three items on one line and the
Stats list and pane work with the existing Back bar; no horizontal
scroll; all tests pass.

**Verify:**

```bash
cd desktop
npm run typecheck
npm test -- --maxWorkers=2 src/features/stats src/features/rail src/features/ui src/App.test.tsx src/web
npm test -- --maxWorkers=2 --testTimeout=30000
```

Then a manual layout check for the report (no Playwright test is
committed): `npx vite --port 1420` with `test/tauri-mock.js` injected,
at 1280×800 and 360×740: bar on one line; at 360px three items, no
horizontal scroll (`document.documentElement.scrollWidth <= 360`),
Stats list buttons ≥ 40px tall. If no browser is available, say so.

---

## T7. UI: model, token-kind, language and productivity cards

**Depends on:** T6. **Parallel:** T7 ∥ T4.

**Files:** new `desktop/src/features/stats/insights.ts`,
`insights.test.ts`, `ModelsCard.tsx`, `TokenKindsCard.tsx`,
`LanguagesCard.tsx`, `ProductivityCard.tsx`, `Cards.module.css`,
`StatsCards.test.tsx`; edit `features/stats/StatsPane.tsx` (render the
four cards after System and Usage); edit `desktop/README.md` (one
bullet in the feature list for the stats bar and Stats section, linking
ADR 0059).

Read first: T6's `StatsPane.tsx`, `UsageCard.tsx`, `StatsPane.module.css`;
`format.ts`; reference.md `ModelUsage`, `LanguageLines`,
`ProductivityRow`; the ADR "Metric definitions".

### `insights.ts`

```ts
/** Productivity rows grouped by language, in the order they arrive. */
export const rowsByLanguage = (rows: readonly ProductivityRow[]): ReadonlyMap<string, readonly ProductivityRow[]>;

/** One sentence per language comparing its models, for example
 * "Go: Haiku 5.5 wrote 62% of lines at 9.1 lines/min ($0.0003/line); Sonnet 5.5 takes 15 min per 100 lines."
 * The first model is the one with the largest share; each other model
 * with at least 20 lines gets "<label> takes <formatMinutes(minutesPer100Loc)> per 100 lines".
 * Languages with fewer than 20 lines in total, and "Other", get no sentence. */
export const languageInsights = (rows: readonly ProductivityRow[]): readonly string[];

/** The four token kinds with their share of totalTokens, for the bar chart. */
export const tokenKinds = (totals: TokenCounts): readonly { readonly kind: "Input" | "Output" | "Cache write" | "Cache read"; readonly tokens: number; readonly share: number }[];
```

Use `formatPercent`, `formatRate(locPerMinute, 1)`, and a cost per line
formatted with 4 decimals (`$0.0003`); null ratios are left out of the
sentence rather than printed as "—".

### Cards

Each is a `<section aria-labelledby>` with an `<h2>`, styled from
`Cards.module.css`. Tables use `<table>` with `<th scope="col">`, inside
a `div` with `overflow-x: auto; max-width: 100%` so only the table
scrolls on a phone; numbers right-aligned with `font-variant-numeric:
tabular-nums`.

- **Models** (`ModelsCard`, "Usage by model"): rows from `byModel`:
  Model (`label`), Requests, Sessions, Agent time, Tokens, Cache hit,
  Est. cost (with "*" and a footnote "No price known" when `!priced`).
- **Token kinds** (`TokenKindsCard`, "Tokens by kind"): a horizontal
  stacked bar (four `div`s with `flex-basis` = share; each has a
  `title`), then a list "Input 9.1k (0%)", … and "Thinking 96k (part of
  output)".
- **Languages** (`LanguagesCard`, "Lines by language"): rows from
  `byLanguage`: Language, Lines added, Lines removed, Edits, and a
  share-of-added CSS bar.
- **Productivity** (`ProductivityCard`, "Productivity by model and
  language"): the `languageInsights` sentences as a `<ul>`, then a table
  grouped by language (a row with the language name spanning all
  columns, then its models): Model, Share, Lines, Agent time, Lines/min,
  Min/100 lines, Tokens/line, Cost/line. A short muted note under it:
  "Lines are lines added by agents' edits; agent time is active session
  time split by lines changed. Costs are estimates at API rates."
- Empty states: "No requests in this window." / "No edits in this
  window." instead of empty tables.

On phones the cards stack (T6's grid); every table scrolls inside its
wrapper; the stacked bar and the insight list wrap.

### Tests

- `insights.test.ts`: grouping keeps order; a two-model Go example gives
  the exact sentence; a language under 20 lines and "Other" give none; a
  model with null `minutesPer100Loc` leaves that clause out;
  `tokenKinds` shares sum to 1 and are 0 for an all-zero total.
- `StatsCards.test.tsx` (render `StatsPane` with `testStore` and
  `fakeApi` returning `statsSnapshot()`): each card heading is present;
  the Models table has one row per model with the label; the
  Productivity card shows the Go insight and a "Go" group row; with a
  snapshot with empty `byModel`, `byLanguage` and `productivity`, the
  empty-state texts show; an unpriced model shows the footnote.

**Acceptance:** tests pass; at 360px no card makes the page scroll
sideways (tables scroll inside their wrappers); the README bullet links
the ADR.

**Verify:**

```bash
cd desktop
npm run typecheck
npm test -- --maxWorkers=2 src/features/stats
npm test -- --maxWorkers=2 --testTimeout=30000
```

Then the same manual 1280 and 360px layout check as T6, on the Stats
section with the Last 30 days window; report table wrappers that scroll
and that `document.documentElement.scrollWidth <= 360`.

---

## After the tasks (orchestrator)

- Screenshots from `test/tauri-mock.js` (it answers `stats_get` after
  T5): the Agents section with the bar at 1280×800 and 360×740, and the
  Stats section (Last 30 days) at both sizes, into `desktop/docs/`;
  named checks: bar on one line, three items at 360px, no horizontal
  scroll, tap targets ≥ 40px in the Stats list.
- Whole-branch verifier `CHECKS`: `make lint; make test; make
  desktop-test`, plus a smoke run of `ws-online` calling
  `POST /api/invoke/stats_get` with `{"sinceMs": null}` and checking the
  JSON has `usage.totals`.

## T8: Fix layout defects found by screenshots

- **Depends on:** T7. **Size:** `small`.
- **Files:** `desktop/src/features/stats/Cards.module.css`, `desktop/src/features/stats/StatsPane.module.css`, `desktop/src/features/stats/StatsBar.module.css`, and, only if needed, `ModelsCard.tsx`, `ProductivityCard.tsx`, `LanguagesCard.tsx`, `StatsPane.tsx`.
- **Problems (seen in screenshots at 1280px and 360px):**
  1. The cards sit in a 3-column grid, so "Usage by model" and "Productivity by model and language" tables are wider than their card and are cut at the edge ("Agent t"). Lines by language is also clipped at 360px.
  2. In the bar, the "Open Stats" button is 31px tall and sits lower than the text items; it must be vertically centred with them.
- **Do:**
  1. Make the card grid `repeat(auto-fit, minmax(320px, 1fr))` and make the Usage by model and Productivity cards span the full row (`grid-column: 1 / -1`). Give every table `width: 100%` and `font-size` no larger than the body; allow header and cells to wrap or shrink so tables fit at 1280px in a full-row card. At 360px a table may scroll inside its card (`overflow-x: auto`), but add a visible right-edge fade or a `min-width` that makes the scroll obvious. Lines by language must fit at 360px without clipping (shrink padding, allow header wrap).
  2. In `StatsBar.module.css` set the bar `align-items: center` and give the Open Stats button `line-height: 1`, `padding-block: 0`, `min-height: 0` on desktop (keep `min-height: 40px` at max-width 720px).
- **Acceptance:** all existing stats tests still pass; no hard-coded colours; no horizontal page scroll at 360px.
- **Verify:** `cd desktop && npm run typecheck && npx vitest run src/features/stats`
