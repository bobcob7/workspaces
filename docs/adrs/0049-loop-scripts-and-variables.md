# 0049. A loop can have a gate script that must exit 0 before a run starts, and variables; its prompt is a Go template of both, in a version 2 record

- Status: Proposed
- Date: 2026-11-05

## Context

### Problem

A loop ([0048](0048-agent-loop-scheduler.md)) starts an agent every
period whether or not there is work. "Triage new issues every hour"
starts a paid agent every hour even when no issue is new, and the prompt
can't say which issues are new, so the agent has to find them again. Two
things are missing: a cheap check that runs at the loop's time and can
say "nothing to do", whose findings reach the prompt; and per-loop
values (a repo, a label, a channel) that the prompt can use, so one
prompt text serves several loops. How should a loop carry a script and
key-value pairs, how should the script run, and how do its output and
the values get into the prompt, without breaking the loops that exist
or letting an older `ws` run a gated loop ungated?

### What a solution needs

1. **Fail closed.** A loop with a script never starts an agent unless
   the script exited 0: not after a timeout, not when the script can't
   start, and not under an older `ws` that doesn't know about scripts.
2. **Existing loops keep working unchanged**, including prompts that
   contain `{{` literally.
3. **Bounded.** A script that hangs or prints megabytes can't stall the
   scheduler forever or produce a prompt Claude Code can't take.
4. **Mistakes show up at `ws loop add`**, not hours later at the first
   run.
5. **Visible**: `ws loop list`, `tick`, and the desktop app say what the
   gate decided last.
6. **Small**: additive JSON, no new long-lived state, and the same
   machinery as 0048 for everything after the gate.

### Relevant facts

- **Templates.** Go's `text/template` with `Option("missingkey=error")`
  stops "immediately with an error" when "a map is indexed with a key
  that is not present"[^template-option]. Executing `{{.Vars.NOPE}}` or
  `{{.Stdout}}` against a map without that key fails; `{{index .Vars
  "NOPE"}}`, and a missing key inside a branch not taken (`{{if
  false}}{{.Vars.NOPE}}{{end}}`), don't fail; `{{.Vars.my-key}}` is a
  parse error (`bad character U+002D '-'`); `{{"{{"}}` prints
  `{{`[^tpl-probe].
- **Older `ws` and new keys.** `ws` 4dcd28c skips a loop record with a
  key it doesn't know (`unknown key script`), and one whose `version` is
  2 (`written by a newer ws (version 2; this ws reads up to 1)`), each
  with a warning; it never rewrites either[^old-ws]. A version-1 record
  whose prompt contains `{{.Vars.REPO}}`, and no new keys, would be read
  by it as a literal prompt.
- **Argument size.** A run's prompt is one argument to `claude`
  ([0048](0048-agent-loop-scheduler.md), decision 4). `getconf ARG_MAX`
  is 1048576 bytes on macOS 26.6.2[^arg-max].
- **Child processes.** `exec.Cmd.WaitDelay` "bounds the time spent
  waiting on … a child process that fails to exit after the associated
  Context is canceled, and a child process that exits but leaves its I/O
  pipes unclosed"[^os-exec]. Killing only the shell can leave its
  children holding the pipes, which `WaitDelay` then closes.
- **Where `exec` runs.** `make lint` forbids running `git`, `claude`,
  and `gh` outside their packages (ADRs [0003](0003-use-git-cli.md),
  [0024](0024-claude-code-client.md), [0039](0039-ws-review-command.md));
  `internal/shell` and `internal/power` run other programs directly.
- **Cost.** Starting `/bin/sh -c true` took under 0.01 s[^sh-cost];
  a tick with nothing due costs about what `ws list` does
  ([0048](0048-agent-loop-scheduler.md)), and `tick` holds
  `CONFIG_DIR/loops/.lock` for the whole pass.

## Decision

We will add to `ws loop add`:

```
--script <TEXT>          a shell script that must exit 0 for a run to start
--script-timeout <DUR>   default 1m; 1s to 10m, whole seconds; only with --script
--var <KEY>=<VALUE>      repeatable
```

1. **Record version 2.** `CONFIG_DIR/loops/<NAME>.toml` gains `script`,
   `script_timeout`, a `[vars]` table, and a `[last_gate]` table (`at`,
   `outcome`, `exit_code`, `message`). `FormatVersion` becomes 2. `add`
   writes version 2, always. `Save` keeps the version a record already
   has, so a version-1 record stays version 1 when `tick` rewrites it.
   This `ws` reads both; an older one skips version 2 with a warning
   ([0027](0027-respawn-lock-and-record-versions.md)).
2. **The prompt is a template in version 2, and literal in version 1.**
   A version-2 prompt is parsed with `text/template`, option
   `missingkey=error`, and executed with a map holding `Name` (the
   loop's name), `Vars` (a `map[string]string`), and, only when the loop
   has a script, `Stdout` and `Stderr`. So `{{.Vars.REPO}}`,
   `{{.Stdout}}`, `{{.Name}}`. A literal `{{` is written `{{"{{"}}`. A
   version-1 prompt is used as it is, forever; there's no command that
   turns a loop into version 2 except `rm` and `add`.
3. **Checked at `add`.** `add` parses the prompt and executes it with the
   loop's vars and empty `Stdout` and `Stderr` (when there's a script). A
   parse error, `{{.Stdout}}` without `--script`, or `{{.Vars.X}}` with
   no `--var X=…`, is `loop_invalid`. Keys match `[A-Za-z_][A-Za-z0-9_]*`
   and are at most 64 bytes; values at most 4096 bytes; at most 32 vars;
   a key given twice is `loop_invalid`. A blank `--script` is no script.
   A key only reached in a branch, or through `index`, can still be
   missing at run time; that run fails with `loop_invalid`.
4. **How the script runs.** `/bin/sh -c <script>`, from the user's home
   directory, with stdin empty and ws's own environment plus
   `WS_LOOP_NAME=<name>` and `WS_LOOP_VAR_<KEY>=<value>` for each var.
   It runs in its own process group; at the timeout the group is sent
   `SIGKILL`, and `WaitDelay` is 5 s. Stdout and stderr are kept up to
   64 KiB each, the rest discarded with a warning; invalid UTF-8 becomes
   U+FFFD, and trailing `\n` and `\r` are trimmed. All exec code lives in
   a new package `internal/gate`, and `make lint` gains a rule that
   `/bin/sh` runs through `exec.Command` only there.
5. **When it runs.** Only when a run would otherwise start: `tick` checks
   `paused`, due, `asleep`, and `busy` first, then Claude Code's check,
   then runs the script, then renders the prompt, then starts the run as
   0048 does. `tick --dry-run` never runs a script; a due loop is
   `would_start` as before.
6. **What it decides.** Each outcome is saved in `last_gate`:
   - exit 0 → `passed`: the run starts with the rendered prompt;
   - any other exit, or death by a signal → `skipped`: a new `tick`
     action. `next_run_at` advances one period, as after any run; no run
     is added to `runs`; `message` is the last non-blank line of stderr,
     up to 200 bytes;
   - timeout → `timed_out`, and the script can't start → `failed`: the
     item is `failed` with the new code `loop_script_failed` (exit 1),
     `next_run_at` advances, and a failed run is recorded, as for a
     start that fails today.
   Output isn't stored anywhere except in the prompt it renders into.
7. **`ws loop run [--force]`** runs the gate too. A non-zero exit is the
   error `loop_skipped` (exit 4) and starts nothing; `--force` skips the
   script, and `Stdout` and `Stderr` render empty. `run` still leaves
   `next_run_at` alone, and saves `last_gate` unless `--force`.
8. **Output** is additive. A loop gains `"script"` (null when none),
   `"script_timeout"` (null when no script), `"vars"` (an object, `{}`
   when none), and `"last_gate"` (null, or `{"at", "outcome",
   "exit_code", "message"}`). A `tick` item gains `"gate"`, the same
   object for this pass, or null when no script ran.
   [`docs/tools/ws-loop.md`](../tools/ws-loop.md) has every shape.
9. **No `ws loop edit`.** Changing a script, vars, or prompt is `rm` then
   `add`. The desktop app's **New loop** form gets a script field, a
   timeout, and a key-value editor, and a loop's details show its script,
   vars, and last gate.

### Why this option

1. **Fail closed.** Only exit 0 starts a run; every other outcome is
   `skipped` or `failed`. The version bump means no older `ws` reads a
   gated or templated loop at all[^old-ws]: the unknown-key check
   already protects gated loops by accident, but a templated prompt
   without new keys would run literally, and the version is the contract
   0027 set for this.
2. **Existing loops.** Version-1 prompts are never parsed, so a prompt
   with `{{` keeps running word for word, and keeping the version on
   `Save` stops `tick` from changing its meaning by rewriting it.
3. **Bounded.** The timeout, the process-group kill, and `WaitDelay`
   bound the wait even when the script leaves children behind[^os-exec].
   Two 64 KiB outputs plus 32 × 4 KiB of vars stay well under
   `ARG_MAX`[^arg-max]. The cheap checks run first, so a script never
   runs for a paused, sleeping, or busy loop.
4. **Early mistakes.** `missingkey=error` turns a typo in a var name or a
   `{{.Stdout}}` with no script into `loop_invalid` at `add`[^tpl-probe].
   It can't catch keys used only in a branch or through `index`, which is
   why a render failure at run time is a `failed` item, not a crash.
5. **Visible.** `last_gate` is one small table, overwritten each time,
   so the record doesn't grow and `list` and the app can show "skipped
   12:00, exit 1: no new issues". A skip isn't a run, so `runs` and
   retention keep meaning "agents that started".
6. **Small.** `text/template` is in the standard library and is what Go
   users already know. The script is text in the record, so `--script
   ./check.sh` and a one-liner both work, and the record says exactly
   what runs. Everything after the gate is 0048 unchanged.

Its weak points: a script runs while `tick` holds the loops lock, so a
slow script delays every later loop in the pass, and a second `tick`
waits, by up to the timeout (10 minutes at most). The home directory is
a guess at a useful working directory; a script that needs a repo has to
`cd` or use `ws cache path`. Vars and scripts are plain text in a 0600
file, not a place for secrets. Without an edit command, changing a
loop's script loses its run history, and its old runs' workspaces are no
longer pruned by it.

## Alternatives

### Simple `${KEY}` substitution instead of a template

Replace `${STDOUT}`, `${STDERR}`, and `${KEY}` in the prompt.

**Pros**

- No escaping rules to learn; works on version-1 prompts as they are.

**Cons**

- `${…}` already appears in prompts that talk about shell code, so it
  needs an escape just as `{{` does, and has no conditionals: a prompt
  can't say "if stderr is non-empty, mention it".
- We'd write and test a parser that `text/template` already provides.

### A script path instead of script text

`--script` names an executable file that `ws` runs directly.

**Pros**

- No shell; arguments aren't reparsed.

**Cons**

- The record no longer says what runs; editing the file changes the
  loop silently. `/bin/sh -c ./check.sh` covers the path case anyway.

### Keep version 1 and rely on the unknown-key check

Add the keys without bumping `version`.

**Pros**

- No version logic in `Save`.

**Cons**

- A templated prompt with no script or vars has no new keys, so an
  older `ws` runs it with `{{.Name}}` in it literally[^old-ws]. The
  unknown-key refusal is a side effect, not the contract.

### Count a skip as a run

Record each skip in `runs`.

**Pros**

- Full history of the gate's decisions.

**Cons**

- A gate that says "nothing to do" every 15 minutes would push real runs
  out of the 20 kept within five hours, and `last_run` would stop
  meaning the last agent.

### A `ws loop edit` command now

**Pros**

- Changing a script keeps the loop's history and its workspaces' pruning.

**Cons**

- It needs rules for every field (can `every` change? does `next_run_at`
  move?) that this decision doesn't need; it can be added later without
  changing anything here.

## Consequences

- New package `internal/gate` (runs a script with a timeout, capped
  output, and a process group), and `internal/loop` gains the version-2
  fields, var and timeout validation, and prompt rendering.
- [0016](0016-output-contract.md)'s status line notes `loop_skipped` and
  `loop_script_failed`; [0048](0048-agent-loop-scheduler.md)'s notes the
  gate, the template, and record version 2.
- `ws loop`'s guides (`internal/cli/guides/ws-loop*.md`) gain the new
  flags, the template values, `skipped`, and the two codes.
- Conformance: unit tests in `internal/loop` pin version handling,
  validation, and rendering (including a version-1 prompt with `{{`);
  tests in `internal/gate` run real `/bin/sh` scripts for exit codes,
  timeout with a child holding the pipe, the output cap, trimming, and
  the environment; tests in `internal/cli` pin `tick`'s order (no script
  for paused, waiting, asleep, busy, or `--dry-run`) and each outcome
  with a fake gate; `make lint` pins where `/bin/sh` runs; the desktop
  tests pin the new form fields and details.
- A script's child that leaves the process group (with `setsid` or
  `nohup … &` into a new session) isn't killed at the timeout; `WaitDelay`
  only closes its pipes, so `ws` returns but the child keeps running.
- **Unverified:** that `SIGKILL` to the process group plus a 5 s
  `WaitDelay` always returns within timeout + 5 s on macOS and Linux.
  Checked by a test in `internal/gate` whose script backgrounds a
  `sleep 60` that holds stdout, with a 1 s timeout.

## References

[^template-option]: Go 1.27.1, `go doc text/template.Template.Option`, read 2026-11-05. "missingkey: Control the behavior during execution if a map is indexed with a key that is not present in the map. … "missingkey=error" Execution stops immediately with an error."
[^tpl-probe]: Measured 2026-11-05 on an Apple M4, macOS 26.6.2, Go 1.27.1: a program parsed each template with `template.New("p").Option("missingkey=error")` and executed it with `{"Name": "deps", "Vars": {"REPO": "x"}}`. `{{.Vars.REPO}}` → `x`; `{{.Vars.NOPE}}` → `map has no entry for key "NOPE"`; `{{.Stdout}}` → `map has no entry for key "Stdout"`; `{{index .Vars "NOPE"}}` → empty, no error; `{{if false}}{{.Vars.NOPE}}{{end}}` → empty, no error; `{{.Vars.my-key}}` → parse error `bad character U+002D '-'`; `{{"{{"}}` → `{{`. One run each; the results are deterministic.
[^old-ws]: Measured 2026-11-05 with `ws` built from 4dcd28c: `ws loop add demo --every 15m --prompt hi` in an empty config dir, then appending `script = "true"` to the record and running `ws loop list --json` gave `"warnings":["skipping demo.toml: parsing …/demo.toml: unknown key script"]`; removing that line and setting `version = 2` gave `"skipping demo.toml: written by a newer ws (version 2; this ws reads up to 1); upgrade ws"`. Both listed no loops and left the file unchanged.
[^arg-max]: `getconf ARG_MAX` on macOS 26.6.2 (25G83), 2026-11-05: `1048576`.
[^os-exec]: Go 1.27.1, `go doc os/exec.Cmd`, read 2026-11-05. "If WaitDelay is non-zero, it bounds the time spent waiting on two sources of unexpected delay in Wait: a child process that fails to exit after the associated Context is canceled, and a child process that exits but leaves its I/O pipes unclosed. … if the I/O pipes communicating with the child process are still open, those pipes are closed in order to unblock any goroutines currently blocked on Read or Write calls."
[^sh-cost]: Measured 2026-11-05 on an Apple M4, macOS 26.6.2: `/usr/bin/time -p /bin/sh -c true`, 3 runs, real 0.00 s each (below the timer's 0.01 s resolution).
