# 0080. Reviews and loops run on the environment that owns them

- Status: Accepted (2026-11-23)
- Date: 2026-11-23

## Context

### Problem

[0073](0073-environment-agent-is-ws.md) left `review.*` and `loop.*` as
"later, one ADR each", and the first PR 4 design kept both local. The owner
decided on 2026-11-23 that reviews and loops work on remote environments in
PR 4. How does a review or a loop run on another machine without a new way to
run code, without a secret crossing the network, and without a loop starting
unseen on a machine nobody is watching?

### What a solution needs

1. No secret travels over MQTT, in an argument or in a result.
2. Only allow-listed, typed operations, and only the agent's own reviews and
   loops.
3. Code that runs on a remote (a loop script, a permission mode that skips
   prompts) is never started by accident.
4. An offline environment pauses its loops without making up missed runs.
5. The same code as a local `ws`.

### Relevant facts

- A remote agent's `ws review start` already runs on the remote
  ([0072](0072-remote-environments.md)).
- A gate script is `/bin/sh -c` as the agent's user, outside Claude Code's
  permissions. A prompt template feeds a Claude agent. Both are the same
  trust as `workspace.create` from the controller.
- `internal/daemon` has no loop tick ([0068](0068-ws-daemon.md)), so the
  remote's one long-lived `ws` process is the environment agent.

## Decision

We will run a review or a loop entirely on its environment, and keep the
forge side of reviews on the controller. These decisions are R1 to R6 of
[the design](../design/remote-environments/tasks-rl.md).

**R1. A review runs on the remote; the controller keeps the forge side.**

- `review.start`, `review.status`, `review.list`, `review.report`,
  `review.decide`, `review.acted` and `review.profiles` run the remote's
  `ws review …`, in a fresh in-process App. The clone cache, the review
  workspace, `review.toml` and `reviews/…/runs/` stay in the agent's
  `CONFIG_DIR`. A controller-started review has `--requester none`, so the
  remote's removal pass ([0045](0045-finished-reviews-are-removed.md))
  removes it when it finishes.
- The remote signs in to the forge itself (`gh auth login`, or `GH_TOKEN` in
  the agent's service environment; git credentials for private repositories;
  Claude Code signed in). **No token crosses the network.**
- The controller keeps the pull request list, posting comments, dismissals,
  and the Fix and Ask agents. Posting reads the findings from the remote,
  posts with the controller's `gh` or token, then records `review.acted`.
- There is no `review.wait`: `ws review wait --env` polls `review.status`.
- Profiles are read-only on a remote.
- `review.report` is paged by bytes (192 KiB) and capped at 1000 pages; an
  item over the budget alone is cut and marked `truncated`.
- A repo's environment is a field of the watched repo
  ([0060](0060-review-filters-and-forges.md)); auto-review starts there.

**R2. A loop's record, history, memory and ticker are on the remote.**

- The agent runs `ws loop tick --json --quiet --max-starts 2` every 60 s
  while it has a control connection. The first pass after a (re)connect waits
  a full period, and passes never overlap.
- Offline means paused in effect: no tick runs, nothing is marked `paused`,
  and nothing queues. On reconnect each overdue loop is due once
  ([0048](0048-agent-loop-scheduler.md) re-anchors it), and `--max-starts 2`
  spreads the starts over later passes. Loop list pages are capped at 1000
  pages too.
- The snapshot's `loops` (at most 100, no prompt, script or vars) lets the
  controller list loops offline (`ws loop list --env --cached`).
- Writing a loop with a script, or with `permission_mode`
  `bypassPermissions`, needs `confirm: true`; `loop.rm`, `loop.history_clear`
  and `loop.memory_clear` always do. The desktop shows the script read-only
  and asks first. `[environments] agent_loop_scripts = false` on the remote
  refuses scripts from the controller.
- **Trust statement.** A loop's script and prompt are the same trust as
  starting a Claude agent on that machine. Anyone who can use this controller
  can run code as the agent's user on every enrolled machine.

**R3. Security of the operations.**

- Every operation is allow-listed and typed, and checked on the controller,
  on the agent, and by the CLI. Unknown keys are refused. A review id or loop
  name must name something in the agent's own directories. Free text goes
  after `--` or on stdin, never as a flag.
- Rates, per agent process: `review.start` 12 per hour; `loop.run` 30 per
  hour and 1 per loop per 60 s; `loop.add`, `loop.edit` and `loop.rm` 60 per
  hour. Over a limit the reply is `operation_limited` (exit 1) with
  `retry_after_seconds`.
- **The agent redacts.** `internal/redact` replaces, in every string of every
  `review.*` and `loop.*` result and the snapshot's `loops`, the exact values
  of the agent's secrets and tokens, and fixed token patterns, with
  `[redacted]`, and adds `"redacted": N`. A write that contains `[redacted]`
  is refused, so a hidden value is never saved over the secret.
- **Known miss.** Redaction is best effort. It does not catch a secret in an
  unknown format, a base64-encoded or URL-encoded secret, and a cut made
  after redaction (a page or preview limit) can leave a short prefix of a
  token.
- `bypassPermissions` on a remote `review.start` is refused when there is no
  confirm flag, in the agent and in the desktop. **Owner decision still
  open:** whether it may ever be allowed.
- The decision suggestion index counts from 0 (the allow-list accepts
  `>= 0`; the earlier design had `>= 1`).

**R4. Offline and stale data are shown dimmed and read-only, with the
reason**, as in [0072](0072-remote-environments.md).

**R5. Agents never choose an environment.** `--env` on `ws review` is refused
inside a workspace. `ws loop` is for people. A remote agent's `ws machine
spawn` and `ws review start` already run on the remote.

**R6. Protocol 3.** Every `review.*` and `loop.*` operation has `Since: 3`
(`ProtocolReviewsLoops`). A protocol-2 agent gets `environment_outdated`.

### Why this option

It adds no new way to run code: each operation is a `ws` command the remote
could run itself. The agent's tick is the only new long-lived behaviour, and
it stops with the connection, so nothing runs unseen. Where it is weak:
redaction can miss a secret, a loop script is as powerful as a shell, and
reviews need the remote's own sign-in.

## Alternatives

### Run reviews on the controller against a remote checkout

**Pros**

- The controller's forge sign-in would do.

**Cons**

- The review needs the clone and the session on the machine that has the
  code, and a remote machine's files would have to travel.

### Keep ticking while disconnected

**Pros**

- No loop is ever late.

**Cons**

- [0072](0072-remote-environments.md): a start that ran later, unseen, on a
  machine the user forgot is worse than an error; a blocked run couldn't
  notify anyone.

### Forbid loop scripts on remotes

**Pros**

- Less code runs.

**Cons**

- A prompt already runs a Claude agent on the machine. A switch on the remote
  (`agent_loop_scripts`) gives its owner the choice.

## Consequences

- Reviews and loops work on environments in PR 4, from the CLI (`ws review
  --env`, `ws loop --env`), the desktop and Workspace Online.
- Known limits:
  - remote markdown and html reports have empty submission summaries;
  - the truncated memory preview doesn't load the full value on open
    (unimplemented, a follow-up);
  - the remote loop tick is the agent's 60 s tick with `--max-starts 2`; do
    not also run another ticker on that machine.
- Follow-ups:
  - Fix and Ask on the environment;
  - remote profile editing;
  - redacting `agent.logs`;
  - merging `internal/redact` with `internal/agent/tools`'s redactor;
  - `cache.*`;
  - a hello field for `gh` sign-in so the UI can warn before a start;
  - the daemon hosting local ticks ([0068](0068-ws-daemon.md)).
- The code follows this decision when the named tests in the design's RL
  tasks pass, including the broker conformance tests and the mutation checks
  of the security tasks.
- Unverified: anything on a real remote machine or WAN.

## References

- [Design and tasks](../design/remote-environments/tasks-rl.md)
- [`ws env`](../tools/ws-env.md#reviews-on-an-environment)
