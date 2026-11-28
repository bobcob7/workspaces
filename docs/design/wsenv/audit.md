# wsenv: agent services on remote environments (audit)

What each service `ws` gives agents does on a remote environment, compared
with a local one, and the bugs found. Read from the code at `566af23`
(main, 2026-11-27). Environment limits: [constraints.md](constraints.md).
No real remote environment exists here, so every row is from reading the
code and its tests; rows marked **unverified** need a real remote.

Two scenarios:

- **A: the agent runs on a remote.** A person ran `ws claude --env X`. The
  session runs the remote's own `ws` with the remote's `CONFIG_DIR`
  (ADR 0073), so its `ws machine` commands read and write remote state, and
  the controller sees it through snapshots and the scope ledger.
- **B: a local parent has a child on a remote** (`ws machine spawn --env X`,
  ADR 0082/0083). The parent's `ws` asks the controller, which asks the
  agent; the child is its own root on the remote (`--parent none`,
  `internal/cli/env_agent_ops.go:440-444`), and the parent keeps a copy of
  its result under `state/<root>/machine/remote/<env>/<child>/`
  (`internal/machine/remote.go:14-24`).

How a remote `ws` knows it is one: `selfRef()` reads the enrolment in
`CONFIG_DIR/environment` (`internal/cli/scope.go:31-40`), and the artifact
refusal checks `CONFIG_DIR/environment/agent.toml`
(`internal/cli/machine_artifact.go:57-62`). `WS_ENVIRONMENT` is set only
for `ws shell` terminals (`internal/cli/env_agent_terminal.go:115`), never
for Claude sessions.

## Table

| Service | Sc. | Local | Remote | Verdict |
| ------- | --- | ----- | ------ | ------- |
| `progress set/step/show` | A | writes `state/<root>/machine/<id>/progress.toml` | same, in the remote's `CONFIG_DIR`; the controller sees it in snapshots (ADR 0083) | OK |
| `progress set/step` output | B | `rollup` counts local children | `rollup` leaves out remote children, while `show` counts them: `updateProgress` → `callerProgress` (`machine_progress.go:192,201-214`) never calls `nestRemoteProgress` | **Bug 6** |
| `result` (child side) | A | `state/<root>/machine/<id>/result.toml`, rewritten on each call (`machine/store.go:49-60`) | same on the remote | OK |
| `result` seen by the parent | B | the latest verdict | the **first** copy wins forever: `WriteRemote` returns early when `result.toml` exists (`machine/remote.go:39-40`), and `remoteRecord` returns the copy, not the live reply (`machine_remote.go:178`) | **Bug 2** |
| `result.report` path | B | `state/<root>/machine/<child>/report.md` | a local copy, `.../machine/remote/<env>/<child>/report.md`, readable by the caller (`machine_remote_test.go:246`) | OK |
| `wait` | B | finishes on a result, or `missing` after two quiet polls | with the environment unreachable, a result already copied is read but `finished` stays false (`machine_remote.go:460-463`), so `wait` runs to `wait_timeout` | **Bug 3** |
| `wait` items' `progress`/`rollup` | B | the child's tree | always `null`: `childProgress` reads the local store for the child id (`machine_wait.go:404`), and `AddRemote` only matches a remote child's *parent* (`machine/progress.go:184-195`) | **Bug 4** |
| `wait` / `children` of a ws-runner child | B | status from `ws`'s runner records | `agent.status` refuses any session whose runner is `ws` with `usage "... has no Claude Code agent"` (`env_agent_sessions.go:41-47`), so `askRemote` never sees it finish (`machine_remote.go:465-470`): a runner child that ends without a result waits until the timeout instead of `missing` | **Bug 5** |
| `logs` | B | Claude child: `claude logs` text; runner child: transcript events, `--tail 0` = all (`machine_logs.go:189-205`) | runner child: refused as above. Claude child: `--tail 0` becomes 200 lines (`tailLines`, `env_agent_sessions.go:102-108`) with `truncated: false`; over 2000 is `usage` (`envproto/scope.go:266`) | **Bug 5**, **Bug 7** |
| `rm` | B | the child's verdict stays readable under the parent's root | `removeRemote` forwards `rm` without copying the result back first (`machine_remote.go:376-387`); the remote deletes `state/<child>/` (`workspace/remove.go:40`) and the controller drops its ledger entry, so a later `wait` gives `workspace_not_found` | **Bug 8** |
| `spawn --role` runner | B | `runner.roles.<role>.runner` picks claude or ws (`machine.go:300-304`) | ignored: `runRemoteSpawn` drops `which`, and `remoteCreateArgs` reads only `opts.runner` (`machine.go:246-262`, `claude.go:438`) | **Bug 9** |
| `spawn` version gate | B | - | `environment_outdated` for a scoped create and an op's `Since` (`environment/scope.go:300-315`, `environment/call.go:135`) | OK |
| `envs` | A, B | scope file or controller | same (`machine_envs.go:38-59`) | OK |
| `memory` | A | a loop run and its descendants | a loop run on the remote works (its run mark is local to it) | OK |
| `memory` | B | any descendant of a loop run | a remote child of a loop run is its own root, so it gets `not_loop_run` (`machine_memory.go`, `callerLoop`) | limitation: **Follow-up F1** |
| `artifact add/get/put/list/show/rm` | A | the store in `CONFIG_DIR/artifacts` | refused with `artifact_unreachable`, exit 1 (`machine_artifact.go:32-34`, test `machine_artifact_test.go:186`) | **Bug 12 (was marked OK in error)**: refused with `artifact_unreachable` until ADR 0087; then reached over the controller's artifact API |
| `@artifact:` in a prompt | B | checked, and a hint appended (`claude.go:143-160`) | `ws claude --env`: the controller warns "sent as plain text", then the remote's own `ws claude` runs `artifactHints(args, "")` against its empty store and fails with `artifact_unknown`, so the workspace is never made. `ws machine spawn --env` skips the check and the warning. A spawn **on** a remote (sc. A, `machine.go:323`) also fails with `artifact_unknown`, not `artifact_unreachable`. ADR 0087 replaces the plain-text fallback with the API | **Bug 1** |
| `review start/wait` | A | in-process; `--env` refused inside a workspace (`review_env.go:23-38`) | same, with the remote's `gh` sign-in | OK |
| `review --env ... wait` | B (a person) | polls to the deadline | the first transport error ends the wait (`review_env.go:380-383`); `machine wait` tolerates the same errors (`warnRemote`) | **Bug 10** |
| `CONFIG_DIR` seen by the session | A, local | the session's `ws` must find its workspace under the same `CONFIG_DIR` | `ws` never passes `WS_CONFIG_DIR` (or `--config-dir`) to `claude --bg` (`app.go:159`; the only `WS_CONFIG_DIR` set anywhere is the runner's, `daemon/env.go:16-25`). With `ws --config-dir X claude` or `ws --config-dir X env agent run`, the session's `ws machine` uses the default `CONFIG_DIR` and fails with `outside_workspace` (or, on a remote, also skips the artifact refusal) | **Bug 11** |
| Claude config dir of sessions | A, local | every session shares `~/.claude` | same on the remote | goal 1: [ADR 0085](../../adrs/0085-per-workspace-claude-config.md) |
| `children` discovery | A | no controller call when the scope is self only | always asks the agent socket when self isn't local (`machine_remote.go:54`): one wasted call, and a warning when the socket is down | minor, not fixed |
| `done` state | B | two quiet polls finish a child with no result | `askRemote` counts Claude's `state == "done"` as finished (`machine_remote.go:470`); whether `done` means "turn ended" in every Claude Code version is **unverified** | unverified |

## Bugs

Each bug has a fix in [tasks.md](tasks.md) with a test that fails first.

1. **`@artifact:` references break or are lost on a remote.**
   `internal/cli/claude.go:149-152` warns and sends the prompt on; the remote's
   `ws claude` then runs `artifactHints(args, "")` → `checkArtifactsExist`
   against its own empty store (`claude.go:173-181`) and fails
   `artifact_unknown`. `runRemoteSpawn` (`scope.go:240-267`) never calls
   `artifactHints`, so `ws machine spawn --env` sends the reference with no
   warning. A local spawn by an agent on a remote (`machine.go:323`) fails
   `artifact_unknown` though the store is just unreachable. Fix: in
   `artifactHints`, when `a.enrolledAgent()`, warn and pass the prompt
   unchanged; `runRemoteSpawn` calls `a.artifactHints([]string{prompt},
   opts.target)` for the warning. **T5.**
2. **A remote child's later verdict is never seen.** `machine/remote.go:39-40`
   keeps the first `result.toml`; locally `WriteResult` overwrites
   (`machine/store.go:49-60`). A child that records `FAIL`, is sent a fix,
   and records `PASS` stays `fail` for its parent. Fix: `WriteRemote`
   replaces the copy when the new result's `recorded_at` is later. **T4.**
3. **`wait` hangs when the environment drops after the result was copied.**
   `machine_remote.go:460-463` returns `childState{record: rec}` without
   `finished`. Fix: `finished: rec.Result != nil, quiet: rec.Result != nil`.
   **T4.**
4. **`wait` shows `progress: null, rollup: null` for remote children.**
   `machine_wait.go:404`. Fix: for a remote target, use the progress of the
   record the poll already fetched and `remoteRollup`. **T4.**
5. **Remote ws-runner children can't be waited on or logged.**
   `env_agent_sessions.go:41` refuses `Runner == ws`. Fix: `agent.status`
   reports a runner session from its lifetime entry, `agent.logs` returns
   the runner transcript's events as lines. **T6.**
6. **`progress set`/`step` print a rollup without remote children**, unlike
   `show`. `machine_progress.go:192-214`. Fix: `callerProgress` nests remote
   progress as `show` does. **T4.**
7. **Remote `logs --tail` drops lines silently.** `tailLines`
   (`env_agent_sessions.go:102-108`) cuts to 200 for `--tail 0` and never
   sets `truncated`. Fix: set `Truncated` when lines were cut, so the caller
   sees `truncated: true`. The 2000-line cap stays (protocol limit). **T6.**
8. **`ws machine rm` of a remote child loses its verdict.**
   `machine_remote.go:376-387`. Fix: call `remoteRecord` (which copies a
   result back) before forwarding `rm`. **T4.**
9. **A role's runner is ignored on a remote spawn.** `machine.go:253-261`
   with `claude.go:438`. Fix: `runRemoteSpawn` sets `opts.runner = which`.
   **T5.**
10. **`ws review --env <env> wait` dies on one transport error.**
    `review_env.go:380-383`. Fix: on `environment_offline` or a timeout of a
    single call, warn and poll again until the deadline. **T6.**
11. **Sessions don't get `ws`'s `CONFIG_DIR`.** No `WS_CONFIG_DIR` in the
    environment of `claude --bg` (`internal/claude/start.go:58-82`,
    `app.go:159`). Fix: `startAgent` passes `WS_CONFIG_DIR=<a.paths.Root>` in
    `StartOptions.Env`. Through the shared Claude daemon the variable may not
    reach the session (whether a caller's environment passes through an
    existing daemon is **unverified**, constraints.md); through a
    per-workspace daemon (ADR 0085) it does, because that daemon is started
    by this `claude --bg`. **T3.**

12. **`ws machine artifact` is refused on every enrolled remote.**
    `machine_artifact.go:32-34` returns `artifact_unreachable` for every
    subcommand, so an agent on a remote can't read a screenshot a person
    shared or share a document back. The audit marked this OK by mistake.
    Fix: ADR 0087. The controller serves `/v1/artifacts` over mutual TLS on
    `api_listen` (8885), the agent learns the address by `api.update`, and
    `ws machine artifact` reaches the store through it. **T1-T4.**

## Follow-ups (not fixed here)

- **F1: loop memory from a remote child of a loop run.** It needs a new
  scope operation (`memory.*` through the controller) and a protocol bump
  to 5; ADR 0080 doesn't promise it. Documented as a limitation in
  `docs/tools/ws-machine.md` by T6.
- **F2: `children` asks the agent socket when the scope is self only**
  (`machine_remote.go:54`). Costs one local call; no wrong result.
- **Unverified with a real remote:** Claude Code's `done` state as "turn
  ended" on the remote's Claude Code version; that `WS_CONFIG_DIR` reaches a
  session through the shared daemon (bug 11).
