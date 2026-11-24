# 0072. A workspace belongs to one environment, the local machine or an enrolled remote one; children inherit their parent's

- Status: Accepted (2026-11-22), with the amendments below
- Date: 2026-11-22

## Context

### Problem

`ws` and its desktop app and Workspace Online
([0053](0053-workspace-online.md)) manage workspaces on the machine they
run on. The user has a Linux server and a Windows rig and wants one
browser to start and watch agents on all of them, with each agent's git
checkouts, shell commands and file edits happening on the machine it was
started for. How do we add "where a workspace runs" without a second
implementation of workspaces, and what happens to the helpers an agent
starts?

### What a solution needs

1. **One place to see and start everything**: `ws list`, the desktop
   app and Workspace Online show every environment's workspaces.
2. **All work happens on the chosen machine**: clones, sessions, shells
   and tools, never half here and half there.
3. **Children follow parents automatically**: an agent that runs
   `ws machine spawn` on a remote gets a child on the same remote, with no
   flag ([0044](0044-workspace-trees.md)).
4. **Local behavior is unchanged**: with no environment set, every
   command, record and screen works as before.
5. **A remote that goes offline is visible, not silent**, and never
   turns into work on the wrong machine.

### Relevant facts

- A workspace record (`state/<id>/workspace.toml`) is version 1, 2
  (hidden, [0043](0043-hidden-workspaces.md)) or 3 (parent,
  [0044](0044-workspace-trees.md)); reading it rejects unknown keys and
  requires an absolute `path` (`internal/workspace/record.go`).
- A child's parent is taken from the workspace `ws` runs in, found from
  the working directory ([0044](0044-workspace-trees.md)).
- Agents are told to use only `ws machine` ([0046](0046-ws-machine.md)).
- Claude Code's background sessions are supervised by `claude daemon run`
  on the machine, through `claude --bg` ([0024](0024-claude-code-client.md)).
- Today `ws` has no concept of another machine; `[[hosts]]` in
  `config.toml` ([0061](0061-hosts-in-config-toml.md)) are git forges.

## Decision

We will add **environments**. An environment is a machine, named by the
user (`linux-server`, `win-rig`), that runs an **environment agent**
([0073](0073-environment-agent-is-ws.md)) enrolled with this `ws`
([0075](0075-environment-enrollment-and-certificates.md)). The machine
`ws` runs on is the implicit environment `local`, which needs no
enrollment and can't be removed.

**A workspace has exactly one environment**, fixed when it's created.
The environment's agent creates it, owns its record, its files and its
session; the controller (this `ws`) holds only a cached snapshot
([0073](0073-environment-agent-is-ws.md)).

**Choosing it.**

- A person picks it: `ws claude --env <name>`, `ws shell --env <name>`,
  and an Environment picker in the desktop app's New agent dialog.
  Without one, `local`.
- An agent never picks it. `ws machine spawn` and `ws review start` run
  on the environment's own `ws`, so what they create is on that
  environment. A review of a pull request runs where its requester runs.
- `--env` on a workspace that has a parent is refused when it differs
  from the parent's (`environment_mismatch`, exit 4): a tree shares one
  machine, so removing a parent can remove its descendants
  ([0044](0044-workspace-trees.md)) without crossing a network.

**Records.** `workspace.toml` gains `environment = "<name>"`, written only
when it isn't `local`, as version 4, so an older `ws` never treats it as
a local workspace and removes it. On the controller, a remote workspace
is a snapshot entry under `state/.environments/<name>/snapshot.json`, not
a `workspace.toml`, because its `path` is a path on another machine and
may be a Windows path.

**Seeing them.** `ws list --json` gives every workspace `"environment"`
(`"local"` or a name) and `ws list --env <name>` filters. An offline
environment's workspaces come from its last snapshot and carry
`"stale": true` and `"environment_status": "offline"`. They can be
listed, not acted on.

**Offline.** A command that needs an offline environment fails with
`environment_offline` (exit 1) and does nothing. There is no queue: a
start that ran later, unseen, on a machine the user forgot is worse than
an error. Nothing falls back to `local`.

**Errors.** `environment_unknown` (exit 3), `environment_offline` (1),
`environment_outdated` (1, [0073](0073-environment-agent-is-ws.md)),
`environment_mismatch` (4).

### Why this option

1. One list and one picker give criterion 1; the snapshot makes an offline
   machine visible.
2. Delegating whole operations to the remote's own `ws`, instead of
   forwarding individual tool calls, keeps every effect on one machine
   (criterion 2) and lets children inherit by construction (criterion 3):
   there's nothing to propagate.
3. With no `--env`, nothing changes locally (criterion 4).

Where it's weak: the controller's view of a remote can be seconds stale;
a snapshot of a long-offline machine can be wrong about what still exists;
and a tree can't span machines, so a Linux agent can't hand a task to a
Windows child. That's deliberate for now: cross-environment work is a
person's decision.

## Alternatives

### Forward each tool call to the remote (Claude here, tools there)

Run Claude Code on the controller with its Bash/Read/Edit tools replaced
by an MCP server that forwards to the agent.

**Pros:** nothing Claude-related on the remote.
**Cons:** reimplements Claude Code's tools, whose behavior differs from the
real ones; every call pays a network round trip; Claude's own files
(transcripts, settings, MCP servers) would live on the wrong machine for
the work. The user chose Claude on the remote.

### Mount the remote filesystem locally

SSHFS or SMB, run everything here.

**Pros:** no agent.
**Cons:** git and builds over a network filesystem are slow, and Windows
and Linux path and permission semantics leak into every command.

### Let a child pick another environment

**Pros:** orchestration across machines.
**Cons:** breaks the tree's removal and `ws machine wait` assumptions,
which read files on one machine ([0050](0050-machine-wait-results-and-roles.md)).
Can be revisited with its own ADR.

## Consequences

- Amends [0012](0012-workspace-layout.md) (`environment`, version 4),
  [0016](0016-output-contract.md) (four error codes),
  [0014](0014-command-set.md) (`--env` on `ws claude` and `ws shell`,
  `ws list --env`), and [0044](0044-workspace-trees.md) (a tree is on one
  environment).
- New `docs/tools/ws-env.md`; `docs/state.md`, `ws llm` and
  `docs/tools/ws-list.md` describe it.
- Conformance: Go tests that a record with `environment` is version 4 and
  an older reader refuses it; that `--env` conflicting with a parent is
  refused; that `ws list` marks an offline environment's entries stale;
  that no command with an offline environment touches `local`.
- **Unverified:** how stale a snapshot is in practice; measure the
  snapshot interval against the broker's keep-alive once built.

## Amendments

Accepted by the owner on 2026-11-22 for PR 3
([0076](0076-environment-platforms-and-phasing.md)). The decisions are
the design's, in
[tasks-ops.md](../design/remote-environments/tasks-ops.md#decisions-this-design-adds-to-the-adrs).
Where the built code differs from them, the deviation is listed below.

- **D2, records and parents.** `environment = "<name>"` is written only when
  it isn't `local`. A child takes its parent's environment; `--env` naming
  another one is `environment_mismatch`. A remote entry's `parent` is an id
  on the same environment, so trees are keyed by `(environment, id)`.
- **D3, snapshot and presence.** The snapshot is `{"at", "protocol",
  "workspaces", "agents"}`. An entry is `"stale": true` when its environment
  isn't `online` or its snapshot is more than 90 s old. Presence comes from
  the daemon's `GET /v1/status`. Scope in PR 3: see deviation 1.
- **D4, request id and timeouts.** Every request carries `request_id` (16
  lowercase hex characters from `crypto/rand`) and `deadline`. Timeouts:
  `workspace.create` 10 min, `workspace.rm` 2 min, `agent.send` 90 s, every
  other operation 30 s. The agent runs each operation under the deadline and
  deduplicates on the request id: it remembers 512 ids for 15 min after they
  finish, and a duplicate gets the stored reply, so an operation runs once.
  The controller republishes a pending call once when its environment
  reconnects, and waits until the deadline plus 15 s. A timeout fails
  `environment_offline` with details `{"environment", "request_id", "op",
  "reason": "timeout"}`; the workspace, if one was made, is an ordinary
  listed workspace that `ws rm` removes.
- **D5, protocol number.** The hello carries `"protocol"` (`envproto.Protocol`,
  1 in PR 3; an agent that sends none is 0). Each operation has `Since`, and
  an operation whose `Since` is above the agent's protocol is refused before
  it is sent with `environment_outdated` (exit 1), details `{"environment",
  "op", "ws_version", "protocol", "need_protocol"}`. PR 4 bumps `Protocol`
  to 2 for `terminal.*`.
- **D7, `ws shell --env`.** Deviation 2: it isn't in PR 3.
- **D9, results.** `ws claude --env <name> --json` prints the remote's
  `ws claude --json` object with `"environment": "<name>"` added. Remote
  error codes keep their exit code and gain `details.environment`; a code
  this `ws` doesn't know becomes `internal` with `details.remote_code`.
- **Claude prompts.** A prompt for a Claude agent on an environment that
  starts with `-` (after leading white space) is refused with `usage`,
  because `claude` would read it as a flag. Runner prompts aren't affected.

### PR 4 (terminals and desktop)

Accepted by the owner for PR 4. The decisions are the design's, in
[tasks-ui.md](../design/remote-environments/tasks-ui.md#decisions-this-design-adds-to-the-adrs).

- **Choosing it.** Besides `ws claude --env` and `ws shell --env`, PR 4 adds
  `ws attach [--env]`, `ws stop [--env]` and `--env` on `ws rm`, `ws rename`,
  `ws hide` and `ws unhide`, plus the Environment picker in the desktop app
  and Workspace Online. Without `--env` these stay local; a remote id given
  without it is `workspace_not_found` and names the environment.
- **Deviations 1 and 2 are resolved.** Plain `ws list` and the table and
  `--json` show every environment, and `ws shell --env` exists. One new
  deviation: **`ws list -q` (ids only) stays local** unless `--env` is
  given, so `ws rm $(ws list -q)` can't mix machines.
- **Seeing them.** `ws list --json` gives every workspace `"environment"`,
  and now `agent_state` (`working`, `blocked`, `done`, `stopped`, from the
  snapshot's agents; `null` for local entries and remote ones without an
  agent). The error code `terminal_limit` (exit 4) is new.
- **Reviews and loops** run on remote environments in this PR, which the
  first design left out; see ADR 0078 (written with the reviews and loops docs) where it
  exists.
- **Later:** the remote conversation view (the desktop reads local
  transcripts, so a remote agent's pane opens on its terminal with a note),
  stopping a `ws` runner agent on a remote, and Windows.

### Deviations from the design

1. **Plain `ws list` stays local until PR 4.** `ws list --env <name>` (or
   `--env local`) lists remote workspaces; plain `ws list` lists local ones
   only, as before. Reason: the desktop app runs `ws list --all` and acts on
   ids with local commands (`ws rm <id>`, `claude attach <id>`), so a remote
   id equal to a local one would make it act on the wrong workspace. PR 4
   makes every environment the default, with the desktop's environment
   badges. This defers part of "one place to see everything" by one PR.
2. **`ws shell --env` moves to PR 4.** A shell is an interactive terminal,
   and PR 4's `terminal.*` streams are what connect one to a remote machine.
   So PR 3 adds no `--env` to `ws shell`, and the wire has no `shell` kind:
   `workspace.create` refuses it with `usage`. The design's D7 refusal isn't
   built; this is its recommended alternative.
3. **The `environment_outdated` message** is the one `internal/environment`
   builds, `environment <name> runs ws <version>, which speaks protocol N;
   <op> needs protocol M`, not the wording D5 proposed. Changing it is a code
   change.

Deviation 1 and 2 are also in the stack table of
[0076](0076-environment-platforms-and-phasing.md).
