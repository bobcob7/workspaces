# Tasks: PR 4, "Terminals and desktop"

PR 4 of [ADR 0076](../../adrs/0076-environment-platforms-and-phasing.md)'s
stack, row 4, as one PR (the owner's decision): **(A)** the management UI in
the desktop app and Workspace Online (Settings › Environments, the
Environment picker, environment badges and filter on the Agents list, the
offline and stale state, plain `ws list` showing every environment) and
**(B)** the terminal streams of [ADR 0074](../../adrs/0074-mqtt-transport-embedded-broker.md)
(`terminal.open` and its frames, `ws shell --env`, `ws attach`, and the
desktop's integrated terminal on a remote workspace).

**Not in this PR:** log streams beyond what attach needs, the conversation
view of a remote agent (it reads local transcripts; see D3), `cache.*`,
`review.*`, `loop.*` on remote environments, stopping a `ws` runner agent on
a remote, Windows.

**Read first:** [constraints-ui.md](constraints-ui.md) (every pinned count,
list and golden value this PR moves, the toolchains, and what the pod can't
run). Every task names the rows of it that it touches. The PR 2 and PR 3
files ([tasks.md](tasks.md), [constraints.md](constraints.md),
[tasks-ops.md](tasks-ops.md), [constraints-ops.md](constraints-ops.md)) still
describe the code this PR builds on; their rules hold unless a decision
below changes one.

## Rules for every task

- **Go**: no blank lines inside function bodies (`internal/blanklines`
  fails `make test` and `make lint` on one); interfaces at the consumer, all
  of a package's interfaces in its one `interfaces.go` with one
  `//go:generate go tool moq -out moq_test.go . <names>` line; mocks only
  from moq (`go generate ./internal/<pkg>/`), never hand-written; unexported
  by default; constructor injection with `*slog.Logger` passed in; tool
  versions only in `go.mod`'s `tool` directive; tests use `t.Context()`,
  `t.TempDir()`, `require` for preconditions and `assert` for verifications,
  `slog.New(slog.NewJSONHandler(io.Discard, nil))`; one test file per source
  file, `Test<Receiver>_<Method>` with `t.Run` subtests; `t.Parallel()` only
  on tests that block on I/O. Comments end with a period; every exported
  name has a doc comment.
- **TypeScript**: named exports only; `strict` and
  `noUncheckedIndexedAccess`; CSS Modules (`*.module.css`), no inline style
  objects for layout; Redux Toolkit slices and `createAppThunk`
  (`src/thunk.ts`); every backend call through `api` in `src/api.ts` (the
  single `call()`), never `invoke` directly; Vitest and React Testing
  Library, colocated `Foo.test.tsx`, queries by role, label or text,
  `user-event` for interaction; slices tested by calling the reducer.
- **Rust** (`desktop/src-tauri`): the existing style. A command is a
  `#[tauri::command]` function in its module, registered in `lib.rs`'s
  `generate_handler!`, named in `online/dispatch.rs`'s `COMMANDS` in the same
  order and given a `dispatch` arm; `ws` is run only through `Runner`
  (`ws_json`, `ws_json_as_given`); argument builders are pure `pub fn
  …_args(...) -> Vec<String>` with unit tests in `#[cfg(test)] mod tests`;
  serde structs serialize `camelCase` to the frontend; errors are `AppError`
  with a `snake_case` code. `cargo clippy --all-targets -- -D warnings` for
  both feature sets.
- JSON from `ws` is `snake_case`; a field with no value is `null`, never
  missing; lists are `[]`. Time is RFC 3339 UTC.
- **No new dependency** anywhere: `go.mod`, `go.sum`, `package.json`,
  `package-lock.json`, `Cargo.toml` and `Cargo.lock` don't change. Raw
  terminal mode uses `golang.org/x/sys/unix` (already required).
- **Verify** with exactly the commands listed, one at a time; `go test` with
  `-p 2`; vitest with `--maxWorkers=2 --testTimeout=60000`; `cargo test
  -j 2`. `make lint` lints the whole repo and runs last. Commit and push
  after each file. Never wait on a permission prompt: list a check you
  couldn't run as unverified. **No `Co-Authored-By` trailer** on commits in
  this repo (the owner's rule overrides the implementer role's default).
- Write a failing test first for every behaviour a task adds, then the code.
- **Node for desktop tasks.** The pod has Node v20.19.2; vitest needs
  `^22.12.0 || ^24.0.0 || >=26.0.0`. Install Node into your own scratch
  directory, never into the repo, and check its SHA-256:

  ```bash
  N=$CLAUDE_JOB_DIR/tmp/node && mkdir -p "$N" && cd "$N"
  curl -fsSLO https://nodejs.org/dist/v22.23.5/node-v22.23.5-linux-x64.tar.xz
  echo "cdeb8670a0c81f51b4dc4ac61919f9b1ad917b304bf8221eb939570663d240f8  node-v22.23.5-linux-x64.tar.xz" | sha256sum -c -
  tar -xJf node-v22.23.5-linux-x64.tar.xz
  export PATH="$N/node-v22.23.5-linux-x64/bin:$PATH" && node --version
  ```

  (arm64: `node-v22.23.5-linux-arm64.tar.xz`, SHA-256
  `2babb9eb5264860d540849f4b064a6fbdebcdc323657d9d382ce23d313fd5e09`.) Then
  run the desktop commands from `desktop/` in the same shell.
- **Security-critical or concurrency-heavy tasks are marked `sonnet`**: they
  get a Sonnet implementer and a mutation-testing reviewer, who deletes or
  inverts each check the task lists and confirms a named test fails.

## Decisions this design adds to the ADRs

Each is marked **Owner review**: the owner should confirm or change it before
T2 starts (T1, the spike, may start at once). T18 writes the accepted ones
into the ADRs and the docs. D11-D18 were added after T1's no-go (amendment
of 2026-11-23); the owner should confirm them before T1a starts.

### D1. Measure first: T1 is a spike, and every terminal task waits for its go / no-go. **Owner review.**

ADR 0074 says terminal latency and throughput over the broker are
unmeasured. T1 measures them on the real embedded broker
(`internal/mqttbroker`, mochi v2.7.9, paho v0.23.0) with a real TLS agent
connection, on loopback and through an in-process throttling proxy, and
writes [terminal-spike.md](terminal-spike.md). It measures:

1. **Keystroke echo round trip**: the controller publishes one input frame
   on `stream/<sid>/in` (QoS 1), the agent's test client echoes it on
   `stream/<sid>/out` (QoS 0); p50, p95 and p99 of 1000 keystrokes at
   10 per second.
2. **Throughput** of 16 KiB QoS 0 chunks from agent to controller for 10 s:
   MiB/s received, frames lost (sequence gaps).
3. **Slow reader**: the controller's in-process subscriber sleeps 5 ms per
   frame. Measure frames dropped, whether the agent's TLS connection's other
   traffic (a `workspace.list` request and reply, and the 30 s keep-alive)
   is delayed (head-of-line blocking), and whether the in-process handler
   blocks the agent's read loop.
4. **Broker memory**: `runtime.MemStats.HeapInuse` before, during a
   saturated stream to a slow reader, and after; with 1, 4 and 8 streams.

Link profiles: loopback; 20 ms RTT at 10 Mbit/s; 80 ms RTT at 2 Mbit/s
(the proxy adds RTT/2 per direction and paces bytes with a token bucket).

**Go** when all hold on the 20 ms profile: echo p50 ≤ RTT + 15 ms and
p99 ≤ RTT + 60 ms; throughput ≥ 70 % of the link; a control request's
latency during a saturated stream ≤ 2 × idle + 100 ms; broker heap growth
≤ 8 MiB per slow stream; and the in-process handler, made non-blocking (a
bounded channel), never stalls the agent's connection.

**What changes on no-go** (ADR 0074's fallback; the topics don't change):

- *Echo or head-of-line fails, memory passes*: **a second MQTT connection
  per agent for streams.** The agent opens it with the same certificate and
  the client id `<env-id>.t`; the broker hook (`internal/mqttbroker/broker.go`,
  `OnConnectAuthenticate`) accepts exactly the suffix `.t`, sets the client id
  to `<cn>.t`, and the ACL lets that client publish only
  `stream/+/out` and subscribe only `stream/+/in` under its own id. T4 then
  publishes and subscribes streams on that connection; T6 and later are
  unchanged. Adds one task (broker hook + agent second connection, `sonnet`)
  before T4.
- *Throughput or memory fails*: **a direct WebSocket opened by a request.**
  `terminal.open` answers `{sid, url, token}`; the agent dials the daemon's
  `[environments] listen` port over mTLS with `GET /v1/terminals/<sid>`
  (upgrade), and the bytes skip the broker. T4's stream side and T6's
  subscription are replaced by a WebSocket; T7 onward are unchanged because
  they consume T6's `Terminal`. Adds two tasks and an ADR amendment; the
  orchestrator stops and asks the owner before writing them.
- *Only the slow-reader drop is worse than measured in the broker spike*:
  no design change; T6's gap handling and redraw already assume drops.

The spike's tests live behind the build tag `spike` (`go test -tags spike`),
so `make test` never runs them.

**Result (T1, branch `feat/env-ui-t1`, [terminal-spike.md](terminal-spike.md)):
NO-GO on head-of-line only.** Echo (p50 20.77 ms, p99 21.06 ms), throughput
(99.2 %) and broker memory (≤ 2.0 MiB per stream) pass; a control request
under a saturated stream takes about 2.0 s on the 20 ms profile against a
limit of about 142 ms. The first branch above applies: a second MQTT
connection per agent for streams. D11-D18 below make it concrete, and
replace "adds one task" with four (T1a-T1d). The spike's caveat stands: the
second connection's isolation was measured only with each TCP connection
paced on its own, not through a shared bottleneck; T1d measures that before
T4 and T6 start (D17).

### D2. How a terminal reaches the broker: the CLI is the only stream client, through an upgraded daemon socket. **Owner review.**

Today a desktop terminal is a local pty running `claude attach <session>` or
`ws shell` (`desktop/src-tauri/src/pty.rs`, `TermSpec`), and Workspace Online
streams that pty to the browser over its WebSocket. PR 4 keeps that: the
desktop's pty runs **`ws attach --env <name> -- <id>`** or **`ws shell --env
<name> …`**, and only `ws` speaks to the daemon. So the desktop, Workspace
Online and a person's terminal share one path, and no Rust code learns the
stream protocol.

```
xterm.js ─ Tauri channel / Online WebSocket ─ pty (desktop) ─ ws attach --env
   ─ unix socket, HTTP Upgrade ─ ws daemon (controller, in-process broker client)
   ─ broker ─ mTLS ─ agent ─ pty ─ claude attach <session> | ws shell
```

- **CLI ↔ daemon**: `POST /v1/environments/{name}/terminal` on the daemon's
  socket (ADR 0068; peer-credential checked like every route) with
  `Connection: Upgrade`, `Upgrade: ws-terminal.v1` and a JSON body
  (`envproto.TerminalOpenArgs`). The daemon sends `terminal.open` to the
  agent, and on success answers `101 Switching Protocols`; the connection
  then carries length-prefixed frames both ways (T7). Errors before the
  upgrade are the ordinary error envelope (`environment_offline`, …). Go's
  `http.Client` returns the 101 body as an `io.ReadWriteCloser`, and the
  server uses `http.Hijacker`; no WebSocket library is needed.
- **Daemon ↔ agent**: `stream/<sid>/out` (agent → controller, QoS 0, with a
  sequence number) and `stream/<sid>/in` (controller → agent, **QoS 1**, see
  D8), as binary frames (T2).
- **Flow control, end to end**:
  - *Agent → controller*: the agent reads its pty into ≤ 16 KiB chunks and
    queues them for publishing; the queue holds at most 1 MiB. Over that it
    drops the oldest queued chunks and publishes one `dropped` frame with the
    byte count, which every receiver turns into a visible marker
    `[ws: N bytes of output dropped]`. A full queue doesn't stop the pty
    reader, so a burst never blocks the program.
  - *Broker → controller*: the controller's in-process handler never blocks:
    it hands the frame to the terminal's bounded buffer (1 MiB) and returns.
    A frame the broker dropped shows as a sequence gap: the controller writes
    `[ws: output lost; redrawing]`, sends one `redraw` frame (at most one per
    second), and continues from the frame it got.
  - *Daemon → CLI*: a writer goroutine copies the buffer to the socket; a
    CLI that doesn't read blocks only that goroutine, the buffer fills, and
    over 1 MiB the controller drops and marks as the agent does.
  - *CLI → agent*: the CLI sends input in ≤ 4 KiB frames and paces itself
    at 64 KiB/s (a large paste takes seconds, not a disconnect); the daemon
    publishes each at QoS 1 and waits for the PUBACK before the next, so a
    slow agent link pushes back through the socket to the CLI's stdin. The
    agent closes a terminal whose input exceeds 128 KiB/s or 500 frames/s
    (abuse, not pacing).
  - Resize frames are coalesced: the CLI sends the last size at most every
    100 ms.
- **The daemon stays up while a terminal is open**: an upgraded connection
  isn't a request the idle timer sees, so the server counts open terminals
  and `idleExit` is false while any is open (T7). Like a call, the
  terminal route goes through `dialDaemon`, which may autostart a transient
  daemon; no agent is connected to that one yet, so the open fails
  `environment_offline`, exactly like a call (PR 3's D8).

### D3. Keying by `(environment, id)`, and what each command does for a remote workspace. **Owner review.**

The listing gives every entry `environment` (`"local"` or a name). The
desktop's `Entry.key` stays the workspace id (or `path:…`, `session:…`) for
local entries, so the tray, the selection and saved UI state don't change,
and becomes **`env:<name>:<id>`** for a remote one. `tree.ts`'s `parentOf`
matches `workspaceId === parentId` **and** `environment` equal: children are
on their parent's environment by construction (ADR 0072), so a tree never
crosses machines. Every command that acts on a workspace carries the entry's
`environment`, and the backend turns a non-local one into `--env <name>`.

Every command in `lib.rs`'s `generate_handler!` (81 today, 87 after T13),
which is also `online/dispatch.rs`'s `COMMANDS` and its `dispatch` arms:

| Command | Takes a workspace? | Remote workspace in PR 4 | How (task) |
| --- | --- | --- | --- |
| `list_entries` | lists them | works | `ws list --all` lists every environment (T9); `Entry` gains `environment`, `environmentOnline`, `snapshotStale` (T12) |
| `create_agent` | creates one | works | `NewAgent.environment` → `ws claude --env <name>` (`workspace.create`) (T12, T16) |
| `remove_entry` | id or session | works for a workspace; a bare session (`session:…`) is local only | `ws rm --env <name> [--keep-children] -- <id>` (`workspace.rm`) (T10, T12) |
| `rename_workspace` | id | works | `ws rename --env <name> -- <id> <name>` (`workspace.rename`) (T10, T12) |
| `set_workspace_hidden` | id | works | `ws hide`/`ws unhide --env <name> -- <id>` (`workspace.hide`) (T10, T12) |
| `stop_agent` | session | works for a Claude agent | `ws stop --env <name> -- <id>` (`agent.stop`) (T10, T12); local stays `claude stop <session>` |
| `runner_stop` | id | **later PR**: `agent.stop` stops only Claude sessions | button disabled: "Stopping a ws runner on linux-server isn't supported yet" (T17) |
| `restore_agents` | all | **local only** (the button restores this machine's agents) | label "Restore stopped (this machine)" when any environment is enrolled (T17) |
| `read_transcript` | session | **later PR** (`files.*` or a log stream) | the Conversation tab shows "The conversation of an agent on linux-server isn't shown here yet. Use Terminal." and Terminal opens by default (T17) |
| `terminal_open` (`attach`) | session | works | `TermSpec.attach` gains `environment`, `workspaceId` → pty runs `ws attach --env <name> -- <id>` (`terminal.open` kind `attach`) (T12) |
| `terminal_open` (`shell`) | creates one | works | `TermSpec.shell.environment` → `ws shell --env <name> …` (`terminal.open` kind `shell`) (T12, T16) |
| `terminal_open` (`dir`) | path | **local only** (a shell in a local folder) | not offered for a remote entry (T17) |
| `terminal_write`, `terminal_resize`, `terminal_close` | terminal id | works unchanged | the local pty runs `ws`; closing it ends `ws attach`, which closes the remote terminal |
| `workspace_open` | path | **local only** (Finder / editor) | disabled: "The files are on linux-server" (T17) |
| `open_external` | URL | unaffected | |
| `reviews_view`, `reviews_check_now`, `reviews_load_repos`, `reviews_set_watch`, `reviews_set_hosts`, `reviews_set_token`, `reviews_dismiss`, `reviews_restore`, `reviews_plan`, `reviews_profiles`, `reviews_profile_show`, `reviews_profile_add`, `reviews_profile_edit`, `reviews_profile_remove`, `reviews_start`, `reviews_ask`, `review_report`, `review_decide`, `review_open_report`, `review_comment_plan`, `review_comment_post`, `review_fix_plan`, `review_fix_start`, `review_fix_sent` | review workspaces | **local only** (`review.*` is a later PR) | the checker joins only `environment == "local"` entries (`reviews/runs.rs`), so a review a remote agent started never shows as Reviewing here (T12) |
| `loops_list`, `loops_add`, `loops_edit`, `loops_set_paused`, `loops_remove`, `loops_run_now`, `loops_history`, `loops_history_run`, `loops_history_clear`, `loops_memory`, `loops_memory_set`, `loops_memory_remove`, `loops_memory_clear` | loop runs | **local only** (`loop.*` later) | unchanged; loop runs are local entries |
| `stats_get` | none | **local only** | the bar already says "this machine"; remote usage isn't counted (later PR) |
| `cache_repos`, `cache_list`, `cache_refs`, `cache_resolve`, `cache_fetch`, `cache_add`, `cache_remove`, `cache_search` | none | unaffected | specs suggested from the local cache are resolved on the remote by its own `ws` |
| `versions`, `settings_get`, `settings_set_reviews`, `settings_set_notifications`, `open_defaults`, `settings_set_open`, `config_list`, `config_set`, `config_unset`, `runner_settings_get`, `daemon_status`, `daemon_restart`, `runner_check`, `endpoint_token_set`, `settings_environment`, `roles_list`, `roles_show`, `roles_add`, `roles_edit`, `roles_remove` | none | unaffected (controller settings) | `config_*` edits `[environments]` (T15) |
| *new* `environments_list`, `environment_add`, `environment_rotate`, `environment_remove`, `environments_ca_status`, `environments_ca_rotate` | none | | T13 |

**Not commands, same rule:**

- **Tray and notifications** (`tray/attention.rs`, `tray/watcher.rs`,
  `online/push.rs`): a remote top-level agent counts and notifies like a local
  one when its environment is online and its snapshot fresh. Its status comes
  from the snapshot: `session.state` plus the new `agent_state` (Claude Code's
  `blocked` → Needs input, `done` → Done), because the controller can't read
  the remote's job files. A stale or offline entry never starts a
  notification and never counts as needing input (its last state is shown,
  dimmed). Expect up to ~10 s more delay than local (the agent's snapshot
  check period). Keys are `env:<name>:<id>`, so a remote and a local id never
  collide in the watcher's map.
- **Tree view**: `parentOf` adds the environment check (T14); a child row of
  a remote parent shows "Runs on linux-server, like its parent" in its pane.
- **Stats bar**: unchanged and local.

### D4. Environment list and presence: poll `ws env list --json` every 10 s; the listing carries per-entry presence. **Owner review.**

- `environmentsSlice` (T14) polls `environments_list` (`ws env list --json`,
  which reads the daemon's status and never starts one) every **10 s**, and
  at once after any environment action, Settings › Environments opening, or
  the New agent dialog opening. The Agents list's 4 s `list_entries` poll
  already gives each entry `environment_status` and `stale`, so the two never
  need to agree to the second.
- Not events: the daemon has no push channel to the desktop today, and a
  10 s poll of a cheap local call meets "the row turns green on its own".
  An event stream is a later improvement, recorded in T18.
- **Staleness**: `loadedAt` in the slice; an answer older than 30 s (the
  poll failing) shows "Environment status as of 14:02" above the list. A
  `status` of `unknown` (no daemon) is shown as offline with the reason
  "the ws daemon isn't running".
- **Offline behaviour of every screen**:

  | Screen | Environment offline (or `unknown`) |
  | --- | --- |
  | Settings › Environments | row `○ offline since 14:02 (last seen)`; Rotate and Remove still work (they're local) |
  | New agent / New shell | the environment is listed, disabled, "(offline since 14:02)"; a remembered choice that is offline falls back to `local` with a note |
  | Agents list | rows dimmed with an `offline` badge, last known status kept; filter chip has a hollow dot |
  | Entry pane actions | Attach/Terminal, Stop, Remove, Rename, Hide disabled with "linux-server is offline (last seen 14:02)" |
  | Open terminal | an open terminal shows the `environment_offline` error line and the exit; reopening is disabled until online |
  | Tray / notifications | no counts, no events from that environment |
  | A stale but online entry (snapshot > 90 s) | like offline for the badge ("stale"), actions enabled (the agent is reachable) |

### D5. The one-time code in Workspace Online and the desktop. **Owner review.**

The code is a bearer secret until used (ADR 0075). What protects it:

- **Shown once**: `environment_add` and `environment_rotate` return it; the
  dialog keeps it in **component state only**, never in Redux (so not in
  devtools, the store, or any persisted state) and never in `localStorage`.
  Closing the dialog drops it; nothing can show it again (rotate makes a new
  one and cancels the old).
- **Not stored, not logged**: the Rust command returns `ws`'s JSON; it isn't
  cached in `AppState`, and `Runner` errors carry `ws`'s stderr, which never
  holds the code (`ws env add` never logs it, ADR 0075). Workspace Online's
  server prints no command results (`online/server.rs` logs only listing
  failures). Test: T13 `environment_add_result_is_not_kept` asserts nothing
  in `AppState` holds it.
- **Not in a URL**: Workspace Online calls commands with `POST
  /api/invoke/<command>` or the WebSocket, bodies only; `/api/*` answers
  `Cache-Control: no-store` (already, `online/server.rs` `cache_policy`).
  The name goes in the body, the code only in the response body.
- **Over TLS only on the web**: Workspace Online is served behind its TLS
  ingress (ADR 0053). The browser's clipboard API needs a secure context, so
  the Copy buttons are hidden, with "select the text to copy it", on plain
  HTTP.
- **Clipboard**: the dialog says "The code stays in your clipboard until you
  copy something else. Anyone who reads it first can enroll a machine;
  Rotate cancels an unused code." A new enrollment shows in the list at once
  for the user to confirm or remove (ADR 0075).

### D6. Terminal security: the checks and the tests that prove each. **Owner review.**

`terminal.open` is the most powerful operation: arbitrary input to a shell
or to Claude on the remote. Every check below has a named test; the
mutation-testing reviewer of T1b, T4, T6 and T7 removes each check and
confirms its test fails.

| Check | Where | Test (`t.Run` name) |
| --- | --- | --- |
| Only the controller opens a terminal: `terminal.open` and `terminal.close` are `CallerTerminal`; the daemon's `/call` route refuses them, only `/terminal` sends them | `envproto.LookupOp`, `daemon.handleCallEnvironment` | `internal/daemon/server_test.go` `TestServer_handleCallEnvironment` "terminal.open isn't forwarded by call"; `internal/envagent/terminals_test.go` `TestAgent_terminalOpen` "an open on up is refused" |
| The agent refuses `terminal.open` from a protocol mismatch: `Since: 2` | `environment.admitCall` (existing) | `internal/environment/terminal_test.go` `TestController_OpenTerminal` "an agent on protocol 1 is outdated" |
| Arguments are typed, unknown keys refused, sizes 1..1000, specs and names validated as `workspace.create`'s | `envproto.Validate` | `internal/envproto/terminal_test.go` `TestValidate_terminalOpen` (table) |
| **Attach takes a workspace, never a session id**; the workspace must be an id or a path under the agent's `workspaces/` (`checkWorkspaceRef`), have a Claude session (not a shell, not a `ws` runner), and the session id comes from its record | `envagent` `openTerminal`, `cli` `terminalCommands.Command` | `TestAgent_terminalOpen` "a workspace outside workspaces/ is refused", "a workspace with no Claude session is refused"; `internal/cli/env_agent_terminal_test.go` `TestTerminalCommands_Command` "the session id is the record's" |
| The command line is built by the agent from typed fields; nothing from the controller becomes a flag (`--` before specs and names; specs may not start with `-`) | `cli` `terminalCommands.Command` | `TestTerminalCommands_Command` "a shell's specs follow --", "a name that starts with - is refused" |
| Bounds: an input frame ≤ 4 KiB, a data frame ≤ 16 KiB, a frame on the wire ≤ 16 KiB + 10; input over 128 KiB/s or 500 frames/s closes the terminal; at most 8 open terminals per agent (`terminal_limit`) | `envagent` `terminal` | `internal/envagent/terminals_test.go` `TestTerminal_input` "an oversized frame closes the terminal", "input over the rate closes the terminal"; `TestAgent_terminalOpen` "a ninth terminal is refused with terminal_limit" |
| An input sequence gap closes the terminal (lost keystrokes must never run a different command) | `envagent` `terminal` | `TestTerminal_input` "a gap in input closes the terminal" |
| **No stream for an unknown sid**: the agent never writes frames on `stream/<sid>/in` for a sid it didn't open (it answers one `FrameExit` `disconnected` so a stale controller terminal ends, D16); the controller drops `out` frames for a sid it didn't open on that environment id | `envagent` `onStreamIn`, `environment` `onStreamOut` | `TestAgent_terminalOpen` "input for an unknown sid is dropped and answered with an exit"; `TestController_OpenTerminal` "output for an unknown sid is dropped", "output for a sid of another environment is dropped" |
| A terminal closes when: its CLI connection closes, the controller closes it, its environment goes offline, the agent's control **or stream** connection drops, no input or ping for 60 s, or its process exits | `envagent`, `environment`, `daemon` | `TestAgent_terminalOpen` "a dropped connection closes every terminal", "a dropped stream connection closes every terminal and keeps the agent online"; `TestTerminal_input` "no frame for 60 s closes the terminal"; `TestController_OpenTerminal` "going offline ends the terminal", "a missing stream connection ends the terminal", "an exit frame ends the terminal whatever its sequence"; `TestServer_handleTerminal` "closing the socket closes the terminal" |
| **Stream topics only on the stream connection** (D12): the control connection (`<id>`) may not publish `stream/+/out` or subscribe `stream/+/in`; the stream connection (`<id>.t`) may do only that | `environment.allowed` | `internal/environment/policy_test.go` `TestAllowed` (the D12 rows); `internal/mqttbroker/conformance_test.go` `TestConformance_streamConnection` "a stream-topic publish on the control connection is refused and disconnects", "a control-topic publish on the stream connection is refused and disconnects" |
| **A stream connection is the agent's own** (D11): the claimed id `<cn>.t` is honoured only for the certificate's CN; any other claim ending in `.t` is refused; it never takes over the control connection; it needs a live control connection; it sets no will; a retained stream publish disconnects | `mqttbroker` hook, `environment.policy.connect` | `TestConformance_streamConnection` "one environment cannot open a stream connection for another", "the stream connection does not kick the control connection"; `internal/mqttbroker/broker_test.go` `TestBroker_streamConnection` (all subtests) |
| **A revoked serial kicks both connections** | `mqttbroker.Broker.Disconnect`, `environment.Controller.disconnectRefused` (existing) | `TestConformance_streamConnection` "a revoked serial kicks both connections" |
| The daemon socket's peer check applies to the upgraded route | `daemon.peerListener` (existing) | `TestServer_handleTerminal` "is served through the peer listener" |
| Sids are 16 lowercase hex characters from `crypto/rand` | `envagent` | `TestAgent_terminalOpen` "the sid is 16 hex characters" |

### D7. Screenshots: the repo's method, after the last UI task. **Owner review.**

Playwright against the Vite dev server with `desktop/test/tauri-mock.js`
(and `desktop/test/web-mock.js` for Workspace Online), as in
`desktop/README.md` › Screenshots without the backend and the handbook page
"screenshots of the desktop app". The mock is already committed; T13 and
T14 add the new commands' sample data to it (two environments:
`linux-server` online, `win-rig` offline since 14:02; one remote entry tree on
`linux-server`; one entry on `win-rig`). The orchestrator's Sonnet
screenshot child runs after T17 (see "Screenshots" at the end) and writes
exactly these files under `desktop/docs/`, which T18 links:

| File | Screen |
| --- | --- |
| `settings-environments.png` | Settings › Environments, desktop width, both environments and the CA card |
| `settings-environments-add.png` | Add environment dialog with the code and two commands |
| `settings-environments-mobile.png` | the same list at 360 px (Workspace Online) |
| `new-agent-environment.png` | New agent with the Environment picker open, `win-rig` disabled |
| `agents-environments.png` | Agents list with badges, the filter chips and a dimmed offline row |
| `agents-environments-mobile.png` | the same at 360 px |
| `remote-terminal.png` | a `linux-server` agent's pane with the Terminal open and the remote-conversation note |

### D8. What this design changes in ADRs 0072-0076 (T18 writes the amendments). **Owner review.**

| ADR | Sentence | Proposed amendment |
| --- | --- | --- |
| 0074, Topics table | "`env/<id>/stream/<sid>/in` … terminal input and resize, QoS 0" | Input frames are **QoS 1**: a lost keystroke can change a command, and the agent closes the terminal on an input gap rather than guess. Output stays QoS 0. |
| 0074, Requests | "A JSON object `{op, args}`" (for everything on `req`) | `terminal.input`, `terminal.resize`, `terminal.redraw` and the ping are **binary frames on `stream/<sid>/in`**, not requests; only `terminal.open` and `terminal.close` are requests. Frame format: T2. |
| 0074, Streams | "on a gap it asks for a redraw (`terminal.redraw`), which sends the screen's current state" | The agent keeps no screen model. A redraw makes the program repaint by changing the pty size by one row and back (two `SIGWINCH`), which repaints Claude Code and other full-screen programs; a line-mode shell shows only the marker. |
| 0074, Streams | "Output the controller isn't reading is dropped by the agent after 1 MiB queued, with a visible marker" | Also by the controller after 1 MiB unread by its client (the CLI), with the same marker. |
| 0074, Where it's weak | "If measured terminal latency is poor, terminals move to a second connection or a direct WebSocket" | Replace with the spike's result and the chosen path ([terminal-spike.md](../design/remote-environments/terminal-spike.md)): the second connection, with T1d's number. The other sentences the second connection changes are in D18. |
| 0073, operations table | "`workspace.create` (`claude`, `shell` or `runner` kind …) / `ws shell`" | A shell is `terminal.open` kind `shell`, not a `workspace.create` kind: the workspace lives as long as the terminal. The agent runs **its own executable** (`os.Executable()`) as `ws shell` in a pty, the one operation that isn't in-process (PR 3's D1), because a shell needs its own process and controlling terminal. |
| 0073, operations table | "`terminal.open`, `terminal.input`, `terminal.resize` … `claude attach` or the shell" | Add `terminal.close`, `terminal.redraw`; attach takes a **workspace**, and the agent reads its session id. |
| 0072, Decision › Choosing it | "`ws claude --env <name>`, `ws shell --env <name>`, and an Environment picker" | Add `ws attach [--env]`, `ws stop [--env]` and `--env` on `ws rm`, `ws rename`, `ws hide`, `ws unhide` (D9). |
| 0072, Amendments › Deviations 1 and 2 | "Plain `ws list` stays local until PR 4"; "`ws shell --env` moves to PR 4" | Resolved in PR 4; one new deviation: `ws list -q` (ids only) stays local by default (D9). |
| 0072, Seeing them | "`ws list --json` gives every workspace `"environment"`" | Add `agent_state` (D9). |
| 0076, stack table row 4 | "`terminal.*` streams, `ws shell --env` …, Settings › Environments, picker, badges, offline state, `ws list` showing every environment by default" | Add `ws attach`, `ws stop`, `--env` on `rm`/`rename`/`hide`/`unhide`, `agent_state`, the error code `terminal_limit`; mark the remote conversation view and remote runner stop as later. |
| 0076, Status | "Proposed" | "Accepted (<date>)" once PR 4 merges, phases 1 done. |
| 0016, Status | lists the ADRs that added codes | add `terminal_limit` (PR 4). |
| 0014 | the command set | add `ws attach` and `ws stop` (people's commands). |

### D9. The CLI surface PR 4 adds. **Owner review.**

- **`ws attach [--env <name>] <workspace>`**: attach this terminal to a
  workspace's Claude agent. Local: `claude attach <session>` as `ws claude`
  does today (exec). Remote: `terminal.open` kind `attach` through the
  daemon. Needs a terminal on stdin (`usage` otherwise). Exits 0 when the
  terminal closes; detaching (Claude Code's own key) leaves the agent running.
- **`ws shell --env <name> [--name N] [--offline] [<SPEC>...]`**: a shell
  workspace on the environment for as long as the terminal is open; exits
  with the shell's status.
- **`ws stop [--env <name>] <workspace>...`**: stop a workspace's Claude
  agent (`claude stop`, or `agent.stop`). The desktop's Stop on a remote uses
  it; locally it keeps calling `claude stop` (no change).
- **`--env <name>` on `ws rm`, `ws rename`, `ws hide`, `ws unhide`**: one
  `workspace.rm|rename|hide` call per id; JSON as the local command's, plus
  `"environment"` (PR 3's D9). Without `--env` they stay local; a remote id
  given without it is `workspace_not_found` with "it is on linux-server; pass
  --env linux-server" when exactly one snapshot has it.
- **`ws list`** with no `--env` lists `local` and then every enrolled
  environment by name; `--env` narrows. Two exceptions, both to protect
  scripts: **`-q`/`--ids` stays local** unless `--env` is given (`ws rm
  $(ws list -q)` must not mix machines); and `--parent <id>` without `--env`
  lists the environment that has that id (local first). Each entry gains
  **`agent_state`**: Claude Code's state (`working`, `blocked`, `done`,
  `stopped`) from the snapshot's `agents` for a remote entry with a session,
  `null` for local entries (the desktop reads local job files itself) and
  for remote entries without a matching agent. The daemon is asked for
  presence only when at least one environment is enrolled.
- **New error code `terminal_limit`** (exit 4): the agent already has 8
  terminals open.
- **`WS_ENVIRONMENT`**: the agent sets it in the pty of a remote shell so the
  `ws shell` it runs stamps the workspace with the environment's name (PR
  3's D2); `ws shell` reads it only to set the record's `environment`.
  Documented in `ws-shell.md` as set by the environment agent.
- `Protocol` becomes **2**; `terminal.open` and `terminal.close` have
  `Since: 2`. An agent from PR 3 answers every PR 3 operation as before and
  refuses terminals with `environment_outdated`.

### D10. Redraw, ping and lifetime. **Owner review.**

- Terminals don't survive a reconnect: the agent closes every terminal when
  its control connection or its stream connection drops (D13), and the
  controller ends every terminal of an environment that goes offline or
  whose stream connection is gone (D16). `ws attach` prints
  `ws: linux-server went offline; the agent keeps running` and exits 1
  (`environment_offline`); after T1 the message is "lost the terminal
  connection to linux-server; the agent keeps running" (T8), because the
  stream connection can drop alone. Reattaching is cheap and avoids replaying input
  after an unknown gap.
- The controller sends a `ping` frame every 15 s per terminal; the agent
  closes a terminal that has had no frame for 60 s (a lost controller that
  didn't disconnect).
- A redraw is requested by the controller on an output gap, and by the
  agent itself after its queue drops output (once the queue is below
  256 KiB). At most one redraw per second per terminal.

### D11. The stream connection's identity: same certificate, client id `<cn>.t` chosen by a fixed suffix. **Owner review.**

Today the hook (`internal/mqttbroker/broker.go`) ignores the claimed client
id and sets it to the certificate's CN, and takes over any live connection
with that CN. A second connection from the same certificate would therefore
kick the first. The rule becomes:

- **One certificate, two client ids.** The agent dials the stream
  connection with the same certificate and claims the client id
  `envproto.StreamClientID(id)` = `<id>.t` (`envproto.StreamSuffix = ".t"`).
  No second certificate: nothing new to issue, renew, rotate or revoke.
- **The adapter derives the id from the claim, the CN decides who it is**
  (`peerOfConn(cl, claimed)`, `claimed` = `pk.Connect.ClientIdentifier`):
  claim exactly `<cn>.t` → `peer.ClientID = <cn>.t`; any other claim that
  ends in `.t` → refused (`errForeignStreamID`, logged); every other claim →
  `peer.ClientID = <cn>` as today (PR 3 agents and the existing test "the
  client id is the certificate CN, whatever the client claims" are
  unchanged). A fixed suffix is chosen over a CONNECT user property because
  the claimed id is already in every CONNECT, mochi keys sessions by it, and
  a property would still need mapping to a distinct id.
- **Connect policy** (`policy.connect`): `peer.ClientID` must be `peer.CN`
  or `envproto.StreamClientID(peer.CN)` (else `errClientIDMismatch`); every
  other check (valid id, enrolled, not revoked, trusted issuer) is the same
  for both.
- **Takeover is per client id** (`takeOver(peer.ClientID)`, matching
  `s.peer.ClientID`): a new `<cn>` replaces the old `<cn>`, a new `<cn>.t`
  replaces the old `<cn>.t`, and neither touches the other.
- **The stream connection is tied to the control one** (broker adapter):
  a `<cn>.t` CONNECT is refused when no `<cn>` session is live
  (`errNoControlConnection`); when a `<cn>` session ends (any reason,
  takeover included) the adapter disconnects the live `<cn>.t`
  (`ErrAdministrativeAction`, from a goroutine, never inside `b.mu`). So at
  most two connections per environment exist, and never a stream one alone.
- **No will, no retain on streams**: a stream connection whose CONNECT has a
  will is refused in `OnConnect`; a new `OnPublish` hook disconnects any
  non-inline client that publishes a retained message on a `stream/…` topic
  (`ErrNotAuthorized`, the packet rejected).
- **Kick by serial is unchanged code**: `Broker.Disconnect(serial)` already
  drops every live client with that serial, so revocation (`ws env rm`,
  `ws env rotate`), `Reload`'s `disconnectRefused` (which iterates
  `Clients()`, now including `<cn>.t` peers) and `ws env ca rotate --force`
  drop both connections. A named test proves it.
- `peerOf`'s fallback for an untracked client becomes `{ClientID: cl.ID, CN:
  strings.TrimSuffix(cl.ID, StreamSuffix)}`; `Clients()` lists both peers.

### D12. The ACL splits by connection. **Owner review.**

`allowed(peer, access, topic)`: the topic's id must equal `peer.CN` (as
today), and then:

| Connection | Publish | Subscribe |
| --- | --- | --- |
| control, `ClientID == CN` | `status`, `snapshot`, `up`, `res/<x>` | `req` |
| stream, `ClientID == CN + ".t"` | `stream/<x>/out` | `stream/<x>/in`, `stream/+/in` |
| any other `ClientID` | nothing | nothing |

The control connection loses `stream/<x>/out` and `stream/<x>/in` (no
released agent uses them: PR 3 agents are protocol 1 and never open a
terminal). A violation disconnects the client as every ACL violation does
(ADR 0074, amended). Reason: a stream on the control connection is what
blocked control traffic for 2 s, and control topics on the stream connection
would let it publish presence or replies outside D13's lifecycle.

### D13. Presence and lifecycle: the control connection is the environment. **Owner review.**

- `online` / `offline` is defined by the control connection only: its
  retained hello and its Last Will on `status`, as today. The stream
  connection has **no Last Will and publishes nothing retained**.
- **Stream connection drops** (control still up): the agent closes every
  terminal of that agent with reason `disconnected` (it can't publish their
  exit frames; the controller notices by D16). The environment stays online;
  other operations continue. No reconnect until the next `terminal.open`.
- **Control connection drops or the agent stops**: the agent closes the
  stream connection and every terminal before reconnecting or going offline
  (decided: **close, not keep**). Reasons: the controller already ends
  terminals of an offline environment (D10); a terminal outliving its
  environment's presence would be invisible to the UI; and the broker
  enforces the same rule (D11), so an agent bug can't leave one dangling.

### D14. Opened lazily, closed when idle, one per agent, a small send buffer. **Owner review.**

- **Lazy**: the agent dials the stream connection on the first
  `terminal.open` (before replying, after subscribing `stream/+/in`, so the
  first input frame is never lost) and closes it **60 s** after the last
  terminal closed (`Options.StreamIdle`, default 60 s). Cost: the first
  terminal after idle pays one TLS 1.3 handshake plus CONNECT/CONNACK and
  SUBSCRIBE/SUBACK, about 3 RTT (60 ms on the 20 ms profile). The other
  choice, always open, doubles every agent's connections, keep-alives and
  broker sessions while almost no agent has a terminal open, so lazy is
  recommended.
- **Backoff**: a failed dial is not retried before
  `backoff(attempt, BackoffMin, BackoffMax, jitter)` (the agent's existing
  1 s to 60 s); a `terminal.open` inside that wait, or whose dial fails,
  answers `environment_offline` "the agent couldn't open its terminal
  connection: <reason>" (an existing code, so T7 and T8 need no new
  mapping). A success resets the attempt count.
- **Limit**: at most one stream connection per agent (D11's takeover makes a
  second replace the first), so at most two per environment; no new global
  connection limit.
- **Keep-alive** 30 s like the control connection; CleanStart; the existing
  session expiry (≥ 10 s) is kept, harmless because CleanStart discards it.
- **Send buffer**: the stream connection's socket send buffer is set to
  **64 KiB** (`ConnectOptions.WriteBuffer`, applied by `mqttclient` with
  `(*net.TCPConn).SetWriteBuffer` before the TLS handshake); the control
  connection keeps the OS default. It bounds how much stream data can sit in
  the kernel ahead of the shared bottleneck (the spike's caveat); D2's 1 MiB
  output queue stays in user space where the drop marker works.

### D15. Renewal and `ca.update` stay on the control connection. **Owner review.**

- `cert.renew` (on `up`) and `ca.update` (on `req`) are control topics, so
  they only ever travel on the control connection; D12 refuses them on
  `<cn>.t`.
- The stream connection takes its TLS configuration from the agent's
  current identity **at dial time**: the session loop calls
  `streams.configure(id)` after every connect, after a renewal and after a
  `ca.update`, and the next dial uses that copy. An open stream connection
  keeps the certificate and roots it was dialled with until it closes, as
  the control connection does today; renewal doesn't revoke the old serial,
  so nothing drops. The two connections may briefly hold different serials:
  `ws env rm` and `ws env rotate` revoke the current and prior serials, so
  both are still kicked.

### D16. The controller notices a lost stream connection. **Owner review.**

The controller can't see the stream connection's drop (no will). Two checks
in T6, both on the existing 15 s ping tick per terminal:

1. If `broker.Clients()` has no peer with `ClientID ==
   envproto.StreamClientID(envID)`, the terminal ends with `Exit{Reason:
   "disconnected"}`.
2. The agent answers a frame on `stream/<sid>/in` for a sid it doesn't have
   with one `FrameExit` `{code: null, reason: "disconnected"}` on
   `stream/<sid>/out`; the controller accepts a `FrameExit` for a known sid
   **whatever its sequence number** and ends the terminal. This covers a
   stream connection that dropped and was re-dialled for another terminal
   inside the 15 s.

So a terminal whose stream connection is gone ends within about 15 s plus a
round trip, like an offline environment (about 11 s).

### D17. Re-measure on the real second connection before T4 and T6. **Owner review.**

T1d repeats T1's head-of-line probe with the real hook, policy and client:
a control connection `<id>` answering requests while the stream connection
`<id>.t` saturates `stream/<sid>/out`, through the throttling proxy in two
modes, each connection paced alone, and both through **one shared
bottleneck queue** (the measurement the spike lacked). **Pass**, on the
20 ms / 10 Mbit/s profile with the shared queue: every control request
answered and the p50 under load ≤ 2 × idle p50 + 100 ms (D1's limit). It
records the numbers in terminal-spike.md › Second connection.

If it fails only through the shared queue, the orchestrator **stops and asks
the owner** before any more terminal work. The options to put to them:
(a) bound the stream's bytes in flight further (a smaller send buffer, or
the agent pacing its output); (b) ADR 0074's next fallback, a direct
WebSocket (D1's second branch), noting that a WebSocket crosses the same
bottleneck and would need the same bound; (c) accept and document the
measured delay. If it fails with each connection paced alone, T1b or T1c
has a bug: fix it there.

### D18. ADR sentences the second connection contradicts (T18 writes the amendments). **Owner review.**

| ADR | Sentence | Amendment |
| --- | --- | --- |
| 0074, Decision › Identity | "the broker's hook sets the MQTT client id to that name, overwriting the id the client claims, and takes every ACL decision from it" | The hook sets the client id to the CN, or to `<cn>.t` when the client claims exactly that (the agent's stream connection, same certificate); a claimed `.t` id of another CN is refused. ACL decisions use the CN and which of the two ids it is (D11). |
| 0074, Topics table | `stream/<sid>/out` publisher "agent"; `stream/<sid>/in` subscriber "agent" | "agent's stream connection (`<id>.t`)" for both stream rows; the others "agent's control connection". |
| 0074, Decision › ACL | "An agent may publish only to its own `status`, `snapshot`, `res/#`, `stream/+/out` and `up`, and subscribe only to its own `req` and `stream/+/in`" | D12's table: the control connection may publish `status`, `snapshot`, `res/<x>`, `up` and subscribe `req`; the stream connection may only publish `stream/<x>/out` and subscribe `stream/+/in`; stream publishes may not be retained. |
| 0074, Decision › Presence | "Last Will with a 10 s delay publishes `offline`; the agent publishes `online` on connect" | On its control connection. The stream connection has no will, opens with the first terminal, closes 60 s after the last, and is refused or dropped by the broker whenever the control connection isn't live (D13, D14). |
| 0074, Why this option, 5 | "`QoS 0` for streams, so one slow terminal can't hold up control messages" | Measured false on one connection: a saturated stream delayed a control request about 2 s at 20 ms / 10 Mbit/s (T1). Streams travel on a second connection so they can't; T1d's number through a shared bottleneck. |
| 0074, Where it's weak | "head-of-line blocking on one TCP connection is shared with control messages. If measured terminal latency is poor, terminals move to a second connection or a direct WebSocket" | (D8's row) the second connection is taken; what remains is the shared bottleneck, bounded by the 64 KiB send buffer and the 1 MiB queue, with T1d's number. |
| 0074, Consequences › Conformance | the list of conformance tests | Add: stream topics refused on the control connection and control topics on the stream connection; the stream connection doesn't kick the control one; a revoked serial drops both. |
| 0074, Amendments › takeover | "The hook takes over the old session itself before the new one is accepted" | Per client id: `<cn>` and `<cn>.t` are taken over separately. |
| 0073, Decision › What it is | "A long-lived process on the remote that holds one MQTT connection to the controller's broker" | "holds one control connection, and a second one for terminal streams while a terminal is open ([0074](0074-mqtt-transport-embedded-broker.md), amended)". |
| 0075, Renewal | "The agent sends `cert.renew` with a new CSR over the live mTLS connection" | "over its control connection"; a stream connection dialled after a renewal uses the new certificate, an open one keeps the old until it closes (D15). |
| 0075, Revocation | "the broker refuses its certificate serial from then on, and disconnects it now" | "…disconnects both of its connections now". |
| 0075, Rotating the CA, step 4 | "`--force`, which disconnects the rest" | unchanged meaning; add "(both connections of each)". |
| 0076, stack table row 4 | "`terminal.*` streams" | "`terminal.*` streams on a second agent connection (0074, amended)". |
| `docs/tools/ws-env.md` lines 91 and 122 | "renews … over the connection"; "keeps the connection and runs its work" | name the control connection, and add the stream connection to the Terminals section T18 writes. |

## Order

```
T1 (spike, done: no-go) ─┐
T1a ─┬─ T1b ─┬─ T1d (re-measure) ─┐ gates T4, T6 (and so T5, T7, T8, T11)
     └─ T1c ─┘                    │
T2 ─┬─ T4 ─ T5 ──────────────┐    (T4 also needs T1c; T6 also needs T1b)
T3 ─┘                         ├─ T11
T2 ── T6 ── T7 ── T8 ─────────┘
T9 ─┬──────────── T12 ─┬─ T14 ─┬─ T15
T10 ┘  (T12 also needs T8)     ├─ T16 ─ T17
T13 ───────────────────┘        └──────────── T18 (after all)
```

Edges: T1a←–; T1b←T1a; T1c←T1a; T1d←T1,T1b,T1c; T4←T1d,T1c,T2,T3;
T5←T4; T6←T1d,T1b,T2; T7←T6; T8←T7,T10; T9←–; T10←–; T11←T5,T8;
T12←T8,T9,T10; T13←–; T14←T12,T13; T15←T14; T16←T14; T17←T16;
T18←T11,T15,T17. (T1 must be merged into `feat/env-ui` before T1d: T1d
extends its `spike` test files.)

T1a, T1b and T1c depend only on merged code (PR 3) and run beside T2, T3,
T9, T10 and T13; T1b and T1c run in parallel with each other (no shared
file). T1d is short (one test file, one run) and is the only new step on
the critical path to T4 and T6.

| Task | Area | Depends on | Parallel with | Size | Model |
| --- | --- | --- | --- | --- | --- |
| T1 Spike: terminal latency, throughput, slow reader, memory | spike | – | T2, T3, T9, T10, T13 | | `sonnet` |
| T1a `envproto`: stream client id and the write-buffer option | second connection | – | T2, T3, T9, T10, T13 | `small` | |
| T1b Broker and policy: the stream connection's identity and ACL | second connection | T1a | T1c, T2, T3, T9, T10, T13 | | `sonnet` |
| T1c Agent: the stream connection, its lifecycle; `mqttclient` write buffer | second connection | T1a | T1b, T2, T3, T9, T10, T13 | | `sonnet` |
| T1d Re-measure head-of-line on the second connection | spike | T1, T1b, T1c | T9, T10, T13 | `small` | `sonnet` |
| T2 Protocol: terminal ops, frames, limits, protocol 2 | agent terminals | – | T1, T3, T9, T10, T13 | | |
| T3 `internal/ptyproc`: a process in a pseudo-terminal | agent terminals | – | T1, T2, T9, T10, T13 | `small` | |
| T4 Agent: terminals, streams, limits, lifetime | agent terminals | T1d, T1c, T2, T3 | T6, T9, T10, T13 | | `sonnet` |
| T5 Agent side in `internal/cli`: attach and shell command lines | agent terminals | T4 | T6, T7 | `small` | |
| T6 Controller: `OpenTerminal`, gaps, buffer, ping, stream liveness | controller and daemon | T1d, T1b, T2 | T3, T4, T5, T9, T10, T13 | | `sonnet` |
| T7 Daemon: the upgraded terminal route and client | controller and daemon | T6 | T5, T9, T13 | | `sonnet` |
| T8 CLI: `ws attach` and `ws shell --env` | CLI | T7, T10 | T13 | | |
| T9 `ws list`: every environment by default, `agent_state` | CLI | – | T1-T7, T13 | | |
| T10 CLI: `--env` on `rm`, `rename`, `hide`, `unhide`; `ws stop` | CLI | – | T1-T7, T13 | | |
| T11 Conformance: terminals over the real broker | CLI | T5, T8 | T12-T17 | | `sonnet` |
| T12 Desktop backend: keyed entries, `--env` routing, remote terminals | desktop backend | T8, T9, T10 | T11 | | |
| T13 Desktop backend: environment commands | desktop backend | – | T1-T11 | `small` | |
| T14 Frontend: types, `environmentsSlice`, polling, tree keys | desktop frontend | T12, T13 | T11 | | |
| T15 Settings › Environments | desktop frontend | T14 | T16, T17 | | |
| T16 Environment picker: New agent and New shell | desktop frontend | T14 | T15 | `small` | |
| T17 Agents list: badges, filter, offline, pane | desktop frontend | T16 | T15 | | |
| T18 Docs and ADR amendments | docs | T11, T15, T17 | – | | |

Heavy children (full `internal/cli` tests, `cargo test`, vitest): T8, T9,
T10, T11, T12, T14-T17. Run at most one at a time on an 8 GB pod; T1-T7 and
T13 are package-local and may run beside one heavy child (T1c's
`internal/cli` change is one file, tested with `-run`). T1d's measurement
runs alone: no other child may run beside it, because timing is its output.

---

## T1. Spike: terminal latency, throughput, slow reader, memory (`sonnet`)

- **Depends on:** nothing. **Parallel:** T2, T3, T9, T10, T13.
- **Pinned values touched:** constraints-ui.md › Broker and streams.
- **Gates:** T4, T6, T7, T8, T11 don't start until the orchestrator has read
  its verdict. On no-go, the orchestrator follows D1 (and asks the owner
  before the WebSocket path).
- **Status:** done on `feat/env-ui-t1`, **no-go on head-of-line** (D1 ›
  Result). Follow-up: T1a-T1d (the second connection), and T1d repeats the
  head-of-line probe on it (D17). Merge `feat/env-ui-t1` into `feat/env-ui`
  before T1d starts.

### Files

Create:

- `internal/mqttbroker/termspike_test.go` (`//go:build spike`, `package
  mqttbroker`)
- `internal/mqttbroker/throttle_test.go` (`//go:build spike`): the proxy
- `docs/design/remote-environments/terminal-spike.md`

Nothing else changes; no production code.

### The harness

Reuse the setup of `internal/mqttbroker/conformance_test.go` (CA, server and
agent certificates, `environment.Policy`/`Rules`, `New(log).Start(ctx,
"127.0.0.1:0", tlsConfig, rules)`, `mqttclient.New(log).Connect`). The
controller side is the broker's in-process `Publish`/`Subscribe` (as
`internal/environment` uses it in the daemon). The agent connects through
the proxy:

```go
// throttle is a TCP proxy on 127.0.0.1:0 to target that delays each
// direction by rtt/2 and paces bytes at bitsPerSecond (0: unpaced).
type throttle struct{ /* listener, target, rtt, bps */ }
func newThrottle(t *testing.T, target string, rtt time.Duration, bitsPerSecond int) *throttle
func (p *throttle) Addr() string
```

Implement delay with a per-direction goroutine that reads into 4 KiB
buffers, stamps each with `time.Now()`, and writes it after `rtt/2`; pace
with a token bucket over bytes. Close everything on `t.Cleanup`.

Profiles: `{"loopback", 0, 0}`, `{"20ms-10mbit", 20ms, 10_000_000}`,
`{"80ms-2mbit", 80ms, 2_000_000}`.

### Measurements (each a `t.Run`, printing one result line with `t.Logf`)

```go
func TestSpike_keystrokeEcho(t *testing.T)   // 1000 keys at 10/s; p50, p95, p99, max
func TestSpike_throughput(t *testing.T)      // 16 KiB QoS 0 chunks, 10 s; MiB/s, gaps
func TestSpike_slowReader(t *testing.T)      // handler sleeps 5 ms; drops; workspace.list RTT idle vs saturated; agent read-loop stall
func TestSpike_brokerMemory(t *testing.T)    // HeapInuse with 1, 4, 8 saturated streams to a slow reader
```

Frames use a placeholder format (8-byte big-endian sequence number +
payload); T2 defines the real one. Input is published by the controller at
QoS 1 on `ws/v1/env/<id>/stream/<sid>/in`; output by the agent at QoS 0 on
`…/out`. For "agent read-loop stall", time how long a QoS 1 publish from the
agent on `res/x` takes to reach the controller while the slow handler runs,
once with the handler sleeping inline and once with it pushing to a
buffered channel (`cap 64`) that a goroutine drains.

### `terminal-spike.md`

Sections: Setup (versions, CPU, `go version`, the commit), one table per
measurement and profile, **Verdict** (`GO` or `NO-GO` against D1's
thresholds, quoting them), **What changes** (nothing, or which D1 branch),
and **Not measured** (a real WAN, Windows, the TLS handshake on a real
network, many agents at once).

### Acceptance

- `go test -tags spike -count=1 -run '^TestSpike_' -v ./internal/mqttbroker/`
  completes in under 10 minutes and prints every number the doc quotes.
- `go vet -tags spike ./internal/mqttbroker/` is clean; `make lint` is clean
  (the default build never sees the files).
- The doc states the go / no-go in its first paragraph.

### Verify

```
go test -tags spike -count=1 -run '^TestSpike_' -v -timeout 15m ./internal/mqttbroker/ 2>&1 | tee $CLAUDE_JOB_DIR/tmp/spike.log
go test -p 2 -count=1 ./internal/mqttbroker/
make lint
```

---

## T1a. `envproto`: stream client id and the write-buffer option (`small`)

- **Depends on:** nothing (merged code). **Parallel:** T2, T3, T9, T10, T13.
- **Pinned values touched:** constraints-ui.md › Second connection.
- Exists so T1b and T1c, which both need these names, can run in parallel.

### Files

Change: `internal/envproto/topics.go`, `topics_test.go`,
`internal/envproto/envproto.go` (`ConnectOptions`), `envproto_test.go` only
if a test there builds `ConnectOptions` by position (none does today).
Nothing else; no behaviour change anywhere (the field is read by T1c).

```go
// StreamSuffix ends the client id of an agent's stream connection (D11).
const StreamSuffix = ".t"

// StreamClientID is the client id of environment id's stream connection:
// id + StreamSuffix.
func StreamClientID(id string) string

// ConnectOptions gains (after WillDelay):
	// WriteBuffer is the socket send buffer in bytes; 0 keeps the OS default (D14).
	WriteBuffer int
```

### Tests

`TestStreamClientID` (`topics_test.go`): "appends .t to the id"; and
"ParseTopic refuses a .t id" in `TestParseTopic` (a topic
`ws/v1/env/env-0123abcd.t/status` is not ok: `ValidID` already refuses it,
the test pins that a stream client id can never name a topic tree).

### Verify

```
go test -p 2 -count=1 ./internal/envproto/
make lint
```

---

## T1b. Broker and policy: the stream connection's identity and ACL (`sonnet`)

- **Depends on:** T1a. **Parallel:** T1c, T2, T3, T9, T10, T13.
- **Pinned values touched:** constraints-ui.md › Second connection, `envagent`
  (the ACL row), Broker and streams.
- **Security-critical**: the mutation-testing reviewer removes or inverts
  each check below (the suffix match, the foreign-`.t` refusal, the
  per-id takeover, the control-connection requirement, the
  control-drop kick, the will refusal, the retain refusal, each `allowed`
  branch) and confirms a named test fails.

### Files

Change: `internal/environment/policy.go`, `policy_test.go`,
`internal/mqttbroker/broker.go`, `broker_test.go`,
`internal/mqttbroker/conformance_test.go` (a new test function only).
`internal/environment/envtest` doesn't change (it takes the claimed id as
given and runs the same `rules`, so the policy applies there too).

### `policy.go`

```go
var errClientIDMismatch = errors.New("client id isn't the certificate's common name or its stream id")

// connect: peer.ClientID must be peer.CN or envproto.StreamClientID(peer.CN);
// the other checks are unchanged and apply to both.
func (p *policy) connect(peer envproto.Peer) error

// allowed is ADR 0074's ACL split by connection (D12): the control
// connection (ClientID == CN) publishes status, snapshot, up, res/<x> and
// subscribes req; the stream connection (ClientID == CN + ".t") publishes
// stream/<x>/out and subscribes stream/<x>/in or stream/+/in; any other
// ClientID, a topic of another id, "#", and anything outside ws/v1/env/ are refused.
func allowed(peer envproto.Peer, access envproto.Access, topic string) bool

func controlPublishAllowed(rest []string) bool   // status, snapshot, up, res/<x>
func controlSubscribeAllowed(rest []string) bool // req
func streamPublishAllowed(rest []string) bool    // stream/<x>/out
func streamSubscribeAllowed(rest []string) bool  // stream/<x>/in, stream/+/in
```

(`publishAllowed` and `subscribeAllowed` are replaced by these four.)

### `broker.go`

```go
var (
	errForeignStreamID     = errors.New("stream client id of another environment")
	errNoControlConnection = errors.New("stream connection without a control connection")
	errStreamWill          = errors.New("a stream connection may not set a will")
)

// peerOfConn reads the verified certificate; ClientID is the CN, or
// StreamClientID(CN) when claimed is exactly that. A claim that ends in
// StreamSuffix otherwise is errForeignStreamID.
func peerOfConn(cl *mqtt.Client, claimed string) (envproto.Peer, error)

// isStream reports whether peer is a stream connection.
func isStream(peer envproto.Peer) bool

// hasControl reports whether a control connection of cn is live.
func (b *Broker) hasControl(cn string) bool

// takeOver ends the old connection with client id id (not CN), as today.
func (b *Broker) takeOver(id string)

// untrack forgets a connection; when it was a control connection it also
// disconnects the live stream connection of the same CN (from a goroutine,
// after b.mu is released).
func (b *Broker) untrack(cl *mqtt.Client)

// OnPublish disconnects a non-inline client that publishes a retained
// message on a stream topic, and rejects the packet (packets.ErrRejectPacket).
func (h *hook) OnPublish(cl *mqtt.Client, pk packets.Packet) (packets.Packet, error)
```

- `OnConnect` and `OnConnectAuthenticate` pass
  `pk.Connect.ClientIdentifier` as `claimed`. `OnConnect` refuses a stream
  peer whose CONNECT has a will (`errStreamWill`), then checks a control
  peer's will topic as today. `OnConnectAuthenticate`: `peerOfConn`,
  `rules.Connect`, then for a stream peer `hasControl(peer.CN)` (refuse with
  `errNoControlConnection` otherwise), then `cl.ID = peer.ClientID`,
  `takeOver(peer.ClientID)`, `track`.
- `Provides` adds `mqtt.OnPublish`.
- `peerOf`'s fallback: `envproto.Peer{ClientID: cl.ID, CN:
  strings.TrimSuffix(cl.ID, envproto.StreamSuffix)}`.
- `Clients()` and `Disconnect(serial)` keep their code; their doc comments
  say a stream connection is listed (`ClientID` `<id>.t`) and dropped too.

### Tests

`TestAllowed` (table) gains: "the control connection may not publish stream
out", "the control connection may not subscribe stream in", "the stream
connection may publish its stream out", "the stream connection may subscribe
stream/+/in", "the stream connection may not publish status", "… snapshot",
"… up", "… res", "the stream connection may not subscribe req", "the stream
connection may not publish another environment's stream out", "another
environment's stream id is refused" (`{CN: A, ClientID: B.t}`), "a client id
with another suffix is refused" (`A.x`). The existing rows "publishes a
stream", "subscribes to a stream's in" and "subscribes to every stream's
in" become `false` (they use the control peer) and are repeated with the
stream peer as `true`; every other existing stream row is repeated with the
stream peer and keeps its `false`.

`TestPolicy_connect` gains "accepts the stream client id", "refuses
another environment's stream client id", "refuses a revoked serial on the
stream client id".

`TestBroker_streamConnection` (`broker_test.go`, the fixture's
`connect(t, cn, claimedID, will)`): "a claimed <cn>.t id becomes the stream
client id", "a stream connection does not take over the control
connection", "a second stream connection takes over the first", "a claimed
.t id of another CN is refused", "a stream connection without a control
connection is refused", "closing the control connection disconnects the
stream connection", "a control takeover disconnects the stream connection",
"a will on a stream connection is refused", "a retained publish to a stream
topic disconnects", "Disconnect by serial drops both connections", "Clients
lists both connections". `TestBroker_connect` "the client id is the
certificate CN, whatever the client claims" stays as is.

`TestConformance_streamConnection` (`conformance_test.go`: real broker, real
`environment` policy through `newStack`, `mqttclient`): "a stream-topic
publish on the control connection is refused and disconnects", "a
stream-topic subscribe on the control connection is refused and
disconnects", "a control-topic publish on the stream connection is refused
and disconnects" (`status`), "a request subscribe on the stream connection
is refused and disconnects", "the stream connection does not kick the
control connection" (the environment stays `online`, a request is still
answered), "one environment cannot open a stream connection for another"
(A's certificate claiming `<B>.t` is refused; B's two connections stay up),
"a revoked serial kicks both connections" (the same path as
`TestConformance_revocation`), "stream frames flow both ways on the stream
connection" (controller `Publish` on `stream/<sid>/in` reaches it; its
`stream/<sid>/out` reaches the controller's `Subscribe`).

### Acceptance

- Every named test passes with `-race`; the D6 rows for D11 and D12 name
  them.
- `go test ./internal/mqttbroker/ ./internal/environment/ ./internal/envagent/`
  pass unchanged otherwise (no released agent uses stream topics on the
  control connection).

### Verify

```
go test -p 2 -count=1 -race ./internal/environment/ -run 'TestAllowed|TestPolicy'
go test -p 2 -count=1 -race ./internal/mqttbroker/
go test -p 2 -count=1 ./internal/environment/ ./internal/envagent/
make lint
```

---

## T1c. Agent: the stream connection, its lifecycle; `mqttclient` write buffer (`sonnet`)

- **Depends on:** T1a. **Parallel:** T1b, T2, T3, T9, T10, T13.
- **Pinned values touched:** constraints-ui.md › Second connection,
  `envagent` (signatures).
- **Concurrency-heavy**: the connection is shared by terminal goroutines
  (T4), the session loop and the idle timer. The reviewer runs `-race` and
  removes each of: the backoff wait, the idle re-check, the close on
  control loss, `configure` after renewal.

### Files

Create: `internal/envagent/streams.go`, `streams_test.go`.
Change: `internal/envagent/agent.go` (`Options.StreamIdle`, default 60 s in
`DefaultOptions`; `Agent.streams`; `SetStreamClient`; `serve` closes the
stream connection on both exits; `connect` calls `configure`),
`renew.go` and `requests.go` (`configure` after a renewal and after
`updateRoots`), `agent_test.go`, `internal/mqttclient/client.go`,
`client_test.go` (`WriteBuffer`), `internal/cli/env_agent.go` (a second
`a.newEnvClient(log)` passed to `agent.SetStreamClient`).
`NewAgent`'s signature doesn't change.

```go
// SetStreamClient gives the agent a second client for terminal streams
// (D11-D14). Call it before Run; without it terminal.open is refused (T4).
func (a *Agent) SetStreamClient(client mqttClient)

// streamConn is the agent's stream connection: dialled on first use,
// closed StreamIdle after the last user, closed with the control connection.
type streamConn struct{ /* log, client, opts, mu, tls *tls.Config, id, broker string,
	connected bool, users int, attempt int, retryAt time.Time, idleGen int,
	onIn func(envproto.Message), onDrop func() */ }

var (
	errStreamUnavailable = errors.New("terminals aren't available on this agent")
	errStreamBackoff     = errors.New("the terminal connection failed recently; retrying later")
	errStreamClosed      = errors.New("the terminal connection is closed")
)

func newStreamConn(log *slog.Logger, client mqttClient, opts Options) *streamConn

// handle sets the input handler for stream/+/in and the drop callback
// (T4 sets both; before that, input is dropped and onDrop does nothing).
func (s *streamConn) handle(onIn func(envproto.Message), onDrop func())

// configure copies where and as whom to dial: id.ID, id.Broker, id.TLSConfig().
func (s *streamConn) configure(id *Identity)

// acquire connects if needed (ClientID StreamClientID(id), no will,
// KeepAlive opts.KeepAlive, WriteBuffer streamWriteBuffer), subscribes
// Topic(id, "stream", "+", "in"), and counts one user. errStreamUnavailable
// with no client, errStreamBackoff before retryAt, or the dial error.
func (s *streamConn) acquire(ctx context.Context) error

// release drops one user; at zero, the connection closes after
// opts.StreamIdle unless acquired again in between.
func (s *streamConn) release()

// publish sends msg on the stream connection (errStreamClosed when down).
func (s *streamConn) publish(ctx context.Context, msg envproto.Message) error

// close ends the connection now and calls onDrop once if it was up.
func (s *streamConn) close()

const streamWriteBuffer = 64 << 10 // D14
```

- When a connection's `Done()` closes on its own, a watcher goroutine marks
  it down and calls `onDrop` once; `users` is not reset (each terminal's
  close calls `release`). No reconnect until the next `acquire`.
- A failed dial sets `retryAt = now + backoff(attempt, BackoffMin,
  BackoffMax, Jitter())` and `attempt++`; a success resets both.
- `serve` calls `a.streams.close()` on `<-done` (control lost) and on
  `ctx.Done()` before `goOffline`.
- `mqttclient.Connect`: when `opts.WriteBuffer > 0`, dial TCP with
  `net.Dialer`, call `SetWriteBuffer`, then `tls.Client(...).HandshakeContext`;
  otherwise as today.

### Tests

`TestStreamConn_acquire` (`envtest` broker with the real policy, a second
`envtest.Client`, fake clock): "connects once with the .t client id and no
will", "subscribes to its own stream/+/in", "a second acquire reuses the
connection", "without a client it is errStreamUnavailable", "a failed
connect backs off" (the next acquire before `retryAt` is
`errStreamBackoff` without dialling), "a success resets the backoff",
"dials with the TLS config of the last configure".
`TestStreamConn_release`: "closes after StreamIdle with no users", "an
acquire during the idle wait keeps it open".
`TestStreamConn_close`: "a broker drop calls onDrop once and keeps the
control connection", "close calls onDrop once".
`TestAgent_Run` gains "losing the control connection closes the stream
connection", "stopping the agent closes the stream connection", "a renewal
reconfigures the stream connection", "a ca.update reconfigures the stream
connection". `TestClient_Connect` (`mqttclient`) gains "WriteBuffer sets
the socket's send buffer" (linux: `SO_SNDBUF` read back through
`SyscallConn` is ≥ the request; the kernel doubles it).

### Verify

```
go generate ./internal/envagent/
go test -p 2 -count=1 -race ./internal/envagent/ ./internal/mqttclient/
go test -p 2 -count=1 -run 'Agent|Env' ./internal/cli/ ./internal/mqttbroker/
make lint
```

---

## T1d. Re-measure head-of-line on the second connection (`small`, `sonnet`)

- **Depends on:** T1 (merged into `feat/env-ui`), T1b, T1c. **Parallel:**
  none while measuring (D17: timing); the orchestrator may run T9, T10 or
  T13 only after the measurement finishes.
- **Pinned values touched:** constraints-ui.md › Broker and streams, Second
  connection.
- **Gates:** T4 and T6 don't start until the orchestrator has read its
  verdict. On fail through the shared queue only, the orchestrator stops and
  asks the owner (D17's options); on fail with separate pacing, it sends T1b
  or T1c back.

### Files

Change: `internal/mqttbroker/termspike_test.go`, `throttle_test.go` (both
`//go:build spike`), `docs/design/remote-environments/terminal-spike.md`
(a new section "Second connection" after "Verdict", and the first paragraph
names its result). No production code.

```go
// sharedThrottle is a TCP proxy like throttle whose connections share one
// bottleneck per direction: a FIFO of at most queueBytes (4 KiB chunks from
// every connection, in arrival order) drained by one goroutine that delays
// by rtt/2 and paces at bitsPerSecond.
func newSharedThrottle(t *testing.T, target string, rtt time.Duration, bitsPerSecond, queueBytes int) *sharedThrottle
func (p *sharedThrottle) Addr() string

func TestSpike_secondConnection(t *testing.T)
```

The probe: the real broker with `environment` rules (as T1 and the
conformance test build them), one enrolled environment, the control
connection (`mqttclient`, client id `<id>`, the agent's will) answering
QoS 1 requests on `req` with replies on `res/<n>`, and the stream
connection (`mqttclient`, client id `<id>.t`, `WriteBuffer: 64 KiB`)
flooding 16 KiB QoS 0 frames on `stream/<sid>/out` to a buffered
controller handler (cap 64). 20 control requests, 200 ms apart; idle p50
first (no stream), then under load. Modes, on the `20ms-10mbit` profile:
`separate` (two `throttle` proxies, one per connection), `shared-64k`
(`sharedThrottle`, queue 64 KiB), and, for information only, `shared-256k`
(a bloated queue) and the `80ms-2mbit` profile in both shared modes.

### Acceptance

- **Pass**: in `separate` and `shared-64k` on `20ms-10mbit`, all 20 requests
  answered and the p50 under load ≤ 2 × idle p50 + 100 ms. The doc records
  idle p50, p50 and max under load, answered, and stream MiB/s for every
  mode, with the verdict and the commit.
- `go vet -tags spike ./internal/mqttbroker/` is clean.

### Verify

```
go test -tags spike -count=1 -run '^TestSpike_secondConnection$' -v -timeout 10m ./internal/mqttbroker/ 2>&1 | tee $CLAUDE_JOB_DIR/tmp/spike2.log
go vet -tags spike ./internal/mqttbroker/
make lint
```

---

## T2. Protocol: terminal ops, frames, limits, protocol 2

- **Depends on:** nothing. **Parallel:** T1, T3, T9, T10, T13.
- **Pinned values touched:** constraints-ui.md › Wire payloads, Error codes
  (none here; T4 uses `terminal_limit` as a string).

### Files

Create: `internal/envproto/terminal.go`, `internal/envproto/terminal_test.go`.
Change: `internal/envproto/ops.go`, `ops_test.go`, `payloads.go`
(`Protocol = 2`), `payloads_test.go` (the two `"protocol":1` literals at
lines 92 and 124 become 2; use `envproto.Protocol` where the test builds
the value).

`internal/envproto` imports only the standard library.

### `ops.go` changes

```go
const (
	OpTerminalOpen  = "terminal.open"
	OpTerminalClose = "terminal.close"
	TermAttach = "attach" // terminal.open kinds
	TermShell  = "shell"
)

// CallerTerminal: sent on env/<id>/req only by the controller's terminal
// path; the daemon's call endpoint refuses it (D6).
const CallerTerminal Caller = iota + 4 // keep CallerController/Agent/Internal values
```

`LookupOp`: `terminal.open` → `{Caller: CallerTerminal, Since: 2, Timeout:
30 * time.Second, Changes: true}`; `terminal.close` → `{Caller:
CallerTerminal, Since: 2, Timeout: 30 * time.Second}`. `newArgs` and
`Validate` learn both. The `validateKind` refusal of `shell` in
`workspace.create` stays (a shell is a terminal).

### `terminal.go`

```go
// Limits of a terminal stream (ADR 0074, D2, D6, D10).
const (
	MaxChunkBytes     = 16 << 10  // a data frame's body
	MaxInputBytes     = 4 << 10   // an input frame's body
	MaxQueuedOutput   = 1 << 20   // the agent's and the controller's output queue
	InputPaceBytes    = 64 << 10  // per second, the sender's pace
	InputLimitBytes   = 128 << 10 // per second, over this the agent closes
	InputLimitFrames  = 500       // per second, over this the agent closes
	MaxTerminals      = 8         // open per agent
	PingInterval      = 15 * time.Second
	IdleClose         = 60 * time.Second
	RedrawEvery       = time.Second // at most one redraw per terminal
	frameHeader       = 10          // version, kind, sequence
	frameVersion      = 1
)

// FrameKind is a stream frame's kind.
type FrameKind byte

// Output kinds (agent → controller, on stream/<sid>/out, QoS 0).
const (
	FrameData    FrameKind = 'd' // pty bytes, 1..MaxChunkBytes
	FrameDropped FrameKind = 'g' // body: uint64 big-endian bytes dropped
	FrameExit    FrameKind = 'x' // body: JSON {"code": int|null, "reason": string}
)

// Input kinds (controller → agent, on stream/<sid>/in, QoS 1).
const (
	FrameInput  FrameKind = 'i' // keystrokes, 1..MaxInputBytes
	FrameResize FrameKind = 'r' // body: uint16 cols, uint16 rows, big-endian
	FrameRedraw FrameKind = 'w' // empty
	FramePing   FrameKind = 'p' // empty
	FrameClose  FrameKind = 'c' // empty
)

// Frame is one stream message: [version 1][kind][uint64 seq big-endian][body].
type Frame struct {
	Kind FrameKind
	Seq  uint64 // per direction per sid, from 1
	Body []byte
}

// MarshalFrame encodes f; it refuses an unknown kind or a body over its limit.
func MarshalFrame(f Frame) ([]byte, error)

// UnmarshalFrame decodes b, refusing a wrong version, an unknown kind, a
// short header, or a body over its kind's limit (ErrFrame).
func UnmarshalFrame(b []byte) (Frame, error)

// ErrFrame is wrapped by every frame decoding error.
var ErrFrame = errors.New("invalid stream frame")

// Resize builds and ParseResize reads a resize body; sizes are 1..1000.
func ResizeBody(cols, rows int) []byte
func ParseResize(body []byte) (cols, rows int, err error)

// DroppedMarker is the text a receiver writes for a FrameDropped or a gap.
func DroppedMarker(bytes uint64) []byte // "\r\n\x1b[7m[ws: N bytes of output dropped]\x1b[0m\r\n"
func LostMarker() []byte                // "\r\n\x1b[7m[ws: output lost; redrawing]\x1b[0m\r\n"

// ValidSID reports whether sid is 16 lowercase hex characters.
func ValidSID(sid string) bool

// TerminalOpenArgs are terminal.open's arguments.
type TerminalOpenArgs struct {
	Kind      string   `json:"kind"`      // TermAttach or TermShell
	Workspace *string  `json:"workspace"` // attach: a workspace reference; null for shell
	Name      *string  `json:"name"`      // shell only
	Specs     []string `json:"specs"`     // shell only; [] for none
	Offline   bool     `json:"offline"`   // shell only
	Cols      int      `json:"cols"`      // 1..1000
	Rows      int      `json:"rows"`      // 1..1000
}

// TerminalOpenResult is terminal.open's result.
type TerminalOpenResult struct {
	SID       string  `json:"sid"`
	Workspace *string `json:"workspace"` // attach: the workspace id; shell: null (ws shell makes it)
	Session   *string `json:"session"`   // attach: the Claude session id; shell: null
}

// TerminalCloseArgs and TerminalCloseResult are terminal.close's.
type TerminalCloseArgs struct {
	SID string `json:"sid"`
}
type TerminalCloseResult struct {
	SID    string `json:"sid"`
	Closed bool   `json:"closed"` // false when it had already ended
}

// TerminalExit is a FrameExit's body.
type TerminalExit struct {
	Code   *int   `json:"code"`
	Reason string `json:"reason"` // "exited", "closed", "idle", "input_gap", "input_limit", "disconnected"
}
```

Validation (`Validate`, reusing `validateWorkspace`, `validateName`,
`validateSpec`): attach needs `workspace` and no `name`/`specs`/`offline`;
shell needs `workspace` null; sizes 1..1000; unknown kind is `usage`; a
`TerminalCloseArgs.SID` must be `ValidSID`.

### Tests (`terminal_test.go`)

`TestMarshalFrame` (round trip of every kind; refuses an unknown kind, a
body over the limit), `TestUnmarshalFrame` (wrong version, short header,
unknown kind, oversized: each wraps `ErrFrame`), `TestParseResize`,
`TestDroppedMarker`, `TestValidSID`, `TestValidate_terminalOpen` (table:
attach without workspace, shell with workspace, size 0 and 1001, a spec
starting with `-`, a name with a control character: each `*ArgsError` with
its code), `TestLookupOp` gains "terminal.open is CallerTerminal since 2".

### Acceptance

- `envproto.Protocol == 2`; every old test passes with the literals updated.
- No new import outside the standard library.

### Verify

```
go test -p 2 -count=1 ./internal/envproto/ ./internal/envagent/ ./internal/environment/
make lint
```

---

## T3. `internal/ptyproc`: a process in a pseudo-terminal (`small`)

- **Depends on:** nothing. **Parallel:** T1, T2, T9, T10, T13.
- **Pinned values touched:** constraints-ui.md › Lint rules (`depguard`,
  platform files).

`internal/claude` has an unexported pty (`pty_{linux,darwin,other,unix}.go`)
for `agent.send`. The agent needs a general one; a new package keeps
`internal/claude` unchanged (merging the two is a follow-up, recorded in
T18).

### Files

Create: `internal/ptyproc/ptyproc.go`, `ptyproc_unix.go`
(`//go:build linux || darwin`), `pty_linux.go`, `pty_darwin.go`,
`ptyproc_other.go` (`//go:build !linux && !darwin`: `Start` returns
`ErrUnsupported`), `ptyproc_test.go` (`//go:build linux || darwin`).

```go
// Package ptyproc runs a process on a new pseudo-terminal.
package ptyproc

// ErrUnsupported is returned on systems without the pty ioctls.
var ErrUnsupported = errors.New("pseudo-terminals aren't supported on this system")

// Spec is what to run.
type Spec struct {
	Path string   // absolute program path
	Args []string // argv[1:]
	Dir  string
	Env  []string // the whole environment
	Cols, Rows int
}

// Proc is a running process and its terminal's controlling side.
type Proc struct{ /* control *os.File, cmd *exec.Cmd, done chan struct{}, exit *int */ }

// Start opens a pty sized Cols×Rows, starts the process as the session
// leader with the pty as its controlling terminal (Setsid, Setctty), and
// returns once it runs.
func Start(spec Spec) (*Proc, error)

func (p *Proc) Read(b []byte) (int, error)  // the pty's output; io.EOF after exit
func (p *Proc) Write(b []byte) (int, error) // the process's input
func (p *Proc) Resize(cols, rows int) error // TIOCSWINSZ; the kernel sends SIGWINCH
// Close sends SIGHUP to the process group, then SIGKILL after 2 s if it
// still runs, and closes the pty. Safe to call twice.
func (p *Proc) Close() error
// Wait waits for the process and returns its exit code, nil when a signal ended it.
func (p *Proc) Wait() *int
```

Copy the `/dev/ptmx` code from `internal/claude/pty_linux.go`,
`pty_darwin.go` and `pty_unix.go` (same ioctls); `exec.Cmd` with
`SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}` and the tty as stdin,
stdout and stderr.

### Tests

`TestStart`: "runs a program and reads its output" (`/bin/sh -c 'printf hi'`),
"write reaches the program" (`cat`, write `abc\n`, read `abc`), "resize is
seen by the program" (`sh -c 'read x; stty size'` after `Resize(100, 40)`
prints `40 100`), "close ends a program that ignores input" (`sleep 60`;
`Close` returns within 3 s; `Wait` is nil or non-zero), "the exit code is
returned" (`sh -c 'exit 3'`). Each `t.Parallel()` (processes).

### Verify

```
go test -p 2 -count=1 -race ./internal/ptyproc/
GOOS=darwin go vet ./internal/ptyproc/
GOOS=windows go vet ./internal/ptyproc/
make lint
```

---

## T4. Agent: terminals, streams, limits, lifetime (`sonnet`)

- **Depends on:** T1d (pass, D17), T1c, T2, T3. **Parallel:** T6, T9, T10,
  T13.
- **Pinned values touched:** constraints-ui.md › `envagent` signatures,
  Broker and streams, Second connection.
- **Security-critical and concurrency-heavy**: the mutation-testing reviewer
  removes each D6 check owned here.
- **Amended after T1 (D11-D16):** streams travel on T1c's `streamConn`,
  never on `a.client`. `terminal.open` and `terminal.close` still arrive on
  `req` (control connection); frames on `stream/<sid>/in` arrive and frames
  on `stream/<sid>/out` leave through `a.streams`.

### Files

Create: `internal/envagent/terminals.go`, `internal/envagent/terminals_test.go`.
Change: `internal/envagent/interfaces.go` (add `terminalCommands`, the
`go:generate` line), `ops.go` (route `terminal.open`/`terminal.close` from
`req` to the terminal manager; refuse `CallerTerminal` ops on `up` and from
nowhere else), `agent.go` (call `a.streams.handle(onStreamIn,
closeAllDisconnected)` in `NewAgent`/`SetStreamClient`; close every
terminal when the control connection ends — `streams.close()` from T1c
triggers it through `onDrop`; **no** `stream/+/in` subscription on
`a.client`), `moq_test.go` (generated),
`internal/envagent/agent.go` `Options` gains `MaxTerminals` (default
`envproto.MaxTerminals`).

`NewAgent`'s signature does **not** change: the terminal commands come
through the `ops operationRunner` value, which must also implement
`terminalCommands` (checked with a type assertion at construction; when it
doesn't, `terminal.open` answers `usage` "terminals aren't available on this
agent"). `NoOperations{}` doesn't implement it.

```go
// terminalCommands builds what a terminal runs (internal/cli implements it,
// T5). It checks the workspace and returns the session id it attached to.
type terminalCommands interface {
	TerminalCommand(ctx context.Context, args *envproto.TerminalOpenArgs) (TerminalCommand, error)
}

// TerminalCommand is a program to run in a pty.
type TerminalCommand struct {
	Path      string
	Args      []string
	Dir       string
	Env       []string
	Workspace *string // the attached workspace id, nil for a shell
	Session   *string
}

// ptyStarter starts a pty process (ptyproc.Start in ws; a fake in tests).
type ptyStarter interface {
	Start(spec ptyproc.Spec) (ptyProcess, error)
}
type ptyProcess interface {
	io.ReadWriter
	Resize(cols, rows int) error
	Close() error
	Wait() *int
}
```

(`ptyStarter` is set by an unexported field defaulting to a `ptyproc`
adapter; tests replace it. moq generates `terminalCommandsMock`,
`ptyStarterMock`, `ptyProcessMock`.)

### Behaviour

- **Open** (`terminal.open` on `req`, validated by `DecodeArgs`): refuse with
  `terminal_limit` when `MaxTerminals` are open; for attach, run
  `checkWorkspaceRef` (PR 3) on `args.Workspace` (`outside_workspace`);
  `a.streams.acquire(ctx)` (`errStreamUnavailable` → `usage` "terminals
  aren't available on this agent"; `errStreamBackoff` or a dial error →
  `environment_offline` "the agent couldn't open its terminal connection:
  <reason>", D14); call `TerminalCommand`; `Start` the pty with the size;
  make a sid (`crypto/rand`, 8 bytes, hex); register it; start the reader
  and writer goroutines; reply `TerminalOpenResult`. Every failure after
  `acquire` calls `release`. An error from `TerminalCommand` that is a
  `*envproto.ReplyError` is replied with its code.
- **Output**: a reader goroutine reads the pty in `MaxChunkBytes` reads into
  a queue (a slice of chunks with a byte count, under the terminal's mutex);
  a publisher goroutine publishes `FrameData` at QoS 0 with the next
  sequence number, through `a.streams.publish`. When the queue would exceed `MaxQueuedOutput`, the oldest
  chunks are dropped and their bytes added to `dropped`; the publisher sends
  one `FrameDropped` before the next data, then requests a redraw of its own
  once the queue is under 256 KiB.
- **Input** (`stream/<sid>/in` handler on the stream connection, QoS 1; it
  only enqueues, D2): unknown sid → not written anywhere, logged at debug,
  and answered with one `FrameExit` `{code: null, reason: "disconnected"}`
  (seq 1) on `stream/<sid>/out` (D16); `UnmarshalFrame` error → close with reason `input_limit`;
  sequence ≠ expected → close with `input_gap`; `FrameInput` → token buckets
  (`InputLimitBytes`/s, `InputLimitFrames`/s), over → close `input_limit`,
  else write to the pty; `FrameResize` → `Resize`; `FrameRedraw` → if the
  last redraw is ≥ `RedrawEvery` ago, `Resize(cols, rows+1)` then after
  50 ms `Resize(cols, rows)`; `FramePing` → refresh `lastIn`; `FrameClose` →
  close with `closed`. Every frame refreshes `lastIn`.
- **Close** (any reason, once): `Close` the pty, wait, publish `FrameExit`
  with the code and reason (skipped when the stream connection is down),
  unregister, `a.streams.release()`. `terminal.close` closes and replies
  `{sid, closed}` (`closed: false` for an unknown sid, not an error).
- **Lifetime**: a ticker every 5 s closes terminals idle for `IdleClose`
  (reason `idle`); `onDrop` (T1c: the stream connection dropped, or the
  session loop closed it because the control connection ended or the agent
  stops) closes all terminals with reason `disconnected`.

### Tests (`terminals_test.go`, the D6 names)

`TestAgent_terminalOpen`: "opens an attach terminal and replies its sid",
"the sid is 16 hex characters", "a workspace outside workspaces/ is
refused", "a workspace with no Claude session is refused" (the mock returns
the `ReplyError`), "a ninth terminal is refused with terminal_limit", "an
open on up is refused", "input for an unknown sid is dropped and answered
with an exit", "a dropped connection closes every terminal", "a dropped
stream connection closes every terminal and keeps the agent online", "an
open without a stream client is usage", "an open whose stream connection
can't be dialled is environment_offline", "frames travel on the stream
connection, not the control one" (the control `envtest.Client` sees no
`stream/…` traffic), "the last close releases the stream connection",
"terminal.close of an unknown sid answers closed false". `TestTerminal_output`: "output is sent in frames of at most
16 KiB with increasing sequence numbers", "a full queue drops the oldest
output and sends one dropped frame", "a drop is followed by one redraw".
`TestTerminal_input`: "input is written to the pty", "an oversized frame
closes the terminal", "input over the rate closes the terminal", "a gap in
input closes the terminal", "resize resizes the pty", "redraw resizes by
one row and back", "a second redraw within a second is ignored", "no frame
for 60 s closes the terminal" (a fake clock: the agent's `now` func and a
test hook for the ticker), "close sends an exit frame with the reason".

Use `envtest`'s in-memory broker as the existing agent tests do, with the
real `environment` policy so D12's ACL applies (a stream frame on the
control client would disconnect it), a second `envtest.Client` given to
`SetStreamClient`, `ptyStarterMock` with an `io.Pipe` process, and `-race`.

### Verify

```
go generate ./internal/envagent/
go test -p 2 -count=1 -race ./internal/envagent/
go test -p 2 -count=1 ./internal/mqttbroker/ ./internal/cli/ -run 'Agent|Env'
make lint
```

---

## T5. Agent side in `internal/cli`: attach and shell command lines (`small`)

- **Depends on:** T4. **Parallel:** T6, T7.
- **Pinned values touched:** constraints-ui.md › `internal/cli` agent side.

### Files

Create: `internal/cli/env_agent_terminal.go`, `env_agent_terminal_test.go`.
Change: nothing else (`agentOperations` gains the method in the new file).

```go
// TerminalCommand implements envagent's terminalCommands (D6, D8).
func (o *agentOperations) TerminalCommand(ctx context.Context, args *envproto.TerminalOpenArgs) (envagent.TerminalCommand, error)
```

- **attach**: resolve `*args.Workspace` with the store (`workspace_not_found`
  as a `ReplyError`); the session must be `lifetime.KindAgent` with runner
  `claude` (`usage` "only a Claude agent can be attached" otherwise, also
  for a `ws` runner and a shell); `Path` from `exec.LookPath(o.app.claude
  path)` (`claude_missing`); `Args: ["attach", <session id from the record>]`;
  `Dir` the workspace's session dir; `Env` the agent's environment plus
  `TERM=xterm-256color`.
- **shell**: `Path` = `os.Executable()`; `Args`: `--config-dir <agent's
  CONFIG_DIR>`, `shell`, `--name <name>` when set, `--offline` when set,
  `--`, then specs; `Dir` the agent's home; `Env` as above plus
  `WS_ENVIRONMENT=<enrolled name>`, which T8's `ws shell` passes to
  `workspace.Create` as `Environment` (PR 3's D2: the shell workspace is
  version 4 like the others the agent makes; in-process operations set
  `App.environment` instead, `env_agent_ops.go`). Check: `--` precedes
  specs; a name starting with `-` was already refused by `Validate`.

### Tests

`TestAgentOperations_TerminalCommand` (`Test<Receiver>_<Method>`): "attach
uses the record's session id", "attach of a shell workspace is refused",
"attach of a ws runner is refused", "an unknown workspace is
workspace_not_found", "a shell's specs follow --", "a shell names the
agent's config dir".

### Verify

```
go test -p 2 -count=1 -run 'TestAgentOperations' ./internal/cli/
make lint
```

---

## T6. Controller: `OpenTerminal`, gaps, buffer, ping, stream liveness (`sonnet`)

- **Depends on:** T1d (pass, D17), T1b (`envproto.StreamClientID`, and the
  ACL the fake agent runs under), T2. **Parallel:** T3, T4, T5, T9, T10,
  T13.
- **Pinned values touched:** constraints-ui.md › Controller, Second
  connection.
- **Concurrency-heavy**: `c.mu` is never held across a broker call (the
  broker's handlers take it).
- **Amended after T1 (D16):** the controller's topics and subscription don't
  change (its in-process client sees frames from either agent connection
  alike); it adds the stream-liveness check and accepts an exit frame out
  of sequence. This is the whole controller side of the second connection,
  so it has no task of its own.

### Files

Create: `internal/environment/terminal.go`, `terminal_test.go`.
Change: `controller.go` (subscribe `ws/v1/env/+/stream/+/out` in
`subscribe`; end an environment's terminals in `onStatus` when it goes
offline and in `Close`), `call.go` (`admitCall` accepts `CallerTerminal`
only from `OpenTerminal`/`closeTerminal`; `Call` refuses it with
`ErrNotForwarded`, PR 3's error).

```go
// Terminal is one open remote terminal.
type Terminal struct{ /* env id, name, sid, out seq, in seq, buffer, done */ }

// OpenTerminal sends terminal.open to name and returns the terminal once
// the agent answered. Errors are Call's (OfflineError, OutdatedError, …) or
// the agent's reply error.
func (c *Controller) OpenTerminal(ctx context.Context, name string, args envproto.TerminalOpenArgs) (*Terminal, envproto.TerminalOpenResult, error)

// Output is the bytes to show, in order, markers included; it is closed
// when the terminal ends.
func (t *Terminal) Output() <-chan []byte
// Exit is the agent's exit frame, or a synthesized one ("disconnected")
// after Output closed because the environment went offline.
func (t *Terminal) Exit() envproto.TerminalExit
// Input publishes one input frame per ≤ MaxInputBytes slice of p at QoS 1,
// waiting for each publish.
func (t *Terminal) Input(ctx context.Context, p []byte) error
func (t *Terminal) Resize(ctx context.Context, cols, rows int) error
// Close sends a close frame and terminal.close; safe to call twice.
func (t *Terminal) Close(ctx context.Context) error
```

### Behaviour

- `onStreamOut(msg)`: parse the topic (env id, sid); unknown `(env id, sid)`
  → drop (debug log). Never block: frames go to the terminal's buffer
  (chunks + byte count, cap `MaxQueuedOutput`; over it drop the oldest, count
  bytes, and put `DroppedMarker` before the next chunk). `FrameExit` is
  checked **first, whatever its sequence number** (D16) → record it, close
  `Output`, unregister. Otherwise: a sequence gap → `LostMarker` into the
  buffer and a `FrameRedraw` (not more than once per `RedrawEvery`); a
  sequence ≤ last → drop. `FrameDropped` → `DroppedMarker`.
- A goroutine per terminal moves the buffer into the `Output` channel
  (unbuffered), so a slow consumer only fills the buffer.
- Ping: every `PingInterval`, first `streamConnected(envID)`; when false the
  terminal ends with `Exit{Reason: "disconnected"}` (D16); else a
  `FramePing`.

```go
// streamConnected reports whether environment id's stream connection is
// live: a peer in c.broker.Clients() with ClientID envproto.StreamClientID(id).
// Called without c.mu held.
func (c *Controller) streamConnected(id string) bool
```
- `onStatus` offline for an env → every terminal of it ends with
  `Exit{Reason: "disconnected"}`.

### Tests (`terminal_test.go`, with `envtest`'s broker and a fake agent)

`TestController_OpenTerminal`: "opens and streams output in order", "an
agent on protocol 1 is outdated", "an offline environment is
environment_offline", "output for an unknown sid is dropped", "output for a
sid of another environment is dropped", "a gap writes the lost marker and
asks one redraw", "a duplicate frame is dropped", "a dropped frame writes the
dropped marker", "a consumer that doesn't read loses the oldest output past
1 MiB and sees the marker", "the handler never blocks the broker" (the fake
publishes 200 frames while nobody reads `Output`; publishing finishes within
1 s), "going offline ends the terminal", "input is split into 4 KiB frames
with sequence numbers", "close sends a close frame and terminal.close",
"ping is sent every interval" (fake clock), "a missing stream connection
ends the terminal" (the fake agent's `<id>.t` client closes; the next ping
tick ends it with `disconnected`), "an exit frame ends the terminal whatever
its sequence" (an exit with seq 1 after data up to 5). The fake agent
connects twice to `envtest`'s broker, as `<id>` and `<id>.t`, and publishes
`out` frames only on the second. `TestController_Call`: "terminal.open
isn't sent through Call".

### Verify

```
go generate ./internal/environment/
go test -p 2 -count=1 -race ./internal/environment/
make lint
```

---

## T7. Daemon: the upgraded terminal route and client (`sonnet`)

- **Depends on:** T6. **Parallel:** T5, T9, T13.
- **Pinned values touched:** constraints-ui.md › Daemon.
- **Amended after T1:** no change. The second connection is below
  `environment.Terminal`; an agent that can't dial it answers
  `environment_offline` (D14), which the route already returns before the
  upgrade ("an offline environment answers environment_offline before
  upgrading" covers it).

### Files

Create: `internal/daemon/terminal.go`, `terminal_test.go`.
Change: `interfaces.go` (`EnvironmentHost` gains `OpenTerminal`; new
`TerminalStream`; regenerate), `environments.go` (`NopEnvironments.OpenTerminal`
→ `ErrEnvironmentUnknown`), `server.go` (route, open-terminal counter,
`idleExit`), `server_test.go`, `client.go` (`OpenTerminal`),
`internal/cli/env_daemon.go` (`envHost.OpenTerminal` adapts
`*environment.Terminal`), `internal/cli/env_daemon_test.go`
(`reloadRecorder` gains the method), `moq_test.go` (generated).

```go
// TerminalStream is an open terminal (environment.Terminal in ws).
type TerminalStream interface {
	Output() <-chan []byte
	Exit() envproto.TerminalExit
	Input(ctx context.Context, p []byte) error
	Resize(ctx context.Context, cols, rows int) error
	Close(ctx context.Context) error
}

// EnvironmentHost gains:
	OpenTerminal(ctx context.Context, name string, args envproto.TerminalOpenArgs) (TerminalStream, envproto.TerminalOpenResult, error)
```

**Route** `POST /v1/environments/{name}/terminal`: require `Connection:
Upgrade` and `Upgrade: ws-terminal.v1` (`usage`, 400, otherwise); body ≤
64 KiB, decoded strictly into `TerminalOpenArgs`; `OpenTerminal` with the
request's context plus `envproto.Grace`; an error → `writeCallFailure`
(same codes as calls); success → hijack, write `HTTP/1.1 101 Switching
Protocols\r\nConnection: Upgrade\r\nUpgrade: ws-terminal.v1\r\n\r\n`, then
the frames below. Count the terminal in `s.terminals` (atomic) until it
ends; `idleExit` returns false while it is above 0.

**Socket frames** (both directions): `[kind byte][uint32 big-endian
length][body]`, body ≤ 64 KiB, unknown kind ends the connection.

| Kind | Direction | Body |
| --- | --- | --- |
| `o` | daemon → CLI, first | `TerminalOpenResult` JSON |
| `d` | daemon → CLI | output bytes (markers included) |
| `x` | daemon → CLI, last | `TerminalExit` JSON |
| `i` | CLI → daemon | input bytes |
| `r` | CLI → daemon | `envproto.ResizeBody` |
| `c` | CLI → daemon | empty: close |

The daemon reads `i`/`r`/`c` in one goroutine (calling `Input` serially, so
QoS 1 acknowledgements push back on the socket) and writes `Output()` in
another; when either side ends, it calls `Close` and closes the connection.

**Client**:

```go
// TerminalConn is the CLI's side of an open terminal.
type TerminalConn struct{ /* rwc io.ReadWriteCloser, write mutex */ }

// OpenTerminal dials the socket and upgrades; on a non-101 answer it
// returns the RemoteError as Do does.
func (c *Client) OpenTerminal(ctx context.Context, name string, args envproto.TerminalOpenArgs) (*TerminalConn, envproto.TerminalOpenResult, error)

// Next returns the next output chunk, or the exit with io.EOF.
func (t *TerminalConn) Next() (data []byte, exit *envproto.TerminalExit, err error)
func (t *TerminalConn) Input(p []byte) error // ≤ MaxInputBytes per frame
func (t *TerminalConn) Resize(cols, rows int) error
func (t *TerminalConn) Close() error
```

### Tests

`TestServer_handleTerminal`: "upgrades and streams both ways" (fake
`TerminalStream`), "a request without the upgrade header is usage",
"an offline environment answers environment_offline before upgrading",
"closing the socket closes the terminal", "an unknown socket frame kind
closes the terminal", "the daemon doesn't exit idle while a terminal is
open", "is served through the peer listener". `TestServer_handleCallEnvironment`
gains "terminal.open isn't forwarded by call". `TestClient_OpenTerminal`:
"reads output and the exit", "a refused open returns the remote error".

### Verify

```
go generate ./internal/daemon/
go test -p 2 -count=1 -race ./internal/daemon/
go test -p 2 -count=1 -run 'Daemon|Env' ./internal/cli/
make lint
```

---

## T8. CLI: `ws attach` and `ws shell --env`

- **Depends on:** T7, T10 (both edit `app.go`, `llm.go`, `app_test.go`).
  **Parallel:** T13.
- **Pinned values touched:** constraints-ui.md › Commands and guides, Error
  codes (`terminal_limit`).

### Files

Create: `internal/cli/attach.go`, `attach_test.go`,
`internal/cli/remote_terminal.go`, `remote_terminal_test.go`,
`internal/cli/rawterm_linux.go`, `rawterm_darwin.go`, `rawterm_other.go`,
`internal/cli/guides/ws-attach.md`.
Change: `app.go` (register `attach` in the people group), `app_test.go`
("has every command" gains `attach`), `llm.go` (`guideTopicsBeforeReview`:
`"attach"` after `"shell"`), `shell.go` (`--env`), `guides/ws-shell.md`
(`--env`, the remote paragraph, `environment_offline`, `terminal_limit`),
`errors.go` (`codeTerminalLimit errorCode = "terminal_limit" // exit 4`
under `// From PR 4 (ADR 0074).`, in `exitCode` and `known`),
`errors_test.go`, `guides/ws.md` (exit-4 row gains `terminal_limit`; the
commands list gains `ws llm attach`), `interfaces.go` (add
`terminalOpener`; regenerate), `moq_test.go`.

```go
// terminalOpener opens a remote terminal through the daemon (daemon.Client).
type terminalOpener interface {
	OpenTerminal(ctx context.Context, name string, args envproto.TerminalOpenArgs) (remoteTerminal, envproto.TerminalOpenResult, error)
}
type remoteTerminal interface {
	Next() ([]byte, *envproto.TerminalExit, error)
	Input(p []byte) error
	Resize(cols, rows int) error
	Close() error
}

// runRemoteTerminal puts stdin in raw mode (restored on every return),
// sends the size and every SIGWINCH (coalesced to one per 100 ms), copies
// stdin to Input in ≤ 4 KiB frames paced at InputPaceBytes/s, writes
// output to stdout, and returns the exit. ctx's end closes the terminal.
func (a *App) runRemoteTerminal(ctx context.Context, env string, args envproto.TerminalOpenArgs) (envproto.TerminalExit, error)
```

- **`ws attach [--env <name>] <workspace>`** (`PreRunE: a.noJSON`; stdin
  must be a terminal, else `usage` "ws attach needs a terminal"):
  local → resolve the workspace, its Claude session (`usage` for a shell or
  a ws runner), then `a.claude.Attach` (exec, as `ws claude` does). With
  `--env` → `environment_unknown` for an unknown name, then
  `runRemoteTerminal` kind attach; exit 0 on `exited`/`closed`, otherwise
  `environment_offline` with "lost the terminal connection to linux-server;
  the agent keeps running" (amended after T1: `disconnected` now also means
  the stream connection dropped while the environment stayed online, D13,
  so the message no longer says "went offline"; D10's wording and
  `ws-attach.md` follow). Without `--env`, an id not found locally but in exactly one
  snapshot → `workspace_not_found` "it is on <name>; pass --env <name>".
- **`ws shell --env <name> [--name] [--offline] [<SPEC>...]`**: kind shell;
  exit with the exit frame's code (`shellExitError`), 0 when null after a
  close. Without `--env`, unchanged, except that a `WS_ENVIRONMENT` holding a
  valid environment name (`^[a-z][a-z0-9-]{0,31}$`, not `local`) becomes
  `CreateOptions.Environment` (T5 sets it when the agent runs `ws shell`;
  documented in `ws-shell.md` as set by the environment agent, not for
  people). An invalid value is ignored with a warning.
- Raw mode: `unix.IoctlGetTermios`/`IoctlSetTermios` with `TCGETS`/`TCSETS`
  (linux) and `TIOCGETA`/`TIOCSETA` (darwin), `cfmakeraw` by hand; size by
  `unix.IoctlGetWinsize(fd, TIOCGWINSZ)`. `rawterm_other.go` refuses with
  `usage`.

### Tests

`TestApp_runAttach`: "a local Claude workspace execs claude attach", "a
shell workspace is refused", "stdin that isn't a terminal is refused",
"--env opens an attach terminal", "an unknown environment is
environment_unknown", "a remote id without --env names its environment".
`TestApp_runRemoteTerminal` (fake `terminalOpener`, pipes for stdin/stdout,
`isTerminal` and raw-mode seams on `App`): "copies output to stdout",
"sends stdin in frames of at most 4 KiB", "paces a large paste" (fake
clock), "sends a resize on SIGWINCH" (a test hook sends to the signal
channel), "restores the terminal on exit", "a disconnected exit is
environment_offline". `TestApp_runShell` gains "WS_ENVIRONMENT stamps the
workspace's environment", "--env runs a remote shell
and exits with its code". `TestErrorCode_exitCode` gains `terminal_limit`.
`TestGuideTopics` passes (guide, flag, code rows).

### Verify

```
go generate ./internal/cli/
go test -p 2 -count=1 -run 'Attach|Shell|RemoteTerminal|ErrorCode|GuideTopics|newRootCmd' ./internal/cli/
go test -p 2 -count=1 ./internal/cli/
make lint
```

---

## T9. `ws list`: every environment by default, `agent_state`

- **Depends on:** nothing. **Parallel:** T1-T7, T13 (not T10: both are
  heavy `internal/cli` tasks; run one at a time).
- **Pinned values touched:** constraints-ui.md › `ws list` JSON and text.

### Files

Change: `internal/cli/list.go`, `list_env.go`, `list_test.go`,
`list_env_test.go`, `guides/ws-list.md`, `docs/tools/ws-list.md`.

- `selectEnvironments(nil)` → `local`, then every **enrolled** environment
  in name order (pending ones have no snapshot). With `-q`/`--ids` and no
  `--env`: local only (D9). With `--parent P` and no `--env`: the
  environment where `P` is (local first; else exactly one snapshot; else
  `workspace_not_found`), replacing today's "`--parent` takes one `--env`"
  error for this case.
- `environmentPresence` is called only when the store lists an enrolled
  environment (no daemon dial otherwise).
- `listEntryJSON` gains `AgentState *string \`json:"agent_state"\`` (not
  `listWorkspaceJSON`: `ws machine children` must not change). Remote: the
  snapshot agent whose `session_id` is the entry's `session.id`, its
  `state`; else `null`. Local: `null`.
- Text output: unchanged columns (the `ENV` column already appears only with
  a remote entry).

### Tests

`list_test.go` "--json" golden: each entry gains `"agent_state":null`.
`list_env_test.go`: "plain ws list lists every enrolled environment after
local", "a pending environment adds nothing", "-q lists local ids only",
"-q with --env lists that environment", "--parent finds a remote parent's
environment", "agent_state comes from the snapshot's agent", "no daemon is
dialled with no environment" (the `daemonPaths` seam points at a socket that
fails the test if dialled).

### Verify

```
go test -p 2 -count=1 -run 'List|Env' ./internal/cli/
go test -p 2 -count=1 ./internal/cli/
make lint
```

---

## T10. CLI: `--env` on `rm`, `rename`, `hide`, `unhide`; `ws stop`

- **Depends on:** nothing. **Parallel:** T1-T7, T13.
- **Pinned values touched:** constraints-ui.md › Commands and guides.

### Files

Create: `internal/cli/stop.go`, `stop_test.go`, `internal/cli/remote_ops.go`,
`remote_ops_test.go`, `internal/cli/guides/ws-stop.md`, `docs/tools/ws-stop.md`.
Change: `rm.go`, `rename.go`, `hide.go` (both commands), their tests, their
guides (`ws-rm.md`, `ws-rename.md`, `ws-hide.md`, `ws-unhide.md`: the flag
row and an example), `app.go`, `app_test.go` (`stop`), `llm.go`
(`"stop"` after `"rm"`), `guides/ws.md` (commands list), `docs/tools/README.md`
(row for `ws stop`), `README.md` command table.

```go
// remoteWorkspaceOp runs op on env for each id, in order, and prints each
// result with "environment" added (PR 3's D9); it stops at the first error.
func (a *App) remoteWorkspaceOp(ctx context.Context, env, op string, argsFor func(id string) any, ids []string) error
```

- `ws rm --env <name> [--keep-children] <id>...` → `workspace.rm`
  `{workspace, keep_children}`; `--dry-run` with `--env` is `usage` (the
  agent has no dry run).
- `ws rename --env <name> <id> <new-name>` → `workspace.rename`.
- `ws hide --env` / `ws unhide --env` → `workspace.hide` `{hidden}`.
- `ws stop [--env <name>] <workspace>...`: local → the workspace's Claude
  session, `a.claude.Stop` (`claude.ErrNoSession` → `stopped: false`);
  remote → `agent.stop`. JSON: `{"stopped": [{"workspace", "session",
  "stopped", "environment"}]}` (`environment` `"local"` for local ones);
  text: one line per workspace.
- A remote id given without `--env` → `workspace_not_found` "it is on
  <name>; pass --env <name>" when exactly one snapshot has it (shared helper
  with T8: `a.remoteHint(id)` in `remote_ops.go`).

### Tests

`TestApp_remoteWorkspaceOp` ("calls once per id", "adds environment to each
result", "stops at the first error"), `TestApp_runRm` gains "--env removes
through workspace.rm", "--env with --dry-run is usage"; same pattern for
rename, hide, unhide; `TestApp_runStop`: "stops a local agent", "a local
workspace without a session answers stopped false", "--env stops through
agent.stop", "a remote id without --env names its environment".
`TestApp_newRootCmd` "has every command" gains `stop`.

### Verify

```
go generate ./internal/cli/
go test -p 2 -count=1 -run 'Rm|Rename|Hide|Stop|RemoteWorkspaceOp|GuideTopics|newRootCmd' ./internal/cli/
go test -p 2 -count=1 ./internal/cli/
make lint
```

---

## T11. Conformance: terminals over the real broker (`sonnet`)

- **Depends on:** T5, T8. **Parallel:** T12-T17.
- **Pinned values touched:** constraints-ui.md › Broker and streams.

### Files

Change: `internal/mqttbroker/conformance_test.go` (new subtests; the real
broker, TLS, a real agent with `internal/cli`'s operations, and
`ptyproc`).

`TestConformance_terminals`: "a shell terminal echoes input" (open kind
shell with no specs through `Controller.OpenTerminal`; write `echo hi\n`;
read `hi`), "an attach terminal runs the command the agent chose" (the
`TerminalCommand` resolves to `/bin/cat` through a test `claude` path
pointing at a script that `exec cat`), "a slow reader sees the dropped
marker, not a hang" (`yes | head -c 4000000` in the shell; read nothing for
2 s; then read: the marker appears and the stream continues), "killing the
agent's connection ends the terminal with disconnected within 15 s",
"another environment's agent can't publish on this terminal's out topic"
(it is disconnected by the ACL, PR 2), "a ninth terminal is terminal_limit".

Amended after T1 (the agent here gets a second `mqttclient` through
`SetStreamClient`, as `ws env agent run` does after T1c; the stream
connection goes through its own `killableProxy` so it can be killed alone):
"opening a terminal opens the stream connection" (`broker.Clients()` lists
`<id>.t` after the open and not before), "killing the stream connection ends
the terminal with disconnected within 20 s and the environment stays
online" (D16's ping tick plus a round trip), "killing the control
connection closes the stream connection" (`<id>.t` leaves `Clients()`
within 5 s), "the stream connection closes after the last terminal and the
idle time" (`StreamIdle` set to 1 s), "a request is answered while a shell
floods output" (a functional check, `yes` in the shell, `workspace.list`
within 5 s on loopback; the latency number is T1d's, not this test's).

### Verify

```
go test -p 2 -count=1 -race -run 'TestConformance' ./internal/mqttbroker/
make lint
```

---

## T12. Desktop backend: keyed entries, `--env` routing, remote terminals

- **Depends on:** T8, T9, T10 (the CLI it runs). **Parallel:** T11.
- **Pinned values touched:** constraints-ui.md › Desktop backend.

### Files

Change: `desktop/src-tauri/src/model.rs`, `commands.rs`, `pty.rs`,
`online/dispatch.rs` (arguments of the changed commands), `tray/attention.rs`,
`tray/watcher.rs`, `reviews/runs.rs`, and their `mod tests`;
`desktop/test/tauri-mock.js` (sample remote entries: `linux-server` tree of
two, `win-rig` one offline).

- `WsWorkspace` gains `#[serde(default)] environment: Option<String>`,
  `environment_status: Option<String>`, `stale: Option<bool>` (top level),
  `agent_state: Option<String>`.
- `Entry` gains `environment: String` (`"local"` when absent),
  `environment_online: bool`, `snapshot_stale: bool` (serialized
  `environment`, `environmentOnline`, `snapshotStale`). Key: unchanged for
  local; `env:<name>:<id>` for remote.
- Remote status: `remote_agent_status(ws_state, agent_state)`: `working` →
  Working; `idle` + `blocked` → NeedsInput; `idle` + `done` → Done; else as
  `agent_status` without a job. No job file is read for a remote entry.
- `NewAgent` gains `environment: Option<String>`; `new_agent_args` adds
  `--env <name>` after `claude` when it is set and not `local`.
- `remove_entry(workspace_id, session_id, keep_children, environment)`,
  `rename_workspace(workspace_id, name, environment)`,
  `set_workspace_hidden(workspace_id, hidden, environment)`,
  `stop_agent(session_id, workspace_id, environment)`: pure `*_args`
  builders put `--env <name>` before `--`; `stop_agent` with a remote
  environment runs `ws stop --env <name> -- <id>`, else `claude stop`
  unchanged. `environment` is `Option<String>` so old frontends' calls still
  decode (`#[serde(default)]` in the dispatch `args!`).
- `TermSpec::Attach { session_id, cwd, environment: Option<String>,
  workspace_id: Option<String> }` → remote: `("ws", ["attach", "--env",
  env, "--", workspace_id])`, no cwd; `TermSpec::Shell { name, specs,
  environment }` → remote: `["shell", "--env", env, ("--name", n)?, "--",
  specs...]`. A remote attach without `workspace_id` is `bad_args`.
- Tray and watcher: an entry with `environment != "local"` and (`!
  environment_online` or `snapshot_stale`) never counts and never starts an
  event; its previous state is kept so it doesn't fire on recovery.
- `reviews/runs.rs`: join only entries with `environment == "local"`.

### Tests (Rust `mod tests`)

`model.rs`: `remote_entry_key_has_environment`, `local_entry_key_is_unchanged`,
`remote_blocked_agent_needs_input`, `remote_entry_reads_no_job`,
`missing_environment_is_local`. `commands.rs`:
`new_agent_args_adds_env`, `rm_args_with_env`, `set_hidden_args_with_env`,
`rename_args_with_env`, `stop_args_remote_uses_ws_stop`. `pty.rs`:
`remote_attach_runs_ws_attach`, `remote_shell_runs_ws_shell_env`,
`remote_attach_without_workspace_is_bad_args`. `tray`:
`offline_remote_entry_does_not_count`, `stale_remote_entry_does_not_notify`,
`remote_needs_input_notifies`. `reviews/runs.rs`:
`remote_review_workspace_is_not_joined`. `online/dispatch.rs`:
`remove_entry_accepts_environment`, `remove_entry_without_environment_is_local`.

### Verify

```
cd desktop/src-tauri && cargo test -j 2
cd desktop/src-tauri && cargo clippy --all-targets -- -D warnings
cd desktop/src-tauri && cargo test -j 2 --no-default-features --features online
cd desktop/src-tauri && cargo clippy --no-default-features --features online --all-targets -- -D warnings
cd desktop/src-tauri && cargo fmt --check
```

(`cargo` builds Tauri's GUI crates; on a pod without the system libraries
(`webkit2gtk`, `libsoup`), run the `online` feature commands, which need
none, and list the `desktop` feature ones as unverified.)

---

## T13. Desktop backend: environment commands (`small`)

- **Depends on:** nothing (the `ws env` commands exist). **Parallel:**
  T1-T11.
- **Pinned values touched:** constraints-ui.md › Desktop command lists.

### Files

Create: `desktop/src-tauri/src/environments.rs`.
Change: `lib.rs` (`mod environments;` and six names at the end of
`generate_handler!`, after `stats::stats_get`), `online/dispatch.rs`
(`COMMANDS` gains the six at the end; six `dispatch` arms), `desktop/test/tauri-mock.js`
(handlers with sample data, D7).

```rust
#[tauri::command] pub async fn environments_list(state) -> CmdResult<EnvironmentListing>          // ws env list --json
#[tauri::command] pub async fn environment_add(state, name: String) -> CmdResult<EnrollmentCode>  // ws env add --json -- <name>
#[tauri::command] pub async fn environment_rotate(state, name: String) -> CmdResult<EnrollmentCode> // ws env rotate --json -- <name>
#[tauri::command] pub async fn environment_remove(state, name: String) -> CmdResult<EnvironmentRemoved> // ws env rm --json -- <name>
#[tauri::command] pub async fn environments_ca_status(state) -> CmdResult<CaStatus>              // ws env ca status --json
#[tauri::command] pub async fn environments_ca_rotate(state, finish: bool, force: bool) -> CmdResult<CaRotated> // ws env ca rotate --json [--finish [--force]]
```

Structs deserialize `ws`'s `snake_case` JSON and serialize `camelCase`
(`RemoteEnvironment { id, name, state, status, last_seen, created_at,
enrolled_at, cert_not_after, cert_ca, hello: Option<EnvHello>, workspaces }`,
`EnvHello { ws_version, os, arch, claude, claude_version, cert_not_after }`,
`EnrollmentCode { id, name, state, code, ca_created: Option<bool>,
revoked_serials: Vec<String>, warnings }`, `EnvironmentRemoved { id, name,
revoked_serials, warnings }`, `CaInfo { fingerprint, not_after }`, `CaStatus
{ current, old, environments_on_current, environments_on_old, warnings }`,
`CaRotated { current, old, dropped, stranded, warnings }`). Pure
`*_args` builders with `--` before names.

### Tests

`env_add_args_put_the_name_after_dashes`, `ca_rotate_args_finish_force`,
`environments_list_decodes_ws_output` (fake `ws` script, as in
`commands.rs` tests), `environment_add_result_is_not_kept` (after the call,
`AppState` has no field holding it: assert by a `Debug` print of the state
not containing the code), `dispatch::commands_match_generate_handler` and
`every_command_is_handled` pass with 87 names.

### Verify

As T12.

---

## T14. Frontend: types, `environmentsSlice`, polling, tree keys

- **Depends on:** T12, T13. **Parallel:** T11.
- **Pinned values touched:** constraints-ui.md › Frontend.

### Files

Create: `desktop/src/features/environments/environmentsSlice.ts`,
`environmentsSlice.test.ts`, `useEnvironmentsPolling.ts`,
`useEnvironmentsPolling.test.tsx`, `environments.ts` (selectors and
`offlineReason`), `environments.test.ts`.
Change: `src/api.ts`, `src/store.ts` (`environments` reducer),
`src/App.tsx` (start polling), `src/test/fixtures.ts` (`entry()` gains
`environment: "local", environmentOnline: true, snapshotStale: false`; new
`remoteEnvironment()`), `src/features/agents/tree.ts` and `tree.test.ts`
(`parentOf` checks the environment), `desktop/test/tauri-mock.js` if T13
missed a handler.

```ts
// api.ts additions
export type EnvironmentState = "pending" | "enrolled";
export type EnvironmentStatus = "pending" | "online" | "offline" | "unknown";
export interface EnvironmentHello { readonly wsVersion: string; readonly os: string; readonly arch: string; readonly claude: "present" | "missing"; readonly claudeVersion: string | null; readonly certNotAfter: string }
export interface RemoteEnvironment { readonly id: string; readonly name: string; readonly state: EnvironmentState; readonly status: EnvironmentStatus; readonly lastSeen: string | null; readonly createdAt: string; readonly enrolledAt: string | null; readonly certNotAfter: string | null; readonly certCa: "current" | "old" | null; readonly hello: EnvironmentHello | null; readonly workspaces: number }
export interface EnvironmentListing { readonly environments: readonly RemoteEnvironment[]; readonly warnings: readonly string[] }
export interface EnrollmentCode { readonly id: string; readonly name: string; readonly code: string; readonly caCreated: boolean | null; readonly revokedSerials: readonly string[]; readonly warnings: readonly string[] }
export interface EnvironmentRemoved { readonly id: string; readonly name: string; readonly revokedSerials: readonly string[]; readonly warnings: readonly string[] }
export interface CaInfo { readonly fingerprint: string; readonly notAfter: string }
export interface CaStatus { readonly current: CaInfo | null; readonly old: CaInfo | null; readonly environmentsOnCurrent: number; readonly environmentsOnOld: number; readonly warnings: readonly string[] }
export interface CaRotated { readonly current: CaInfo | null; readonly old: CaInfo | null; readonly dropped: CaInfo | null; readonly stranded: readonly string[]; readonly warnings: readonly string[] }
export type ConfigType = "string" | "strings" | "duration" | "bool" | "hosts" | "int";
// Entry gains: environment: string; environmentOnline: boolean; snapshotStale: boolean
// NewAgent gains: readonly environment?: string
// TermSpec attach gains: readonly environment?: string; readonly workspaceId?: string; shell gains readonly environment?: string
// api gains: environmentsList, environmentAdd(name), environmentRotate(name), environmentRemove(name),
//   environmentsCaStatus(), environmentsCaRotate(finish, force);
//   removeEntry/renameWorkspace/setWorkspaceHidden/stopAgent gain an `environment` argument (last, default "local").
```

`environmentsSlice` state: `{ items: readonly RemoteEnvironment[];
loadedAt: number | null; error: string | null; lastChoice: string }`;
thunks `fetchEnvironments`, `removeEnvironment`, `rotateCa`; `lastChoice`
read from and written to `localStorage["ws.environment.last"]` inside
`try/catch` (no code is ever stored). The enrollment code is **not** in the
slice (D5): `environmentAdd`/`environmentRotate` are called from the dialog
directly through `api`.

`useEnvironmentsPolling(intervalMs = 10_000)`: fetch at once, then every
interval; `refresh()` returned for actions.

`environments.ts`: `selectEnvironmentChoices(state)` → `[{name: "local",
disabled: false}, …enrolled, each {name, disabled, reason}]`;
`offlineReason(env, now)` → `"win-rig is offline (last seen 14:02)"`,
`"… (the ws daemon isn't running)"` for `unknown`; `statusIsOld(loadedAt,
now)` (> 30 s).

### Tests

Reducer tests for each case; `useEnvironmentsPolling` with fake timers
("fetches every 10 s", "refresh fetches at once"); `environments.test.ts`
(choices, reasons); `tree.test.ts` gains "a child is found only on its
parent's environment" (a local and a remote entry with the same id).
`npm run typecheck` passes with the new `Entry` fields across every fixture.

### Verify

```
cd desktop && npm ci
cd desktop && npm run typecheck
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/environments src/features/agents/tree.test.ts
cd desktop && npm test -- --maxWorkers=2 --testTimeout=60000
```

---

## T15. Settings › Environments

- **Depends on:** T14. **Parallel:** T16, T17.
- **Pinned values touched:** constraints-ui.md › Frontend (Settings groups).

### Files

Create in `desktop/src/features/settings/`: `EnvironmentsGroup.tsx`,
`EnvironmentsGroup.module.css`, `EnvironmentsGroup.test.tsx`,
`AddEnvironmentDialog.tsx`, `EnrollmentCode.tsx` (the code and the two
commands, used by Add and Rotate), `EnrollmentCode.module.css`,
`AddEnvironmentDialog.test.tsx`, `RemoveEnvironmentDialog.tsx`,
`CaCard.tsx`, `CaCard.test.tsx`, `EnvironmentSettingsCard.tsx` (the four
`[environments]` keys), `EnvironmentSettingsCard.test.tsx`.
Change: `settingsSlice.ts` (`SettingsGroup` gains `"environments"`),
`SettingsList.tsx` (`{ id: "environments", title: "Environments" }` after
`"workspaces"`), `SettingsPane.tsx` (render the group).

- **List** (wireframe in [README.md](README.md)): `local` first ("this
  machine"); each environment with a dot (● online, ○ offline, ◌ pending),
  status text (`online`, `offline since 14:02 (last seen)`, `waiting for
  enrollment`), `ws <version>`, `<os>/<arch>`, Claude (`claude 2.4` /
  "claude: missing" with "Install Claude Code on this machine, then try
  again."), `<n> workspaces`; buttons Rotate and Remove (Cancel for a
  pending one = Remove). Warnings from the listing above it.
- **Add environment**: a name field (validated as `ws env add` does:
  `^[a-z][a-z0-9-]{0,31}$`, not `local`; the server's
  `name_invalid` shown under the field), then `EnrollmentCode`: the code in
  a read-only monospace box, and the two commands `ws env agent enroll
  <code>` and `ws env agent run`, each with Copy (hidden with "select the
  text to copy it" when `navigator.clipboard` is unavailable), the clipboard
  note from D5, and Done. The code lives in this component's `useState` and
  is cleared on close. `usage` from a missing `public_name` shows "Set
  Public name below first."
- **Rotate**: confirm ("The old code or certificate stops working"), then
  the same `EnrollmentCode`.
- **Remove**: confirm naming the environment and its workspace count.
- **CA card**: fingerprint (first 16 hex, Copy for the whole), "expires
  <date>", the old CA during a rotation with "<n> environments still on the
  old CA"; Rotate CA (confirm), Finish rotation (disabled with "<n>
  environments still use the old CA" while `environmentsOnOld > 0`, with a
  Force checkbox that shows `stranded`); every `warnings` line (the
  near-expiry warning comes from `ws`, `renew_before_days`).
- **Settings card**: Public name, Listen, Enroll listen (strings), Renew
  before (days, `int`), each saved with `config_set` on blur as the other
  groups do, showing defaults (`0.0.0.0:8883`, `0.0.0.0:8884`, 30) when
  `config_list` doesn't list the key (it lists them only when
  `[environments]` exists). "Changes apply when the ws daemon reloads; the
  desktop restarts it" and a Restart daemon button (`daemon_restart`).

### Tests

`EnvironmentsGroup.test.tsx`: "lists local first and each environment with
its status", "shows the claude missing hint", "an offline row says when it
was last seen", "a pending row offers Cancel". `AddEnvironmentDialog.test.tsx`:
"shows the code and the two commands once", "closing forgets the code"
(reopen shows the name field), "the code is not in the store" (assert
`JSON.stringify(store.getState())` doesn't contain it), "a missing public
name points to the setting". `CaCard.test.tsx`: "Finish is disabled while
environments use the old CA", "shows warnings". `EnvironmentSettingsCard.test.tsx`:
"saves renew_before_days as an int", "shows defaults when unset".

### Verify

```
cd desktop && npm run typecheck
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/settings
cd desktop && npm test -- --maxWorkers=2 --testTimeout=60000
```

---

## T16. Environment picker: New agent and New shell (`small`)

- **Depends on:** T14. **Parallel:** T15.
- **Pinned values touched:** constraints-ui.md › Frontend.

### Files

Create: `desktop/src/features/create/EnvironmentField.tsx`,
`EnvironmentField.test.tsx`.
Change: `NewAgentDialog.tsx`, `NewAgentDialog.test.tsx`, `NewShellDialog.tsx`,
`desktop/src/features/terminals/terminalsSlice.ts` (a shell spec carries
`environment`).

- `EnvironmentField`: a select labelled "Environment" with
  `selectEnvironmentChoices`; disabled options show "(offline since 14:02)";
  hidden entirely when no environment is enrolled (local-only users see no
  change). Starts at `lastChoice` when it is enabled, else `local` with the
  note "linux-server is offline; using local". On submit, `lastChoice` is
  saved.
- New agent sends `environment` (omitted for `local`); New shell's
  `TermSpec` gets `environment`.
- When the chosen environment's hello says `claude: "missing"`, New agent
  shows "claude isn't installed on linux-server." and disables Start.

### Tests

"the picker is hidden without environments", "an offline environment is
disabled with its reason", "remembers the last choice", "an offline
remembered choice falls back to local", "sends environment with the new
agent", "a missing claude disables Start".

### Verify

```
cd desktop && npm run typecheck
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/create
cd desktop && npm test -- --maxWorkers=2 --testTimeout=60000
```

---

## T17. Agents list: badges, filter, offline, pane

- **Depends on:** T16. **Parallel:** T15.
- **Pinned values touched:** constraints-ui.md › Frontend.

### Files

Create: `desktop/src/features/agents/EnvBadge.tsx`, `EnvBadge.module.css`,
`EnvFilter.tsx`, `EnvFilter.module.css`, `EnvFilter.test.tsx`,
`remote.ts` (`entryActions(entry, env, now)`: each action's `enabled` and
`reason`), `remote.test.ts`.
Change: `EntryRow.tsx` (badge unless local; dimmed + `offline`/`stale`
badge), `EntryRow.test.tsx`, `FilterBar.tsx` (the chip row, only with ≥ 1
environment), `FilterBar.test.tsx`, `agentsSlice.ts` (`environmentFilter:
string | null`, `environmentFilterChanged`; `filtersCleared` clears it),
`agentsSlice.test.ts`, `Sidebar.tsx` (apply the filter before the tree),
`EntryPane.tsx` (disabled actions with reasons from `remote.ts`; the
remote-conversation note; Terminal default; "Runs on linux-server, like its
parent" for a child; attach spec with `environment` and `workspaceId`),
`EntryPane.test.tsx`, `AgentsActions.tsx` (Restore stopped label),
`ChildrenCard.tsx` (badge on remote children).

Thunks `removeEntry`, `renameEntry`, `setEntryHidden`, `stopAgent` pass the
entry's `environment`. `runner_stop`, `workspace_open`, `terminal_open` kind
`dir` and `read_transcript` are not called for a remote entry (D3).

### Tests

`remote.test.ts`: every row of D3's "remote" column (enabled, or disabled
with the exact reason) for an online, an offline and a stale remote entry.
`EntryRow.test.tsx`: "a remote entry shows its environment badge", "a local
entry has none", "an offline entry is dimmed with offline". `EnvFilter.test.tsx`:
"lists local and each environment with its dot", "choosing one filters the
list". `EntryPane.test.tsx`: "a remote agent's actions are disabled with the
reason when offline", "Open folder is disabled for a remote entry", "the
conversation of a remote agent shows the note and opens the terminal",
"Stop on a remote agent passes its environment", "a remote child says it
runs on its parent's environment".

### Verify

```
cd desktop && npm run typecheck
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/agents
cd desktop && npm test -- --maxWorkers=2 --testTimeout=60000
```

---

## T18. Docs and ADR amendments

- **Depends on:** T11, T15, T17 (and the screenshots, which the
  orchestrator's child commits before T18 merges). **Parallel:** none.
- **Pinned values touched:** constraints-ui.md › Docs with lists.

### Files

Change: `docs/adrs/0072-remote-environments.md`, `0073-…`, `0074-…`,
`0076-…`, `0014-command-set.md`, `0016-output-contract.md` (Status lines and
Amendments per D8), **`0075-…`**, and the second-connection amendments of
0073, 0074, 0075 and 0076 per **D18** (with T1d's measured number),
`docs/adrs/README.md` (0076 → Accepted),
`docs/tools/ws-env.md` (Terminals section: frames, limits, lifetime;
protocol 2; the stream connection: client id `<id>.t`, opened with the first
terminal, closed 60 s after the last, no will, nothing new for a firewall
since it uses the same `listen` port; lines 91 and 122 per D18), `docs/tools/ws-shell.md`, `docs/tools/ws-list.md`,
`docs/tools/ws-rm.md`, `ws-rename.md`, `ws-hide.md` (`--env`), new
`docs/tools/ws-attach.md`, `docs/tools/README.md` (row), `README.md`
(roadmap: "Remote environments, terminals and desktop" checked; command
table rows for `ws attach`, `ws stop`), `desktop/README.md` (an
"Environments" bullet and a section with the D7 screenshots),
`docs/online.md` (the Environments screenshot and the code note of D5),
`docs/design/remote-environments/README.md` (link this file; the stale "PR
4" notes), `docs/state.md` (nothing new on disk on the controller: say so in
the snapshot paragraph, and `agent_state`), `internal/cli/guides/ws.md`
(remote environments paragraph: `ws attach`, `ws stop`, `--env`).
Record follow-ups: merging `internal/claude`'s pty into `internal/ptyproc`;
T1d's untested cases (a real WAN with loss and bufferbloat, many agents);
an event stream for presence instead of polling; remote conversation view;
remote runner stop.

### Verify

```
go test -p 2 -count=1 -run 'TestGuideTopics' ./internal/cli/
make lint
grep -n "\.t\b\|stream connection" docs/adrs/0073-environment-agent-is-ws.md docs/adrs/0074-mqtt-transport-embedded-broker.md docs/adrs/0075-environment-enrollment-and-certificates.md docs/tools/ws-env.md
grep -rn "settings-environments.png\|new-agent-environment.png\|agents-environments.png\|remote-terminal.png" desktop/README.md docs/online.md
ls desktop/docs/settings-environments.png desktop/docs/settings-environments-add.png desktop/docs/settings-environments-mobile.png desktop/docs/new-agent-environment.png desktop/docs/agents-environments.png desktop/docs/agents-environments-mobile.png desktop/docs/remote-terminal.png
```

---

## Screenshots (the orchestrator's Sonnet child, after T17)

Not an implementer task; step 4 of the multi-agent procedure. Run Vite
(`cd desktop && npx vite --port 1420`, another port if taken) with Node from
"Rules for every task", load `test/tauri-mock.js` (and `test/web-mock.js`
for the mobile shots, `window.__WEB_MOCK__.authenticated = true`), click
through with `page.getByRole`, and save exactly D7's seven files under
`desktop/docs/`. Desktop width 1280×800; mobile 360×740. Layout checks to
report by name: no clipped inputs (the Add dialog's code box and both
command rows), no overlapping fields, no horizontal scroll at 360 px, tap
targets ≥ 40 px at 360 px (Copy buttons, filter chips, Rotate/Remove),
badges don't push the row's status off-screen; computed styles checked with
`getComputedStyle` in the browser. Stop Vite and delete `.playwright-mcp/`
afterwards. A layout bug becomes a fix task on the same branch.
