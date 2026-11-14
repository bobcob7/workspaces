# 0063. A `ws`-runner child is a session of kind `runner`, kept in files and supervised by the `ws` daemon

- Status: Proposed
- Date: 2026-11-13; reworked 2026-11-14 after review (supervision moved
  to [0068](0068-ws-daemon.md); names made provider-neutral)

## Context

### Problem

`ws` ties every workspace to exactly one session
([0015](0015-workspace-lifetime-follows-session.md)). An agent session is a
Claude Code background session that `ws` reads through `claude agents
--json` ([0024](0024-claude-code-client.md)). A child run by `ws`'s own
runner ([0062](0062-agent-runner-built-into-ws.md)) isn't one. How is it
recorded, listed, waited for, logged, stopped, removed and restored, so
that `ws machine` and the desktop app treat it like any other child?

### What a solution needs

1. **The contract holds.** No existing JSON field, error code or exit code
   changes ([0016](0016-output-contract.md)); there are additions only.
   `ws machine wait`'s finished and outcome rules
   ([0050](0050-machine-wait-results-and-roles.md)) apply unchanged.
2. **Lifetime follows the session**, as in 0015.
3. **Older `ws` binaries leave it alone**: a record they can't read is
   kept, never removed.
4. **Observable**: an orchestrator can read what the child did, and its
   token use, and every agent, Claude or not, reports its model the same
   way.
5. **Survives** `ws machine spawn` exiting, a crash, and a reboot
   ([0068](0068-ws-daemon.md)).

### Relevant facts

- `state/<id>/session` holds `kind` (`claude` or `shell`) and
  `claude_session_id`. `Session.check` rejects an unknown kind, the reader
  rejects unknown keys, and the removal pass keeps a workspace whose record
  doesn't parse, with a warning[^judge].
- `lifetime.Session` reports `kind` (`agent`, `shell`) and `state`
  (`working`, `idle`, `starting`, `stopped`, `unknown`, `running`). 0016
  tells consumers to accept enum values they don't know[^contract].
- `internal/proc` reads a process's start time on Linux and macOS[^proc].
- `claude agents --json` reports no model. A Claude session's transcript
  records the model on every assistant entry (`"model":"claude-opus-5-5"`),
  and `internal/claude` already reads transcript tails[^transcript].
- A Claude child that has finished stays `idle` with a process. `wait`
  counts it finished once it has a result, or after two quiet polls[^wait].

## Decision

We will add a third session kind, **`runner`**, whose process the `ws`
daemon supervises ([0068](0068-ws-daemon.md)) and whose state anyone reads
from files.

**Records.** `state/<id>/session` gains `kind = "runner"` and
`runner_session_id = "rn-<8 hex>"`, written as `version = 2`, so an older
`ws` reports "written by a newer ws" and keeps the workspace.
`state/<id>/runner/` holds:

| File | Written by | Holds |
| ---- | ---------- | ----- |
| `run.toml` | `ws machine spawn` | version, model alias and id, endpoint name, URL and `api`, context tokens, role, budgets, MCP selection, prompt, `created_at` |
| `status.json` | the runner, replaced atomically on every change | `pid`, `process_started_at`, `state` (`starting`, `working`, `waiting_model`, `exited`), `step`, `usage`, `restarts`, `mcp` (each server's state and tool count), `exit` (reason or `null`), `updated_at` |
| `transcript.jsonl` | the runner, appended | one event per line: request, response, tool call, tool result, compaction, retry, MCP event, error |
| `runner.log` | the runner's stderr | its own `slog` JSON log |
| `stop` | `ws rm` (through the daemon, or directly) | asks the runner to stop after the current step |

**Starting.** `ws machine spawn --runner ws` resolves the model and asks
the daemon to preflight it ([0064](0064-choosing-the-runner-model-and-endpoint.md)),
creates the workspace, writes `run.toml`, records the session, and asks the
daemon to start the runner. The daemon starts `ws runner run <id>` in its
own session, stdio on `/dev/null` except stderr (`runner.log`), with the
workspace's session directory as working directory and the environment of
[0068](0068-ws-daemon.md). Spawn returns once `status.json` exists, or fails
after 10 s with `runner_failed`. On any failure it removes the workspace, as
`startAgent` does today.

**Reading the state** (`lifetime`). A `runner` session is listed with
`kind: "agent"`, because it is one, and `runner: "ws"`:

| `status.json` and process | `state` |
| ------------------------- | ------- |
| `pid` alive and its start time matches `process_started_at`; `state` is `working` or `waiting_model` | `working` |
| no `status.json` yet, under 10 s since `session` was recorded; or the daemon is between a crash and its resume (`restarts` grew, no `exit`, under 3 min) | `starting` |
| `exit` set (the runner finished) | `idle` |
| `pid` dead or reused, `exit` not set, and no resume pending | `stopped` |
| `status.json` unreadable | `unknown` |

A finished runner reads as **`idle`**, like a Claude agent that finished,
so the desktop app and `wait` treat both the same. `pid` is `null` once the
process has exited, and `stale` is `null` always.

**Every agent reports its model.** `session.model` is new for every
agent: for a `ws` runner, the model id from `run.toml`; for a Claude agent,
the `model` of the last assistant entry in its transcript tail (for
example `claude-sonnet-5-5`), else `null` until it has answered. A shell's
is `null`. `runner` is `claude` or `ws` for an agent and `null` for a
shell; it's the functional field (what runs it), and `model` is the label.
Runner-only details (endpoint, step, budgets, usage, exit) are in a
separate `ws_runner` object, `null` for Claude agents.

**Gone.** The session is gone when `state/<id>/runner/` no longer exists.
Only `ws rm` (and so `ws machine rm`) deletes it: it asks the daemon to
stop the runner (write `stop`, `SIGTERM` to its process group, wait up to
10 s, `SIGKILL`), or does that itself when the daemon is unavailable, then
removes the workspace as today.

**`ws machine wait` and `result`.** These are unchanged. The runner
records the verdict through the same `machine.Store` as `ws machine
result`. When it stops without a verdict from the model, for any reason
other than `ws rm`, it (or the daemon, for `crashed`) records the role's
first `fail` verdict (`FAIL` for a child without a role) with a generated
report that starts `ws runner: <exit reason>`
([0065](0065-runner-agent-loop-tools-and-budgets.md)). A child is
`missing` only if neither got to write.

**Usage outlives the child.** On exit, `state/<root>/machine/<child>/usage.json`
(requests, tokens, steps, wall time, exit reason, model, endpoint,
restarts) is written next to the result, so it survives `ws machine rm`.

**Logs.** `ws machine logs <workspace> [--tail N]` prints a descendant's
transcript as JSON, or for a Claude child the text of `claude logs
<session>`. It stands in for `claude logs`, which `ws llm` tells agents to
run and which doesn't know `rn-` sessions.

**Restore.** `ws restore` asks the daemon to resume each `runner` session
whose state is `stopped`. The daemon also does that by itself when it
starts ([0068](0068-ws-daemon.md)). `ws restart` doesn't touch runners.

**Not supported for runner sessions:** `ws claude --runner` (people start
Claude agents; open question), `claude attach`, `send` and `ws continue`.
The desktop app hides Attach and Send for them.

### Why this option

1. **Contract.** `kind` stays `agent`, so consumers that branch on it keep
   working. `runner`, `model` and `ws_runner` are new fields. `idle` after
   finishing makes `wait`'s rules, and the desktop's states, apply as they
   are.
2. **Lifetime.** Two signals have to agree before anything is removed: no
   runner directory, and `ws rm` having run.
3. **Older ws.** The version 2 record and the unknown kind both make an
   older `ws` keep the workspace and warn[^judge].
4. **Observable.** Plain files the desktop app can read, and one `model`
   field for every agent.
5. **Survives**: the daemon restarts and resumes; reading never depends on
   it.

Where it's weak: the `rn-` session id is a form Claude Code's commands
don't understand. `--resume` replays a transcript into a model that may
answer differently. A Claude agent's `model` costs a transcript-tail read
per agent in `ws list`, and is `null` before its first answer.

## Alternatives

### A Claude Code background session that shells out to the runner

**Pros**

- No new session kind.

**Cons**

- It burns Claude tokens and a Claude session for every on-prem child.

### Report a new `kind` in `ws list`

**Pros**

- Simpler to explain.

**Cons**

- Orchestrator scripts that test `kind == "agent"` would skip these
  children.

### `stopped` for a finished runner

**Pros**

- It's literally true: there's no process.

**Cons**

- `ws restore` and the daemon would restart finished work.

### Store a Claude agent's model in its session record

**Pros**

- No transcript read.

**Cons**

- The record rejects unknown keys, so an older `ws` would warn on every
  Claude workspace; and the requested alias (`sonnet`) isn't the model
  that answered.

## Consequences

- `internal/workspace` session records gain version 2 and the new kind.
  `internal/lifetime` gains a `runnerReader` interface. `internal/runner`
  holds the files, the state reading and the shared finalizer;
  `internal/daemon` is the only package that starts `ws runner run`.
- `internal/claude` gains `Model(session)` from the transcript tail.
- `ws list --json`, `ws machine spawn`, `children` and `wait` gain
  `runner`, `model` and `ws_runner`;
  [reference.md](../design/ollama-agent/reference.md) lists them.
- New error code `runner_failed` (exit 1).
- How we'll know: `ws list --json` on a runner child shows `kind: "agent"`,
  `runner: "ws"`, its model, and the state table above, in a test with a
  fake runner process; a Claude child shows its transcript's model.
- Unverified: a runner across a real pod restart.

## References

[^judge]: workspaces `internal/lifetime/judge.go` and `internal/workspace/session.go` @98697f3 (read 2026-11-14). `judge`: "case rec.err != nil: r.m.warn("kept workspace %s: %v", id, rec.err); return nil, keep". The session reader fails on `meta.Undecoded()` keys.
[^contract]: [ADR 0016](0016-output-contract.md). "Enum values are lowercase strings. Consumers must accept values they don't know."
[^proc]: workspaces `internal/proc/proc.go` @98697f3. `func (Reader) StartTime(pid int) (time.Time, error)`.
[^transcript]: Measured 2026-11-14: `claude agents --json --all` entries have the keys `cwd, id, kind, name, pid, sessionId, startedAt, state, status` and no model; this session's transcript has `"model":"claude-opus-5-5"` on its assistant entries. `internal/claude/transcript.go` @98697f3 reads a 1 MiB tail ("transcriptTail").
[^wait]: [ADR 0050](0050-machine-wait-results-and-roles.md), "4. `ws machine wait`".
