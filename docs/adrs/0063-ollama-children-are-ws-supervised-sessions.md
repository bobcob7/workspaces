# 0063. An Ollama child is a `ws`-supervised session of kind `ollama`

- Status: Proposed
- Date: 2026-11-13

## Context

### Problem

`ws` ties every workspace to exactly one session
([0015](0015-workspace-lifetime-follows-session.md)). An agent session is a
Claude Code background session that `ws` reads through `claude agents
--json` ([0024](0024-claude-code-client.md)). An Ollama-backed child
([0062](0062-ollama-runner-built-into-ws.md)) isn't one. How is it
started, listed, waited for, logged, stopped, removed and restored, so
that `ws machine` and the desktop app treat it like any other child?

### What a solution needs

1. **The contract holds.** No existing JSON field, error code or exit code
   changes ([0016](0016-output-contract.md)); there are additions only.
   `ws machine wait`'s finished and outcome rules
   ([0050](0050-machine-wait-results-and-roles.md)) apply unchanged.
2. **Lifetime follows the session**, as in 0015: a workspace is never
   removed early, and goes when its session is deleted.
3. **Older `ws` binaries leave it alone.** On a machine where an older
   `ws` (or the desktop app's bundled one) runs, a record it can't read
   must be kept, never removed.
4. **Observable.** An orchestrator can read what the child did, like
   `claude logs`, and its token use.
5. **Survives `ws machine spawn` exiting**, and ideally a reboot.

### Relevant facts

- `state/<id>/session` holds `kind` (`claude` or `shell`) and
  `claude_session_id`. `Session.check` rejects an unknown kind, and the
  removal pass keeps a workspace whose record doesn't parse, with a
  warning[^judge].
- `lifetime.Session` reports `kind` (`agent`, `shell`) and `state`
  (`working`, `idle`, `starting`, `stopped`, `unknown`, `running`). `ws
  list --json` prints them, and 0016 tells consumers to accept enum values
  they don't know[^contract].
- `internal/proc` reads a process's start time on Linux and macOS, which
  `ws` already uses to detect stale agents (ADR 0018)[^proc].
- `internal/claude` starts Claude Code's background service in its own
  session, with stdio on `/dev/null` and the home directory as working
  directory, so it holds nothing of `ws`'s (ADR 0024, item 8).
- A Claude child that has finished stays `idle` with a process. `wait`
  counts it finished once it has a result, or after two quiet polls[^wait].

## Decision

We will add a third session kind, **`ollama`**, supervised by `ws`
itself.

**Records.** `state/<id>/session` gains `kind = "ollama"` and
`runner_session_id = "ol-<8 hex>"`. A record with the new key is written
as `version = 2`, so an older `ws` reports "written by a newer ws" and
keeps the workspace. `state/<id>/runner/` holds:

| File | Written by | Holds |
| ---- | ---------- | ----- |
| `run.toml` | `ws machine spawn` | version, endpoint name and URL, model, context tokens, role, budgets, prompt, `created_at` |
| `status.json` | the runner, replaced atomically on every change | `pid`, `process_started_at`, `state` (`starting`, `working`, `waiting_model`, `exited`), `step`, `usage`, `exit` (reason or `null`), `updated_at` |
| `transcript.jsonl` | the runner, appended | one event per line: request, response, tool call, tool result, compaction, retry, error |
| `runner.log` | the runner's stderr | its own `slog` JSON log |
| `stop` | `ws rm` | asks the runner to stop after the current step |

**Starting.** `ws machine spawn --runner ollama` creates the workspace,
preflights the endpoint
([0064](0064-choosing-the-runner-endpoint-and-model.md)), writes
`run.toml`, and starts `ws runner run <id>` the way Claude Code's service
is started: its own session (`setsid`), stdio on `/dev/null` except
stderr, which goes to `runner.log`, and the workspace's session directory
as its working directory. It then records the session and returns once
`status.json` exists, or fails after 10 s with `runner_failed`. On any
failure it removes the workspace, as `startAgent` does today.

**Reading the state** (`lifetime`). An `ollama` session is listed with
`kind: "agent"`, because it is one, and `runner: "ollama"`:

| `status.json` and process | `state` |
| ------------------------- | ------- |
| `pid` alive and its start time matches `process_started_at`; `state` is `working` or `waiting_model` | `working` |
| no `status.json` yet, under 10 s since `session` was recorded | `starting` |
| `exit` set (the runner finished) | `idle` |
| `pid` dead or reused, `exit` not set (crashed, killed, rebooted) | `stopped` |
| `status.json` unreadable | `unknown` |

A finished runner reads as **`idle`**, like a Claude agent that finished,
so the desktop app and `wait` treat both the same. `pid` is `null` once
the process has exited, and `stale` is `null` always.

**Gone.** The session is gone when `state/<id>/runner/` no longer exists.
Only `ws rm` (and so `ws machine rm`) deletes it: it writes `stop`, sends
`SIGTERM` to the runner's process group, waits up to 10 s, sends
`SIGKILL`, then removes the workspace as today.

**`ws machine wait` and `result`.** These are unchanged. The runner
records the verdict through the same `machine.Store` as `ws machine
result`. When it stops without a verdict from the model, for any reason
other than `ws rm`, it records the role's first `fail` verdict (`FAIL`
for a child without a role) with a generated report that starts `ws
ollama runner: <exit reason>` ([0065](0065-ollama-agent-loop-tools-and-budgets.md)).
So a runner that crashed outright is `missing`, and any other stop is
`fail` with a reason.

**Usage outlives the child.** On exit, the runner writes
`state/<root>/machine/<child>/usage.json` (requests, prompt and completion
tokens, steps, wall time, exit reason, model, endpoint) next to the
result, so it survives `ws machine rm` as the report does.

**Logs.** A new command, `ws machine logs <workspace> [--tail N]`, prints
a descendant's transcript as JSON. For a Claude child it returns
`claude logs <session>`'s text. It stands in for `claude logs`, which
`ws llm` tells agents to run and which doesn't know `ol-` sessions.

**Restore.** `ws restore` restarts an `ollama` session whose state is
`stopped`: it starts `ws runner run <id> --resume`, which rebuilds the
conversation from `transcript.jsonl` and carries on with the step budget
that is left. Finished (`idle`) runners aren't restarted. `ws restart`
doesn't touch runners: they don't depend on Claude Code's service.

**Not supported for `ollama` sessions:** `ws claude` (people start Claude
agents; a `--runner` there is an open question), `claude attach`, `send`
and `ws continue`. The desktop app hides Attach and Send for them.

### Why this option

1. **Contract.** `kind` stays `agent`, so consumers that branch on it
   keep working. `runner`, `runner_session`, and an `ollama` object are
   new fields. `idle` after finishing makes `wait`'s rules, and the
   desktop's Needs input and Done states, apply as they are.
2. **Lifetime.** Two signals have to agree before anything is removed: no
   runner directory, and `ws rm` having run. Nothing is removed on a
   timer, as in 0015.
3. **Older ws.** The version 2 session record and the unknown kind both
   make an older `ws` keep the workspace and warn[^judge].
4. **Observable.** The transcript and status are plain files that the
   desktop app can read, as it reads Claude transcripts today.
5. **Survives.** `setsid` detaches the runner from the short-lived `ws`
   that spawned it, and `--resume` brings it back after a reboot.

Where it's weak: `ws` now supervises processes, including PID reuse
(checked by start time) and orphaned runners if the state directory is
deleted by hand. The `ol-` session id is a new form that Claude Code's
commands don't understand. `--resume` replays a transcript into a model
that may answer differently.

## Alternatives

### A Claude Code background session that shells out to the runner

`claude --bg` with a prompt that runs `ws runner run` and waits.

**Pros**

- No new session kind.

**Cons**

- It burns Claude tokens and a Claude session for every on-prem child,
  which defeats the purpose.
- The Claude session's state says nothing true about the runner.

### Report a new `kind: "ollama"` in `ws list`

**Pros**

- Simpler to explain.

**Cons**

- Orchestrator scripts that test `kind == "agent"` (the handbook's
  `jq` filters) would skip these children. 0016 allows it, but it breaks
  them in practice. The desktop app happens to treat any kind but `shell`
  as an agent (`model.rs`, `workspace_entry`), so it would cope, but
  `ws`'s own guides describe `kind` as `agent` or `shell`.

### `stopped` for a finished runner

**Pros**

- It's literally true: there's no process.

**Cons**

- `ws restore` and the desktop's Restore action would offer to restart
  finished work, and `stopped` already means "killed by a reboot" to
  people.

### A per-user systemd or launchd unit per child

**Pros**

- Supervision and restarts come for free.

**Cons**

- It differs per OS, needs a user bus that doesn't exist in the ws-online
  pod, and adds one unit file per child to clean up.

## Consequences

- `internal/workspace` session records gain version 2 and the new kind.
  `internal/lifetime` gains a `runnerReader` interface beside
  `claudeClient`. `internal/runner` (process start, status, stop, resume)
  is the only package that starts `ws runner`, enforced by a `make lint`
  rule.
- `ws list --json`, `ws machine spawn`, `children` and `wait` gain fields;
  [reference.md](../design/ollama-agent/reference.md) lists them.
- New error code `runner_failed` (exit 1).
- How we'll know: `ws list --json` on an Ollama child shows `kind: "agent"`,
  `runner: "ollama"`, and the state table above, in a test with a fake
  runner process. A version 2 session record makes the current `ws` keep
  the workspace (`TestManager_*` warnings).
- Unverified: how a runner behaves when the ws-online pod restarts. It
  should be `stopped`, then restored, but this hasn't been run.

## References

[^judge]: workspaces `internal/lifetime/judge.go` and `internal/workspace/session.go` @98697f3 (read 2026-11-13). `judge`: "case rec.err != nil: r.m.warn("kept workspace %s: %v", id, rec.err); return nil, keep". `check`: "return fmt.Errorf("unknown session kind %q", sess.Kind)".
[^contract]: [ADR 0016](0016-output-contract.md). "Enum values are lowercase strings. Consumers must accept values they don't know."
[^proc]: workspaces `internal/proc/proc.go` @98697f3 (read 2026-11-13). `func (Reader) StartTime(pid int) (time.Time, error)`, with Linux and Darwin implementations.
[^wait]: [ADR 0050](0050-machine-wait-results-and-roles.md), "4. `ws machine wait`". "A child is finished when its workspace is gone, or when it is neither `working`, `starting` nor `unknown`, none of its descendants is `working` or `starting`, and either it has recorded a result or it was also not busy at the previous poll."
