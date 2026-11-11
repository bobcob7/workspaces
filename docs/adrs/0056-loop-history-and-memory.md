# 0056. A loop keeps a run history and a shared memory in `state/.loops/<name>/`, which outlive the runs' workspaces

- Status: Proposed
- Date: 2026-11-11

## Context

### Problem

A loop ([0048](0048-agent-loop-scheduler.md)) records only its last 20
runs, and each run only as `started_at`, `workspace`, `session` and an
error[^runs]. Whether a run did its job is not recorded anywhere that
lasts: the agent's verdict, if it ran `ws machine result`, is written to
`state/<run>/machine/<run>/result.toml`[^machine-store], and that whole
directory is deleted when the run's workspace is removed[^remove], which
`ws loop tick` does to every run beyond the loop's `keep`[^prune]. A
skipped run (gate script exited non-zero) isn't recorded as a run at all,
only as `last_gate`[^skip]. Separately, each run starts from the same
prompt with no way to know what earlier runs did, so "only report what's
new since last time" has to be rebuilt from scratch every run.

How should `ws` record what each run of a loop did, keep it after the
run's workspace is gone, and give the runs of one loop a place to share
state, without letting agents run anything but `ws machine`?

### What a solution needs

1. **History outlives workspaces.** A run's status, times, verdict and
   report survive `keep` pruning and `ws rm`.
2. **Agents stay inside `ws machine`** ([0046](0046-ws-machine.md)): a
   run reads and writes memory only through `ws machine`, scoped to its
   own loop, with no flag to name another loop.
3. **No new deadlocks or waits.** `ws loop tick` holds `loops/.lock` for
   the whole pass, including gate scripts of up to 10 minutes[^lock];
   nothing an agent or a gate script runs may need that lock.
4. **Existing loops and older `ws` keep working.** No loop record is
   rewritten in a form an older `ws` refuses.
5. **Bounded on disk.** A loop that runs every 5 minutes for a year, or
   an agent that writes in a loop, can't fill the disk.
6. **Overlapping runs don't lose each other's writes.** `ws loop run`
   starts a run even while the last one is working[^run].

### Relevant facts

- `ws loop tick` reads the session listing (`lifetime.List`) only once a
  loop is due[^readpass]; that listing gives each run's session state
  (`working`, `starting`, `idle`, `stopped`, `unknown`)[^states].
- A loop run's workspace has no parent, so it is the root of its tree,
  and `ws machine result` from the run agent, or from any child it
  spawns, lands under `state/<run>/machine/`[^machine-store].
- Names in `state/` starting with `.` are never workspaces[^hidden].
- Reports from `ws machine result` are at most 1 MiB[^report-size].
- Go's `text/template` returns the zero value from `index` on a missing
  map key even with `missingkey=error`, but `{{.Map.KEY}}` fails on a
  missing key[^template].

## Decision

We will keep, per loop, a **history** and a **memory** in
`CONFIG_DIR/state/.loops/<name>/`, written only by `ws`, and expose them
through new commands.

1. **Layout.** `state/.loops/<name>/history.json` (runs),
   `memory.json` (key/value), `reports/<seq>.md` (copied reports),
   `.memory.lock`. JSON, not TOML: these are data stores that `--json`
   prints almost verbatim, with nullable times and arbitrary strings;
   each file has its own `"version": 1`, refused when newer.
   Writes go through a temp file renamed into place.
2. **One history entry per attempt**, with a per-loop increasing `seq`,
   a `trigger` (`tick`, `run`, `run_force`, `imported`), and a `status`:

   | status | meaning | final? |
   | --- | --- | --- |
   | `running` | the agent is working or starting | no |
   | `done` | the agent is idle or stopped, and recorded no verdict | no (a verdict may still come) |
   | `succeeded` / `failed` | the agent's `ws machine result` verdict, judged as `ws machine wait` does (`PASS`/`APPROVED` pass) | no, while the workspace exists |
   | `start_failed` | the run couldn't start: Claude Code check, gate timeout or failure, prompt render, workspace creation | yes |
   | `skipped` | the gate script exited non-zero; consecutive skips with the same exit code and message are one entry with `repeats` | yes |
   | `gone` | the workspace disappeared while the run was `running` | yes |

   Once the workspace is gone the entry is `closed` and never checked
   again; its last status stands.
3. **When it's recorded.** `ws loop tick` and `ws loop run` append the
   entry when the attempt ends (started, failed, or skipped), under
   `loops/.lock` as today. Every tick pass that isn't `--dry-run` then
   **settles** each open entry (workspace set, not closed): it reads the
   session listing (now also when no loop is due but some entry is
   open), and the run's `result.toml`; a new verdict copies `report.md`
   to `reports/<seq>.md`. Pruning settles an entry once more right
   before removing its workspace, so a verdict is never lost to `keep`.
   `ws loop history` reads the file without locking and does not
   settle, so it can lag the truth by one tick.
4. **Retention.** The newest 200 entries; reports for the newest 50
   entries that have one (older entries keep the verdict, lose the
   text). `ws loop history --clear` empties it; `ws loop rm` deletes
   `state/.loops/<name>/`; `ws loop add` deletes a leftover one.
5. **Memory** is a map of key → `{value, rev, updated_at, updated_by}`.
   Keys follow the var-key rule (`[A-Za-z0-9_]`, 1-64, not starting
   with a digit). At most 100 keys, 64 KiB per value, 256 KiB in all.
   Each write takes `.memory.lock` (flock), reads, changes one key, and
   writes; `rev` increases on every set, and `--expect-rev N` makes a set
   or rm a compare-and-swap (`0` = must not exist). Last writer wins
   per key without it. Memory commands never take `loops/.lock`.
6. **Agents** use `ws machine memory list|get|set|rm`. The loop is found
   from the caller's **root** workspace: `ws loop tick`/`run` writes
   `state/<run>/loop.toml` (`version`, `loop`, `started_at`) before the
   agent starts. A caller whose root has no such file fails with
   `not_loop_run`. Children of a run share its loop's memory.
7. **People** use `ws loop history [--limit N] [--run SEQ | --clear] <NAME>`
   and `ws loop memory list|get|set|rm|clear <NAME> …`.
8. **Prompt and gate.** A templated prompt gets a `memory` function:
   `{{memory "KEY"}}` is the value, or `""` when unset. The gate script
   gets `WS_LOOP_MEMORY` and `WS_LOOP_HISTORY`, the absolute paths of
   `memory.json` and `history.json` (which may not exist yet), to read;
   to write it runs `ws loop memory set`.
9. **No loop record version bump.** `loops/<name>.toml` stays version 2
   and keeps `runs` (last 20) as the scheduler's working set for `busy`
   and pruning. The first settle of a loop with no `history.json` seeds
   it from `runs` (`trigger = "imported"`; errors become `start_failed`,
   others `running` and are settled like new runs).
10. **Desktop and Workspace Online** get seven commands, in both
    `generate_handler!` and `online::dispatch::COMMANDS`, and History and
    Memory cards in the loop's detail view.

### Why this option

1. *History outlives workspaces*: the entry, verdict and report are
   copied out of `state/<run>/` before pruning removes it. The weak spot
   is `ws rm` by hand within a minute of the verdict: the entry ends as
   `done`/`running`→`gone` without it.
2. *Agents inside `ws machine`*: the loop is derived from the caller's
   root, never named.
3. *No deadlocks*: memory has its own lock; history is written only
   under `loops/.lock` by `ws` itself; readers don't lock (atomic
   renames).
4. *Compatibility*: old `ws` ignores `state/.loops/` and keeps reading
   version 2 records. It doesn't append history (gaps), and a prompt
   using `memory` fails to render under it (`loop_invalid`, recorded as
   a failed run by the newer `ws` once it ticks again).
5. *Bounded*: 200 small entries, 50 reports of at most 1 MiB, 256 KiB
   of memory per loop.
6. *Overlap*: per-key compare-and-swap; different keys never clobber.

## Alternatives

### Keep the history in the loop record (version 3)

Grow `runs` with status and verdict fields and raise the cap.

**Pros**

- One file per loop; no seeding.

**Cons**

- An older `ws` refuses a version 3 record and stops running the loop,
  rather than only missing history.
- The record, which people edit with `ws loop edit`, gets large and is
  rewritten every pass; reports still need their own files.

### Learn outcomes from Claude Code's transcript instead of verdicts

**Pros**

- Works for prompts that never call `ws machine result`.

**Cons**

- No pass/fail signal in a transcript; parsing it ties `ws` to Claude
  Code's file format, which [0024](0024-claude-code-client.md) avoided.
  `done` covers the "finished without a verdict" case instead.

### `{{.Memory.KEY}}` in the prompt

**Pros**

- Matches `{{.Vars.KEY}}`.

**Cons**

- With `missingkey=error` ([0049](0049-loop-scripts-and-variables.md)),
  it fails on a key no run has written yet, including when `ws loop
  add` checks the prompt with empty memory. The `memory` function
  returns `""` instead.

### Memory as a directory of files, one per key, passed as `$WS_LOOP_MEMORY_DIR`

**Pros**

- Gate scripts and agents could `cat` it.

**Cons**

- Agents may only run `ws machine`; writing files directly bypasses
  limits and locking, and per-key files make atomic multi-key reads
  and the total-size cap harder.

### Environment variable naming the loop for `ws machine memory`

**Pros**

- No marker file.

**Cons**

- `claude --bg` sessions are started by Claude Code's service, not as
  children of `ws`, so `ws` can't rely on its environment reaching the
  agent; and a child spawned by the run would not inherit it.

## Consequences

- `ws loop tick` reads the session listing every pass while any loop has
  an open run, which is almost always (kept workspaces stay open). That
  is one `claude agents --json` per minute.
- Loop prompts that want a verdict should end with "finish with `ws
  machine result PASS|FAIL --file <report>`"; `ws llm loop` says so.
  Without it a run ends as `done`.
- New error codes: `memory_invalid` (2), `memory_not_found` (3),
  `memory_conflict` (4), `memory_full` (4), `not_loop_run` (4).
- [`docs/state.md`](../state.md) gains the `state/.loops/` and
  `state/<id>/loop.toml` rows; [`docs/tools/ws-loop.md`](../tools/ws-loop.md)
  and [`ws-machine.md`](../tools/ws-machine.md) document the commands.
- Tests show compliance: `internal/loop` tests for settle, coalescing,
  retention and compare-and-swap; `internal/cli` tests that pruning
  copies a verdict before removal and that `ws machine memory` refuses a
  non-loop caller; `commands_match_generate_handler` for the desktop.
- Unverified: the cost of the extra listing on a Mac with many agents.

## References

[^runs]: `internal/loop/loop.go`, `Run` and `MaxRuns = 20`, at commit 074de35.
[^machine-store]: `internal/machine/store.go`, `Store.Dir`: `<stateDir>/<root>/machine/<child>`; `internal/cli/machine_result.go`, `runMachineResult` uses `machine.Root(list, caller)`.
[^remove]: `internal/workspace/remove.go`, `Store.Remove`: `os.RemoveAll(s.stateOf(ws.ID))`.
[^prune]: `internal/cli/loop_tick.go`, `pruneRuns`, calls `lifetime.RemoveTree` on runs beyond `l.Keep`.
[^skip]: `internal/cli/loop_tick.go`, `tickLoop`: `if !skipped { l.AddRun(run) }`.
[^lock]: `internal/cli/loop_tick.go`, `tick` holds `a.loops.Lock` for the pass; `internal/loop/loop.go`, `MaxScriptTimeout = 10 * time.Minute`.
[^run]: `docs/tools/ws-loop.md`, `ws loop run`: "whether or not the loop is paused, due, or busy".
[^readpass]: `internal/cli/loop_tick.go`, `tickLoop` calls `readPass` only after the paused and due checks.
[^states]: `internal/lifetime/lifetime.go`, `State` constants.
[^hidden]: `docs/state.md`, "Hidden names. A name in `state/` that starts with `.` is never a workspace."
[^report-size]: `internal/machine/result.go`, `MaxReportSize = 1 << 20`.
[^template]: Measured 2026-11-11 on the ws-online pod (linux/arm64, Go from `go.mod`): a template with `Option("missingkey=error")` printed `[]` for `{{index .M "X"}}` with an empty map and returned no error.
