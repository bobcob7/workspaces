# Loop history and memory: command and API reference

The contract for [ADR 0056](../../adrs/0056-loop-history-and-memory.md).
Every task in [tasks.md](tasks.md) implements part of it; when they
disagree, this file wins. Times are RFC 3339 UTC (`formatTime`), and
optional values are `null` in JSON, never omitted.

## Files

Under `CONFIG_DIR/state/.loops/<name>/` (`loop.DataStore`):

| File | Written by | Shape |
| --- | --- | --- |
| `history.json` | `ws loop tick`, `ws loop run`, `ws loop history --clear` (under `loops/.lock`) | `{"version":1,"next_seq":43,"entries":[Entry…]}`, oldest first |
| `reports/<seq>.md` | tick's settle, copied from `state/<run>/machine/<run>/report.md` | the report, ≤ 1 MiB |
| `memory.json` | every memory write (under `.memory.lock`) | `{"version":1,"entries":{"KEY":MemoryEntry}}` |
| `.memory.lock` | memory writers | empty, flock |

Under `CONFIG_DIR/state/<run-workspace>/`:

| File | Written by | Shape (TOML) |
| --- | --- | --- |
| `loop.toml` | `ws loop tick`/`run`, after creating the workspace, before starting the agent | `version = 1`, `loop = "deps-bump"`, `started_at = 2026-11-10T08:00:00Z` |

On-disk `Entry` (Go `loop.Entry`, JSON tags shown):

```json
{
  "seq": 42,
  "status": "succeeded",
  "trigger": "tick",
  "started_at": "2026-11-10T08:00:00Z",
  "finished_at": "2026-11-10T08:14:30Z",
  "workspace": "loop-deps-bump-1a2b3c4d",
  "session": "e8cdf5d8",
  "closed": false,
  "error_code": "",
  "error_message": "",
  "gate": {"at": "2026-11-10T07:59:58Z", "outcome": "passed", "exit_code": 0, "message": ""},
  "repeats": 1,
  "verdict": {"verdict": "PASS", "outcome": "pass", "recorded_at": "2026-11-10T08:14:30Z", "report": true}
}
```

`gate` and `verdict` are `null` when absent; `finished_at` is `null`
until known. For `skipped`, `started_at` is the first skip and
`finished_at` the latest, and `repeats` counts them.

On-disk `MemoryEntry`:

```json
{"value": "#412 #415", "rev": 3, "updated_at": "2026-11-10T08:14:00Z", "updated_by": "loop-deps-bump-1a2b3c4d"}
```

`updated_by` is the caller's workspace id for `ws machine memory`, and
`""` for `ws loop memory` (a person or a gate script).

### Limits

| Constant (package `loop`) | Value |
| --- | --- |
| `MaxHistory` | 200 entries |
| `MaxReports` | 50 entries keep a report |
| `MaxReportSize` | 1 MiB |
| `MaxMemoryKeys` | 100 |
| `MaxMemoryValue` | 64 KiB (65536 bytes) |
| `MaxMemoryBytes` | 256 KiB, the sum of all values' bytes |
| `DefaultHistoryLimit` / `MaxHistoryLimit` | 20 / 200 (`--limit`) |

## Status rules (`(*Entry).Settle`)

Input: `Observation{Listed bool, State lifetime-state string, Verdict *Verdict}`.

1. Entry not open (`Workspace == ""` or `Closed`): no change.
2. `!Listed`: `Closed = true`; if `Status == running`, `Status = gone`
   and `FinishedAt = now`. Changed.
3. `Verdict != nil` and differs from the stored one (by `RecordedAt` or
   `Verdict`): store it; `Status = succeeded` when `Outcome == "pass"`,
   else `failed`; `FinishedAt = Verdict.RecordedAt`. Changed.
4. Stored verdict present: no further change.
5. `State` is `working` or `starting`: `Status = running`, `FinishedAt = nil`.
6. `State` is `idle` or `stopped`: `Status = done`; `FinishedAt = now`
   if it was nil.
7. Any other state (`unknown`, `""`): no change.

## CLI

### ws loop history

```
ws loop history [--json] [--limit <N>] [--run <SEQ> | --clear] <NAME>

Show a loop's runs, newest first

Flags:
      --clear       delete the loop's run history and saved reports
      --limit int   how many runs to show, 1 to 200 (default 20)
      --run int     show one run, with its report
```

Text output, one line per run:

```
42  succeeded     2026-11-10T08:00:00Z  14m30s  loop-deps-bump-1a2b3c4d  PASS
41  skipped ×12   2026-11-09T20:00:00Z  -       -                        exit 1: no outdated modules
40  start_failed  2026-11-09T08:00:00Z  -       -                        claude_missing: …
```

JSON, list (`loopHistoryJSON`):

```json
{"loop": "deps-bump", "runs": [RunJSON…], "total": 57, "warnings": []}
```

`RunJSON`:

```json
{
  "seq": 42,
  "status": "succeeded",
  "trigger": "tick",
  "started_at": "2026-11-10T08:00:00Z",
  "finished_at": "2026-11-10T08:14:30Z",
  "duration_seconds": 870,
  "workspace": "loop-deps-bump-1a2b3c4d",
  "workspace_exists": true,
  "session": "e8cdf5d8",
  "error": null,
  "gate": {"at": "2026-11-10T07:59:58Z", "outcome": "passed", "exit_code": 0, "message": null},
  "repeats": 1,
  "verdict": {"verdict": "PASS", "outcome": "pass", "recorded_at": "2026-11-10T08:14:30Z", "report": "/…/state/.loops/deps-bump/reports/42.md"}
}
```

- `duration_seconds`: whole seconds `finished_at − started_at` for
  `done`, `succeeded`, `failed`, `gone` with `finished_at` set; else `null`.
- `workspace`, `session`: `null` when empty. `workspace_exists`:
  `workspace != null && !closed`.
- `error`: `{"code","message"}` (the existing `itemErrorJSON`) when
  `error_code` is set.
- `gate`: the existing `gateJSON`, or `null`.
- `verdict.report`: absolute path of `reports/<seq>.md` if the file
  exists, else `null`.

JSON, `--run SEQ` (`loopHistoryRunJSON`):

```json
{"loop": "deps-bump", "run": RunJSON, "report_text": "# Bumped 3 modules\n…", "warnings": []}
```

`report_text` is `null` without a report. Text output prints the run's
line, then a blank line and the report.

JSON, `--clear` (`loopClearedJSON`):

```json
{"loop": "deps-bump", "removed": 57, "warnings": []}
```

Errors: `loop_not_found` (3) for an unknown loop, and for `--run` with
a seq not in the history (message `run 99 of loop deps-bump not
found`); `usage` (2) for `--run` with `--clear` or `--limit`, a
`--limit` out of 1-200, or a `--run` below 1.

### ws loop memory

```
ws loop memory list  [--json] <NAME>
ws loop memory get   [--json] <NAME> <KEY>
ws loop memory set   [--json] [--expect-rev <N>] [--file <PATH> | --file -] <NAME> <KEY> [<VALUE>]
ws loop memory rm    [--json] [--expect-rev <N>] <NAME> <KEY>
ws loop memory clear [--json] <NAME>
```

Short help: `memory` "Read and change what a loop's runs remember";
`list` "List a loop's memory"; `get` "Print one memory value"; `set`
"Set one memory value"; `rm` "Remove one memory value"; `clear` "Remove
all of a loop's memory".

Flags: `--expect-rev int` "fail with memory_conflict unless the key's
revision is N; 0 means the key must not exist" (default -1 = don't
check); `--file string` "read the value from this file; - reads stdin".
`set` needs exactly one of `<VALUE>` and `--file`.

Text output: `list` prints `KEY  rev N  <updated_at>  <first line of
value, cut to 60 chars>`; `get` prints the value exactly; `set` prints
`KEY  rev N`; `rm` prints `KEY removed`; `clear` prints `N keys removed`.

JSON:

```json
// list
{"loop": "deps-bump", "entries": [MemoryEntryJSON…], "bytes": 1234, "warnings": []}
// get, set
{"loop": "deps-bump", "entry": MemoryEntryJSON, "warnings": []}
// rm
{"loop": "deps-bump", "key": "seen", "removed": true, "warnings": []}
// clear
{"loop": "deps-bump", "removed": 4, "warnings": []}
```

`MemoryEntryJSON` (entries sorted by key):

```json
{"key": "seen", "value": "#412 #415", "rev": 3, "updated_at": "2026-11-10T08:14:00Z", "updated_by": "loop-deps-bump-1a2b3c4d"}
```

`updated_by` is `null` when empty.

### ws machine memory

```
ws machine memory list
ws machine memory get <KEY>
ws machine memory set [--expect-rev <N>] [--file <PATH> | --file -] <KEY> [<VALUE>]
ws machine memory rm [--expect-rev <N>] <KEY>
```

Short help: "Read and change your loop's memory". Always JSON, same
shapes as `ws loop memory` (with `"loop"` set to the caller's loop).
`updated_by` is the caller's workspace id. No `clear`.

### Error codes

| Code | Exit | When | Go sentinel |
| --- | --- | --- | --- |
| `memory_invalid` | 2 | bad key | `loop.ErrMemoryInvalid` |
| `memory_not_found` | 3 | `get`/`rm` of a missing key | `loop.ErrMemoryNotFound` |
| `memory_conflict` | 4 | `--expect-rev` didn't match | `loop.ErrMemoryConflict` |
| `memory_full` | 4 | value over 64 KiB, over 100 keys, or over 256 KiB in all | `loop.ErrMemoryFull` |
| `not_loop_run` | 4 | `ws machine memory` whose root isn't a loop run, or whose loop was removed | `cli.errNotLoopRun` |
| `loop_invalid` | 2 | a data file from a newer ws | `loop.ErrNewerData` |

### Prompt and gate script

- Templated prompt: `{{memory "KEY"}}` is the value, or `""`.
- Gate env, after `WS_LOOP_NAME` and before the `WS_LOOP_VAR_*`:
  `WS_LOOP_HISTORY=<abs path of history.json>`,
  `WS_LOOP_MEMORY=<abs path of memory.json>`.

## Desktop (Tauri) and Workspace Online

Each command is a `#[cfg_attr(feature = "desktop", tauri::command)] pub
async fn` in `desktop/src-tauri/src/loops.rs`, listed in
`generate_handler!` in `lib.rs` and, in the same order, at the end of
`COMMANDS` in `online/dispatch.rs`, with a `dispatch` arm. Online, each is
`POST /api/invoke/<command>` with the camelCase args as the JSON body,
or an `invoke` frame on `/api/ws`; no new routes.

| Command | Args (camelCase) | Runs `ws … --json` | Returns |
| --- | --- | --- | --- |
| `loops_history` | `name: string, limit: number \| null` | `loop history [--limit=N] -- <name>` | `LoopHistory` |
| `loops_history_run` | `name: string, seq: number` | `loop history --run=<seq> -- <name>` | `LoopRunDetail` |
| `loops_history_clear` | `name: string` | `loop history --clear -- <name>` | `LoopCleared` |
| `loops_memory` | `name: string` | `loop memory list -- <name>` | `LoopMemory` |
| `loops_memory_set` | `name, key, value: string, expectRev: number \| null` | `loop memory set [--expect-rev=N] --file=- -- <name> <key>`, with `value` on stdin (`ws_json_with_input`) | `LoopMemorySet` |
| `loops_memory_remove` | `name, key: string` | `loop memory rm -- <name> <key>` | `LoopMemoryRemoved` |
| `loops_memory_clear` | `name: string` | `loop memory clear -- <name>` | `LoopCleared` |

Rust structs deserialize snake_case and serialize camelCase, as `Loop`
does. TypeScript (`desktop/src/api.ts`):

```ts
export type LoopRunStatus = "running" | "done" | "succeeded" | "failed" | "start_failed" | "skipped" | "gone";

export interface LoopRunVerdict {
  readonly verdict: string;
  readonly outcome: "pass" | "fail";
  readonly recordedAt: string;
  readonly report: string | null;
}

export interface LoopHistoryRun {
  readonly seq: number;
  readonly status: LoopRunStatus;
  readonly trigger: string;
  readonly startedAt: string;
  readonly finishedAt: string | null;
  readonly durationSeconds: number | null;
  readonly workspace: string | null;
  readonly workspaceExists: boolean;
  readonly session: string | null;
  readonly error: LoopError | null;
  readonly gate: LoopGate | null;
  readonly repeats: number;
  readonly verdict: LoopRunVerdict | null;
}

export interface LoopHistory {
  readonly loop: string;
  readonly runs: readonly LoopHistoryRun[];
  readonly total: number;
  readonly warnings: readonly string[];
}

export interface LoopRunDetail {
  readonly loop: string;
  readonly run: LoopHistoryRun;
  readonly reportText: string | null;
  readonly warnings: readonly string[];
}

export interface LoopMemoryEntry {
  readonly key: string;
  readonly value: string;
  readonly rev: number;
  readonly updatedAt: string;
  readonly updatedBy: string | null;
}

export interface LoopMemory {
  readonly loop: string;
  readonly entries: readonly LoopMemoryEntry[];
  readonly bytes: number;
  readonly warnings: readonly string[];
}

export interface LoopMemorySet {
  readonly loop: string;
  readonly entry: LoopMemoryEntry;
  readonly warnings: readonly string[];
}

export interface LoopMemoryRemoved {
  readonly loop: string;
  readonly key: string;
  readonly removed: boolean;
  readonly warnings: readonly string[];
}

export interface LoopCleared {
  readonly loop: string;
  readonly removed: number;
  readonly warnings: readonly string[];
}
```

`api` methods: `loopsHistory(name, limit)`, `loopsHistoryRun(name, seq)`,
`loopsHistoryClear(name)`, `loopsMemory(name)`,
`loopsMemorySet(name, key, value, expectRev)`,
`loopsMemoryRemove(name, key)`, `loopsMemoryClear(name)`.
