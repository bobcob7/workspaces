# Stats: command reference

The contract for [ADR 0059](../../adrs/0059-stats.md). Every task builds
to this file; when the code and this file disagree, this file wins until
it is changed on purpose.

## `stats_get`

One command, the same in both builds:

| Build | Call |
|-------|------|
| Desktop (Tauri) | `invoke("stats_get", { sinceMs })` |
| Workspace Online | `POST /api/invoke/stats_get` with body `{"sinceMs": …}`, session cookie and `Origin` as for every command ([0053](../../adrs/0053-workspace-online.md)) |
| Frontend | `api.statsGet(sinceMs: number \| null): Promise<Stats>` |

**Arguments** (Tauri-style camelCase object):

| Key | Type | Meaning |
|-----|------|---------|
| `sinceMs` | integer (Unix ms) or `null` | Start of the window. Buckets whose UTC hour starts at or after `floor(sinceMs / 3600000) * 3600000` count. `null`: everything still on disk |

The frontend computes `sinceMs` from the window it shows:

| Window | `sinceMs` |
|--------|-----------|
| `today` | local midnight of the browser's current day |
| `7d` | now − 7 × 86 400 000 |
| `30d` | now − 30 × 86 400 000 |
| `all` | `null` |

**Errors**: only `AppError { code: "internal" }` (the blocking task
panicked). A missing Claude config dir, an unreadable file or a failed
system call is not an error: the numbers it would give are zero or null
(and `warnings` names a missing config dir or an unpriced model).
`{}` means `sinceMs: null`. Workspace Online answers a `sinceMs` that is
neither an integer nor null with `bad_args` (status 400), as for every
command.

**Result**: `Stats`, below. All keys camelCase. Integers are JSON
numbers (token counts stay far below 2^53). `f64` fields are unrounded;
the frontend rounds for display.

### Example

```json
{
  "generatedAtMs": 1762977600000,
  "sinceMs": 1762927200000,
  "system": {
    "cpuPercent": 23.4,
    "cpuCount": 8,
    "loadAverage": [1.92, 1.40, 1.12],
    "memoryTotalBytes": 34359738368,
    "memoryUsedBytes": 20959297536,
    "disk": { "path": "/Users/me/Library/Application Support/workspaces", "totalBytes": 994662584320, "usedBytes": 477438279680 }
  },
  "usage": {
    "totals": {
      "requests": 1840,
      "inputTokens": 9120,
      "outputTokens": 412000,
      "thinkingTokens": 96000,
      "cacheWriteTokens": 3100000,
      "cacheReadTokens": 52000000,
      "totalTokens": 55521120,
      "costUsd": 18.42,
      "cacheHitRatio": 0.9437
    },
    "sessions": 31,
    "activeMinutes": 412.5,
    "byModel": [
      {
        "model": "claude-haiku-5-5",
        "label": "Haiku 5.5",
        "family": "haiku",
        "priced": true,
        "sessions": 18,
        "activeMinutes": 240.0,
        "linesAdded": 5210,
        "linesRemoved": 1330,
        "requests": 1200,
        "inputTokens": 6000,
        "outputTokens": 260000,
        "thinkingTokens": 50000,
        "cacheWriteTokens": 1900000,
        "cacheReadTokens": 34000000,
        "totalTokens": 36166000,
        "costUsd": 1.21,
        "cacheHitRatio": 0.9468
      }
    ],
    "byLanguage": [
      { "language": "Go", "linesAdded": 3120, "linesRemoved": 610, "edits": 140 },
      { "language": "TypeScript", "linesAdded": 2400, "linesRemoved": 820, "edits": 122 }
    ]
  },
  "productivity": [
    {
      "model": "claude-haiku-5-5",
      "label": "Haiku 5.5",
      "family": "haiku",
      "language": "Go",
      "linesAdded": 1934,
      "linesRemoved": 380,
      "edits": 88,
      "activeMinutes": 212.5,
      "tokens": 19800000,
      "costUsd": 0.66,
      "shareOfLanguage": 0.62,
      "locPerMinute": 9.1,
      "minutesPer100Loc": 10.99,
      "tokensPerLoc": 10237.8,
      "costPerLoc": 0.00034
    }
  ],
  "scan": { "files": 112, "filesRead": 3, "bytesRead": 48213, "durationMs": 4, "cached": false },
  "warnings": []
}
```

### Shapes

TypeScript (in `desktop/src/api.ts`; every field `readonly`):

```ts
export interface DiskStats { readonly path: string; readonly totalBytes: number; readonly usedBytes: number }

export interface HostStats {
  readonly cpuPercent: number | null;      // busy % since the previous stats_get; null on the first call
  readonly cpuCount: number | null;
  readonly loadAverage: readonly [number, number, number] | null;
  readonly memoryTotalBytes: number | null;
  readonly memoryUsedBytes: number | null;
  readonly disk: DiskStats | null;         // filesystem of $WS_CONFIG_DIR, else $HOME
}

export interface TokenCounts {
  readonly requests: number;
  readonly inputTokens: number;            // uncached input
  readonly outputTokens: number;
  readonly thinkingTokens: number;         // part of outputTokens
  readonly cacheWriteTokens: number;       // 5m + 1h
  readonly cacheReadTokens: number;
  readonly totalTokens: number;            // input + output + cacheWrite + cacheRead
  readonly costUsd: number;                // estimate at API rates
  readonly cacheHitRatio: number | null;   // cacheRead / (input + cacheRead + cacheWrite)
}

export interface ModelUsage extends TokenCounts {
  readonly model: string;                  // as in the transcript, e.g. "claude-haiku-5-5"
  readonly label: string;                  // "Haiku 5.5"; the model id when it can't be parsed
  readonly family: "opus" | "sonnet" | "haiku" | "fable" | "mythos" | "other";
  readonly priced: boolean;                // false: no rate known, costUsd is 0
  readonly sessions: number;
  readonly activeMinutes: number;
  readonly linesAdded: number;
  readonly linesRemoved: number;
}

export interface LanguageLines {
  readonly language: string;
  readonly linesAdded: number;
  readonly linesRemoved: number;
  readonly edits: number;
}

export interface Usage {
  readonly totals: TokenCounts;
  readonly sessions: number;               // transcript files with a request in the window
  readonly activeMinutes: number;
  readonly byModel: readonly ModelUsage[];       // totalTokens descending, then model ascending
  readonly byLanguage: readonly LanguageLines[]; // linesAdded descending, then language ascending
}

export interface ProductivityRow {
  readonly model: string;
  readonly label: string;
  readonly family: ModelUsage["family"];
  readonly language: string;
  readonly linesAdded: number;
  readonly linesRemoved: number;
  readonly edits: number;
  readonly activeMinutes: number;          // allocated by lines changed, see ADR
  readonly tokens: number;                 // allocated totalTokens
  readonly costUsd: number;                // allocated cost
  readonly shareOfLanguage: number | null; // 0..1
  readonly locPerMinute: number | null;
  readonly minutesPer100Loc: number | null;
  readonly tokensPerLoc: number | null;
  readonly costPerLoc: number | null;
}

export interface ScanInfo {
  readonly files: number;       // transcript files known after the refresh
  readonly filesRead: number;   // files read (wholly or from an offset) by this refresh
  readonly bytesRead: number;
  readonly durationMs: number;
  readonly cached: boolean;     // true: within MIN_REFRESH of the last refresh, nothing scanned
}

export interface Stats {
  readonly generatedAtMs: number;
  readonly sinceMs: number | null;
  readonly system: HostStats;
  readonly usage: Usage;
  readonly productivity: readonly ProductivityRow[]; // language by total linesAdded desc, then linesAdded desc, then model asc
  readonly scan: ScanInfo;
  readonly warnings: readonly string[];
}
```

Rust (`desktop/src-tauri/src`), each `#[derive(Debug, Clone, Serialize,
PartialEq)]` with `#[serde(rename_all = "camelCase")]`:

| Rust type (file) | TS type | Notes |
|------------------|---------|-------|
| `host::DiskStats` (`host.rs`) | `DiskStats` | `path: String, total_bytes: u64, used_bytes: u64` |
| `host::HostStats` (`host.rs`) | `HostStats` | also `Default` (all `None`); `load_average: Option<[f64; 3]>`, `cpu_count: Option<u32>` |
| `stats::aggregate::TokenCounts` | `TokenCounts` | also `Default` |
| `stats::aggregate::ModelUsage` | `ModelUsage` | `#[serde(flatten)] tokens: TokenCounts`; `family: &'static str` |
| `stats::aggregate::LanguageLines` | `LanguageLines` | |
| `stats::aggregate::Usage` | `Usage` | |
| `stats::aggregate::ProductivityRow` | `ProductivityRow` | `minutes_per_100_loc` serialises as `minutesPer100Loc` |
| `stats::cache::ScanInfo` | `ScanInfo` | also `Default` |
| `stats::Stats` (`stats/mod.rs`) | `Stats` | `generated_at_ms: i64, since_ms: Option<i64>` |

## Constants

| Name | Value | Where |
|------|-------|-------|
| `IDLE_CAP_MS` | `300_000` | `stats/ingest.rs` |
| `HOUR_MS` | `3_600_000` | `stats/time.rs` |
| `MAX_RECENT_KEYS` | `32` | `stats/ingest.rs` |
| `MAX_PENDING_EDITS` | `1_000` | `stats/ingest.rs` |
| `MIN_REFRESH_MS` | `5_000` | `stats/cache.rs` |
| `HAIKU_LONG_PROMPT` | `100_000` | `stats/price.rs` |
| `statsPollInterval` | `15_000` | `desktop/src/features/stats/useStatsPolling.ts` |

## Price table (`stats/price.rs`)

USD per million tokens. A model matches the first row whose id prefix it
starts with.

| Prefix | family | input | output | write 5m | write 1h | read |
|--------|--------|-------|--------|----------|----------|------|
| `claude-opus-5-5` | opus | 4.00 | 20.00 | 5.00 | 8.00 | 0.20 |
| `claude-sonnet-5-5` | sonnet | 2.00 | 10.00 | 2.50 | 4.00 | 0.10 |
| `claude-haiku-5-5` (prompt ≤ 100K) | haiku | 0.10 | 0.50 | 0.125 | 0.20 | 0.01 |
| `claude-haiku-5-5` (prompt > 100K) | haiku | 0.50 | 2.50 | 0.625 | 1.00 | 0.05 |
| `claude-fable-5-2` | fable | 8.00 | 40.00 | 10.00 | 16.00 | 0.20 |

Prompt = input + cache read + cache write (5m + 1h) of that request.

## Languages (`stats/lang.rs`)

By lowercased extension (text after the last `.` of the base name),
except base names `Makefile`/`GNUmakefile` → Make and `Dockerfile` or
`Dockerfile.*` → Dockerfile.

| Extensions | Language |
|------------|----------|
| `go` | Go |
| `rs` | Rust |
| `ts` `tsx` `mts` `cts` | TypeScript |
| `js` `jsx` `mjs` `cjs` | JavaScript |
| `py` | Python |
| `rb` | Ruby |
| `java` | Java |
| `kt` `kts` | Kotlin |
| `swift` | Swift |
| `c` `h` | C |
| `cc` `cpp` `cxx` `hpp` `hh` | C++ |
| `cs` | C# |
| `php` | PHP |
| `sh` `bash` `zsh` | Shell |
| `sql` | SQL |
| `html` `htm` | HTML |
| `css` `scss` `sass` `less` | CSS |
| `md` `mdx` | Markdown |
| `json` | JSON |
| `yaml` `yml` | YAML |
| `toml` | TOML |
| `proto` | Protobuf |
| `ipynb` | Notebook |
| `tf` `hcl` | HCL |
| `mk` | Make |
| anything else, or no extension | Other |

## Transcript lines the parser reads

Only lines containing `"type":"assistant"` or `"type":"user"` are parsed.
Fields used (all others ignored; a missing field is skipped, never an
error):

```jsonc
// assistant: one line per content block; usage repeats on each
{"type":"assistant","timestamp":"2026-11-12T20:00:01.123Z","requestId":"req_1",
 "message":{"id":"msg_1","model":"claude-haiku-5-5",
   "content":[{"type":"tool_use","id":"toolu_1","name":"Edit",
               "input":{"file_path":"/w/x/main.go","old_string":"a\n","new_string":"a\nb\n"}}],
   "usage":{"input_tokens":2,"output_tokens":331,"cache_read_input_tokens":27394,
            "cache_creation_input_tokens":10068,
            "cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":10068},
            "output_tokens_details":{"thinking_tokens":146}}}}
// user: a tool result, with Claude Code's structured result beside it
{"type":"user","timestamp":"2026-11-12T20:00:02.000Z",
 "message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":false,"content":"ok"}]},
 "toolUseResult":{"filePath":"/w/x/main.go",
   "structuredPatch":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":2,"lines":[" a","+b"]}]}}
// user: Write creating a file
{"type":"user","timestamp":"…","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_2"}]},
 "toolUseResult":{"type":"create","filePath":"/w/x/new.go","content":"package x\n\nfunc A() {}\n","structuredPatch":[]}}
```
