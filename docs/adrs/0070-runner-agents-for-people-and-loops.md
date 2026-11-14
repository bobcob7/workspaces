# 0070. `ws claude` and loops can start agents on `ws`'s runner: `--runner` and `--endpoint`, a loop record version 4, and runner and model pickers in the desktop

- Status: Proposed
- Date: 2026-11-14

## Context

### Problem

ADRs [0062](0062-agent-runner-built-into-ws.md)–[0069](0069-runner-mcp-tools-and-claude-code-parity.md)
let `ws machine spawn` start a child on `ws`'s own runner. The owner
decided that people (`ws claude`) and loops (`ws loop`) should be able to
as well: "Loops should be able to pick their model and runner." How do
`ws claude` and a loop choose the runner and the model, how is that
stored in a loop record without breaking older `ws` binaries, how does a
loop run start a runner session, and how does the desktop app offer it?

### What a solution needs

1. **The same choice everywhere**: the runner, model and endpoint are
   chosen the way `ws machine spawn` chooses them
   ([0064](0064-choosing-the-runner-model-and-endpoint.md)), with the same
   flags, the same `--model` after `--`, and the same preflight.
2. **The contract holds** ([0016](0016-output-contract.md)): existing JSON
   fields, error codes and exit codes don't change; additions only.
3. **Older `ws` binaries leave new records alone**, as loop records
   already do ([0027](0027-respawn-lock-and-record-versions.md),
   [0048](0048-agent-loop-scheduler.md)).
4. **Loop conventions hold**: `add` and `edit` take the same options, the
   desktop's one `LoopForm` edits everything
   ([0052](0052-loop-edit-and-structured-forms.md)), and a run is still
   "exactly what `ws claude` would do"
   ([0048](0048-agent-loop-scheduler.md), [0049](0049-loop-scripts-and-variables.md)).

### Relevant facts

- A loop record is `CONFIG_DIR/loops/<NAME>.toml` at `FormatVersion = 3`;
  version 3 added `hidden`. A record whose version is newer than the
  binary's is skipped with a warning and never rewritten; adding `hidden`
  to an older record makes it version 3, and a version 1 record with a
  literal `{{` in its prompt can't take it unless `--prompt` is given in
  the same edit[^loop-code].
- A loop already stores `model` and `permission_mode` and passes them to
  `claude` after `--`; `model` and `permission_mode` are `null` in the
  JSON when unset[^loop-doc].
- `ws loop tick` starts a run through the same function as `ws claude`
  (`startAgent`), and records `session` and an `error_code` for a start
  that failed[^tick].
- `ws claude` without a prompt attaches the person's terminal to the
  Claude session ([0025](0025-ws-claude-session-hand-off.md)); a runner
  session has nothing to attach to ([0063](0063-runner-children-are-supervised-sessions.md)).
- The desktop's `LoopForm` has an **Agent** section with model and
  permission-mode selects whose choices come from the New agent dialog's
  list of Claude models[^form].

## Decision

**`ws claude`** gains `--runner claude|ws` and `--endpoint <name>`:

```
ws claude [--runner claude|ws] [--endpoint <name>] [OPTIONS] [<SPEC>...] [-- "<prompt>" [--model <m>]]
```

- The runner is `--runner`, else `claude`. (`ws claude` has no role, so
  `runner.roles` doesn't apply; the "no role" budgets do.)
- With `--runner ws`: a prompt is required (`usage`: "a ws runner agent
  needs a prompt; it can't be attached"); after the prompt only `--model
  <m>` is accepted, as for `ws machine spawn`; the model is `--model`, else
  `runner.model`. Preflight, workspace creation, `run.toml` (with `parent
  = ""`), the session record and the start go through the same function
  `ws machine spawn` uses. `runner.fallback = "claude"` applies the same
  way.
- The output gains `runner`, `model` and `ws_runner`, as `ws machine
  spawn`'s does ([reference.md](../design/ollama-agent/reference.md#ws-claude-and-ws-loop)).
  For a Claude agent they are `"claude"`, the `--model` value or `null`,
  and `null`.
- The agent has no parent, so its verdict, report and usage are kept in
  its own `state/<id>/runner/` ([0063](0063-runner-children-are-supervised-sessions.md)).

**Loops** gain two fields, set with `add` and `edit`:

| Option | Record key | JSON | Meaning |
| ------ | ---------- | ---- | ------- |
| `--runner claude\|ws` | `runner` (omitted for `claude`) | `"runner"`: `"claude"` or `"ws"` | What runs each run; default `claude` |
| `--endpoint <name>` | `endpoint` (omitted when empty) | `"endpoint"`: a name or `null` | Only with `ws`: overrides the model's endpoint |

- `--model` keeps its key and JSON field. For a `ws` loop it's a model
  alias or a raw id, resolved at each run as for `ws claude --runner ws`;
  unset, the run uses `runner.model`.
- `--permission-mode` and `--endpoint` don't mix with the other runner:
  `permission_mode` on a `ws` loop, or `endpoint` on a `claude` one, is
  `loop_invalid`. `edit --runner claude` on a `ws` loop clears `endpoint`;
  `edit --runner ws` on a loop with a `permission_mode` needs
  `--permission-mode ""` in the same edit.
- `add` and `edit` check that a given alias or endpoint exists in
  `config.toml` (`loop_invalid` otherwise, naming it). They don't
  preflight: endpoints come and go, and a run reports a failed preflight
  like any start failure.
- **Version 4.** `FormatVersion` becomes 4. A record is written as
  version 4 when it has `runner = "ws"` or an `endpoint`; setting them on
  an older record makes it version 4, with the same version 1 literal
  `{{` rule as `hidden`. A loop whose runner is `claude` and that has no
  endpoint writes no new key and keeps its version, so binaries that read
  version 3 keep running it. An older `ws` skips a version 4 record with
  its usual warning.
- **A run.** `ws loop tick` and `ws loop run` start a `ws` loop's run with
  the shared runner start function: the daemon (auto-started as for any
  runner) preflights, then the workspace is created with the loop's
  `hidden`, and the runner starts with the rendered prompt. A preflight
  failure is a failed start with its code (`endpoint_unreachable`,
  `model_not_served`, `model_without_tools`, `daemon_unavailable`) in the
  run's `error_code`, its history entry and the Loops section, as any
  failed start is now. `runner.fallback` applies as for `ws claude`.
  `busy` and retention work unchanged, since a runner session reads
  `working`, `starting` or `idle` like a Claude one.
- The loop's runs are agents without a parent: the same `state/<id>/runner/`
  results as `ws claude --runner ws`.

**The desktop app.** One shared component, `RunnerModelFields`, is used by
`LoopForm`'s **Agent** section and by the New agent dialog:

- **Runner**: a select, "Claude Code" or "ws runner".
- **Model**: for Claude Code, today's Claude model select. For the ws
  runner, a select of the `[models]` aliases from `settings_get` (shown as
  `qwen9b · qwen/qwen3.5-9b`), with "Default (runner.model)" first, and an
  **Endpoint** select ("The model's endpoint" first, then each endpoint).
- **Permission mode** is shown only for Claude Code.

The Tauri commands pass `--runner`, `--endpoint` and `--model` as they
pass the other loop options: `edit` sends every field, so `--runner=claude`
and `--endpoint=` clear them. The loop detail's Agent card shows the
runner and the model.

### Why this option

1. **Same choice**: the same flags and the same start function as `ws
   machine spawn`; the same `--model` slot means "a Claude alias" for one
   runner and "a ws alias or id" for the other, as it already does for
   spawn.
2. **Contract**: `runner` and `endpoint` are new JSON fields, after the
   existing ones; `model` keeps its meaning per runner.
3. **Older binaries**: only loops that use the new fields become version
   4, so a `claude` loop is untouched and a `ws` loop is skipped, not
   misread as a Claude loop.
4. **Conventions**: options on `add` and `edit`, one form, a run that is
   "what `ws claude` would do", now with `--runner`.

Where it's weak: a `ws` loop's model or endpoint can be removed from
`config.toml` after `add`, and then each run fails until the loop is
edited. A loop run on the runner has no attach, so a person can only read
its transcript. The `--model` field means two things depending on
`runner`.

## Alternatives

### A separate `ws runner start` command for people, and leave loops on Claude

**Pros**

- `ws claude` keeps its name's meaning.

**Cons**

- Two commands that create a workspace and start an agent, and loops
  couldn't use the runner, which the owner asked for.

### Store the runner inside `model` (`ws:qwen9b`)

**Pros**

- No new record key or version.

**Cons**

- [0064](0064-choosing-the-runner-model-and-endpoint.md) rejected a
  `runner:model` syntax: model ids contain `:` (`qwen3.5:9b`), and an
  older `ws` would pass `ws:qwen9b` to `claude --model` instead of
  skipping the record.

### Always write version 4

**Pros**

- One rule.

**Cons**

- Every loop edited by a new `ws` would disappear from older binaries,
  including loops that don't use the runner.

## Consequences

- `internal/loop`: `Loop` gains `Runner` and `Endpoint`; `FormatVersion`
  4; `Edit` gains `Runner` and `Endpoint`; `Validate` gains the rules
  above. `internal/cli`: `ws claude` and `ws loop add|edit` gain the
  flags; tick's start path chooses by runner. Guides for `ws claude`,
  `ws loop add` and `ws loop edit` gain the flags; `docs/tools/ws-claude.md`
  and `ws-loop.md` gain them.
- The loop JSON gains `runner` and `endpoint`; `ws claude`'s output gains
  `runner`, `model` and `ws_runner`.
- The desktop gains `RunnerModelFields` and the fields in `NewLoop`.
- How we'll know: unit tests for the record rules and versions; e2e
  scenario `loop-runner` ([0067](0067-runner-tests-use-vendored-fakes.md)).

## References

[^loop-code]: workspaces `internal/loop/loop.go` (`FormatVersion = 3`, `Version2`: "the record version of loops added before ADR 0057: no hidden") and `internal/loop/edit.go` (`applyHidden`) @98697f3 (read 2026-11-14).
[^loop-doc]: [docs/tools/ws-loop.md](../tools/ws-loop.md), "Output": "`model` and `permission_mode` are `null` when not set" (read 2026-11-14).
[^tick]: workspaces `internal/cli/loop_tick.go` @98697f3, `startLoopRun`: "creates the run's workspace and starts its agent, as ws claude does" (read 2026-11-14).
[^form]: workspaces `desktop/src/features/loops/LoopForm.tsx` @98697f3: `import { models, permissionModes } from "../create/NewAgentDialog"` (read 2026-11-14).
