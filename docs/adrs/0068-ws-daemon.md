# 0068. `ws daemon`: one long-lived process per config directory supervises runners and gives them their environment

- Status: Proposed
- Date: 2026-11-14; revised 2026-11-14 after the second review (the
  desktop app hosts the daemon; `secrets.env` the only secrets store; a
  Stop for one runner)

## Context

### Problem

The first draft of [0063](0063-runner-children-are-supervised-sessions.md)
started each runner as a detached process (`setsid`) and kept only files
about it. The review asked: if these processes are detached, how are they
maintained? Doesn't `ws` need a long-lived process, one that also provides
environment variables and other runtime configuration?

Honestly, in that draft nothing maintained them. A runner that crashed was
`stopped` until a person ran `ws restore`. A reboot or a pod restart
killed every runner, also until `ws restore`. And a runner's environment,
its endpoint token, `GH_TOKEN`, and the variables its MCP servers need, was
whatever the agent that ran `ws machine spawn` happened to have: Claude
Code's background service's environment, frozen when that service started
([0018](0018-restart-claude-daemon.md)). Which process supervises runners,
what happens on crash, reboot and pod restart, and who provides their
environment, configuration and secrets?

### What a solution needs

1. **Supervised**: a crashed runner is restarted or failed with a reason,
   never left silent; after a reboot or pod restart, unfinished runners
   resume without a person.
2. **A defined environment**: a runner's variables don't depend on which
   agent spawned it, and secrets come from one place that isn't
   `config.toml`.
3. **Small**: one process, nothing per child, no OS service manager
   required, and the same on macOS and in the unprivileged ws-online pod.
4. **Existing commands keep working**: `ws list`, `ws machine wait` and
   `children` keep reading files; Claude sessions don't change.
5. **Safe**: only the user can reach it, and it can't be asked to run an
   arbitrary command.

### Relevant facts

How `ws` runs long-lived work today:

- **Claude sessions** are supervised by Claude Code's own service, `claude
  daemon run`. `claude --bg` starts it on demand ("transient — started
  on-demand by `claude`"), it runs each session in a pty host, and it keeps
  the environment it started with; `ws restart` replaces it to refresh that
  environment[^claude-daemon][^ws-restart]. In the ws-online pod, measured
  2026-11-14: PID 1 is `tini`, its child is `ws-online serve`, and the
  Claude service is re-parented to PID 1, with its socket under
  `/tmp/cc-daemon-1000/`; each session's MCP stdio servers run as
  children of that session[^measured-ps].
- **Loops**: "Nothing in `ws` runs in the background." `ws loop tick` is
  run every 60 s by a launchd agent, by `--watch`, or by a task in the
  desktop app's Rust backend while it's open[^loop]. `ws-online` runs the
  same task, plus the auto-review checker and the push watcher, for as long
  as the container runs[^online].
- **After a reboot**, `ws restore` (a person, or the desktop's Restore) runs
  `claude respawn` for each stopped session ([0015](0015-workspace-lifetime-follows-session.md)).
- The desktop app (Tauri, Rust) already runs background work while it's
  open: `ws loop tick` every 60 s, the review checker and the tray watcher
  ([0048](0048-agent-loop-scheduler.md), [0050](0050-desktop-tray-notifications.md)).
  Closing its window hides it and it keeps running in the tray; only Quit
  (tray or ⌘Q) ends it. An app opened from Finder or the Dock gets
  launchd's minimal environment, and the app resolves the user's
  login-shell `PATH` itself (`desktop/src-tauri/src/env.rs`)[^desktop].
  `ws-online` is the same backend built with the `online` feature.
- `ws-online`'s environment comes from the container (`GH_TOKEN`,
  `CLAUDE_CODE_OAUTH_TOKEN`, …); `/home/ws` and `/data` are persistent
  volumes, `/tmp` isn't[^online-env][^pod].
- `internal/lockfile` already takes `flock` locks in `CONFIG_DIR`, which is
  on NFS in the pod (`loops/.lock`)[^lockfile].
- Claude Code's service can't run other programs: its interface is
  `claude --bg`, `agents`, `respawn`, `stop`, `daemon status|stop|run`.

## Decision

We will add **`ws daemon`**: one long-lived `ws` process per config
directory that starts, watches, restarts and stops runners, and builds
their environment. It does nothing else in this design.

**Lifecycle** (for people, the desktop app, `ws-online` and service
managers; agents never run them):

```
ws daemon run [--origin transient|managed|app]   # foreground; exits on SIGTERM
ws daemon status [--json]
ws daemon stop [--keep-runners] [--json]
```

- `run` takes the single-instance lock `CONFIG_DIR/daemon/lock`
  (`internal/lockfile`, exclusive, not waiting). If another daemon holds
  it: `daemon_running` (exit 4). It writes `CONFIG_DIR/daemon/daemon.json`
  (`version`, `pid`, `started_at`, `origin`, `socket`, `ws_version`), logs
  to `CONFIG_DIR/daemon/daemon.log` (`slog` JSON), and listens on its
  socket.
- `status` asks the socket, and falls back to `daemon.json` and the lock to
  say `running: false` with the last pid. It never starts one.
- `stop` asks it to exit. Without `--keep-runners`, it first interrupts
  every runner (`SIGTERM` to its process group, but without the `stop`
  file that `ws rm` writes, so the runner exits without an exit reason),
  so they read `stopped` and resume at the next start. With it, runners keep running and
  the next daemon adopts them.
- `origin = transient` (an auto-start) exits after 15 minutes with no live
  runner and no request; `managed` never exits by itself; `app` (started
  by the desktop app) never exits by itself while the process that started
  it is alive, and once that parent is gone (its parent pid changes) it
  behaves as `transient`.

**The socket.** A Unix socket at `$XDG_RUNTIME_DIR/ws/<h>.sock` when
`XDG_RUNTIME_DIR` is set, else `${TMPDIR:-/tmp}/ws-<uid>/<h>.sock`, where
`<h>` is the first 12 hex digits of the SHA-256 of the absolute
`CONFIG_DIR`, so two config directories (tests, a second install) never
share one. The directory is created 0700 and must be owned by the user;
the socket is 0600. Every connection's peer uid is checked
(`SO_PEERCRED` on Linux, `LOCAL_PEERCRED` on macOS) and must be the
daemon's own. There's no TCP listener. The protocol is HTTP/1.1 with JSON
bodies under `/v1/`; errors use `ws`'s error object
([0016](0016-output-contract.md)). Requests name a **workspace id**, never
a command, a path or an environment: the daemon reads everything else from
that workspace's records.

| Request | Does |
| ------- | ---- |
| `GET /v1/status` | the daemon, and each runner it watches |
| `POST /v1/preflight` `{role, endpoint, model}` | resolves and preflights a model ([0064](0064-choosing-the-runner-model-and-endpoint.md)) with the daemon's environment |
| `POST /v1/runners/<id>/start` `{resume}` | starts `ws runner run [--resume] <id>` for an existing runner session |
| `POST /v1/runners/<id>/stop` | writes `stop`, signals, waits, kills ([0063](0063-runner-children-are-supervised-sessions.md)); used by `ws rm` and by the desktop's **Stop** |
| `POST /v1/shutdown` `{keep_runners}` | `ws daemon stop` |

**How `ws` reaches it.** Commands that start or stop runners (`ws machine
spawn` for a `ws` child, `ws claude --runner ws`, a loop run on the `ws`
runner, `ws restore`, `ws rm` of a runner, and the hidden `ws runner
check` and `ws runner stop` the desktop app uses) connect to the socket. When nothing answers, they start one:
`ws daemon run --origin transient`, detached the way `internal/claude`
starts Claude Code's service (own session, stdio on `/dev/null`, stderr to
`daemon.log`, home directory as working directory), and wait up to 5 s for
the socket. If it still doesn't answer, they fail with
`daemon_unavailable` (exit 1, `details.log`), except `ws rm`, which then
stops the runner itself (writes `stop`, signals the process group) so
removal never depends on the daemon. `WS_DAEMON_AUTOSTART=0` turns
auto-start off, for setups that run the daemon under a service manager and
want a missing daemon to be an error. Commands that only read (`ws list`,
`ws machine wait`, `children`, `logs`, the desktop's listing) read the
runner files as before and never need the daemon.

**Supervision.**

- Runners are started in their own session (`setsid`), as the daemon's
  children, so a daemon restart doesn't kill them. The daemon watches each
  by `status.json`'s `pid` and process start time (`internal/proc`), every
  2 s, which works the same for its own children and for runners it
  adopted from an earlier daemon.
- **Crash** (the process is gone and `status.json` has no `exit`): the
  daemon resumes it (`--resume`) after 5 s, then 30 s, then 2 min. A fourth
  crash in the same run ends it: the daemon records the role's first fail
  verdict with exit reason `crashed`, writes `usage.json`, and sets `exit`,
  through the same `internal/runner` function the runner uses
  ([0065](0065-runner-agent-loop-tools-and-budgets.md)).
- **Daemon start** (after a reboot, a pod restart, or an auto-start):
  before listening, it reconciles every runner session in `state/`: alive
  ones are adopted; `stopped` ones (no process, no `exit`) are resumed when
  `[daemon] resume_on_start = true` (the default), counting toward the
  same crash limit; finished ones are left.
- **Hangs** are the runner's own budgets' job (`timeout`), not the
  daemon's.

**Environment and runtime configuration.** A runner's environment is
built by the daemon, lowest precedence first:

1. the daemon's own environment, captured when it started;
2. `[runner.env]` in `config.toml`: plain, non-secret values such as
   `METRICS_URL` or `OTEL_EXPORTER_OTLP_ENDPOINT` (refused when the name looks secret, as in
   [0061](0061-hosts-in-config-toml.md));
3. `CONFIG_DIR/secrets.env`: `NAME=value` lines, mode 0600 (a looser mode
   is refused with a warning, and the file is ignored), for endpoint tokens
   (`WS_ENDPOINT_TOKEN_<NAME>`), MCP servers' tokens, and credentials such
   as `OTEL_EXPORTER_OTLP_HEADERS`. It is the only secrets store in this
   design, on every platform. The desktop's Settings › Models writes it;
   people may edit it;
4. variables `ws` sets: `WS_RUNNER=ws`, `WS_WORKSPACE=<id>`,
   `WS_CONFIG_DIR`, and `WS_RUNNER_SECRET_NAMES` (the names defined in
   `secrets.env`, so the runner can scrub them from its tools'
   environment without reading the file).

The spawning agent's environment is never used, so a runner's environment
doesn't depend on who spawned it. `config.toml` and `secrets.env` are read
again at every start and resume, so edits apply to the next runner without
restarting the daemon; only layer 1 is frozen, and `ws daemon stop
--keep-runners` followed by any spawn refreshes it from the current shell.
What a runner then passes to its tools and MCP servers is in
[0066](0066-runner-security-boundaries.md) and
[0069](0069-runner-mcp-tools-and-claude-code-parity.md).

**Where it runs.**

- **ws-online pod**: `ws-online serve` starts `ws daemon run --origin
  managed` at startup and restarts it if it exits (backoff 1 s to 60 s), as
  it already runs its tick and watcher tasks. Its environment is the
  container's, so `GH_TOKEN` and the like are there by construction. After
  a pod restart, it resumes unfinished runners at once. `/tmp` isn't
  persistent, which is fine: the socket is recreated.
- **The desktop app hosts it** (below) while the app runs.
- **CLI with no app running**: auto-started on first need, as a transient
  daemon, from the shell that first needed it. People who want resume
  after a reboot without opening the app can install a launchd agent or a
  systemd user unit running `ws daemon run --origin managed`; the
  `ws daemon run` guide gives both.

Whatever starts it, it's the same `ws` binary, the same lock and the same
socket, so `ws machine spawn` talks to the one daemon of its config
directory and never needs to know who hosts it.

**The desktop app hosts the daemon.** The app is a long-lived process the
user already runs, so it is the daemon's natural host on a desktop. It
can't run the daemon in-process (the app is Rust, the daemon is `ws`'s Go
code), so it runs it as a supervised sidecar of the same binary:

- **Start.** When the app starts, its backend keeps `ws daemon run
  --origin app` running, with the code `ws-online` uses for its managed
  daemon (restart on exit with backoff from 1 s to 60 s). The sidecar is
  started in its own session, with the app's resolved `PATH` and the
  app's environment as layer 1.
- **Single instance.** The lock decides. If a daemon already runs (a
  transient one a shell started, or a managed one under launchd), the
  sidecar exits `daemon_running` (4), and the app uses the running daemon
  over the socket, checking again every 60 s and starting its own once
  that one has exited. Two daemons never run for one config directory.
- **App quit.** Quitting the app doesn't stop runners or the daemon. The
  sidecar, in its own session, sees its parent go and becomes
  `transient`: it keeps supervising and resuming runners until none is
  alive, then exits 15 minutes later. Closing the window isn't quitting:
  the app stays in the tray and keeps hosting.
- **App restart.** The new app finds the daemon still running and uses it
  (single instance, above); once it exits, the app starts its own.
- **No app running.** The CLI's auto-start applies as before. A transient
  daemon started from a shell is the one weak spot: its layer 1 is that
  shell's environment.
- **Login item.** Not in this design. The app doesn't start at login;
  resuming runners after a reboot without the app or a shell needs the
  launchd agent or systemd unit from the guide.
- **Status.** Settings › Models shows a **Runner service** line: running
  or not, who started it (`this app`, `a terminal`, `a service manager`,
  from `origin`), its pid, the runners it watches, and a **Restart**
  button (`ws daemon stop --keep-runners`, after which the sidecar starts
  a new one that adopts the runners, with the app's current environment).
- **Stop one runner.** The app's **Stop** on a `ws` agent runs the hidden
  `ws runner stop <id>`, which sends `POST /v1/runners/<id>/stop` through
  `Ensure`: the runner exits `stopped` after its current step, keeps its
  workspace and transcript, and isn't resumed.
- **Loops, later.** When the loop scheduler moves into the daemon
  (deferred, below), the app stops running its own `ws loop tick` task
  once the daemon's status says it schedules loops. Because the app hosts
  the daemon, loops then run whenever the app or any other host runs.

**Migration.** Nothing existing changes behaviour. Claude sessions stay
under Claude Code's service, and `ws restart` still restarts only that
service. `ws restore` additionally asks the daemon to resume stopped
runners. An older `ws` ignores the daemon and its files; runner sessions
are version 2 records, which it keeps ([0063](0063-runner-children-are-supervised-sessions.md)).
The daemon and the CLI are the same binary; a daemon from an older `ws`
answers `/v1/status` with its `ws_version`, and a newer client that needs
something it lacks fails with `daemon_unavailable`, "the running ws daemon
is older (v…); run ws daemon stop --keep-runners".

**Deferred**, each its own later decision:

- the loop scheduler moving into the daemon (`[daemon] loops = true`, then
  the desktop and `ws-online` stop running `ws loop tick` when the daemon
  says it does);
- the desktop's watcher and notifications, and `ws-online`'s, moving into
  the daemon;
- a separate Settings › Daemon section (the Runner service line in
  Settings › Models covers status and restart for now);
- starting the app, or the daemon, at login;
- supervising anything other than runners.

**Non-goals** (by the owner's decision): a per-endpoint queue or
`max_parallel` in the daemon. Concurrency per endpoint is left to the
orchestrator and to llm-router; the daemon starts every runner it's asked
to.

### Why this option

1. **Supervised**: crash restarts with a limit and a recorded reason;
   resume on start covers reboots and pod restarts; in the pod it happens
   with no one logged in.
2. **Defined environment**: four layers, one secrets file, never the
   spawner's environment.
3. **Small**: one process, a socket, a lock file, five requests. No unit
   per child and no OS service manager required; one is optional for the
   daemon itself.
4. **Compatible**: readers don't need it, Claude sessions don't touch it.
5. **Safe**: user-only socket with a peer check, and requests that name a
   workspace id rather than a command.

Where it's weak: it's a new long-lived process to debug, and a second
daemon next to Claude Code's. A transient daemon started from an agent's
shell inherits that shell's environment as layer 1 (layers 2–4 still
apply); a managed daemon avoids this. After a reboot on a desktop without
a service manager, runners resume only when something next needs the
daemon. `flock` on NFS works for `ws`'s existing locks but hasn't been
tested with a daemon holding one for days.

## Alternatives

### Detached processes and records only (the first draft)

**Pros**

- No new process; nothing to start.

**Cons**

- Nothing restarts a crashed runner; after a reboot nothing resumes until
  a person acts; the environment is the spawner's. Fails criteria 1 and 2.

### Reuse Claude Code's service

Run each runner inside a Claude background session, or ask the service to
run it.

**Pros**

- One daemon, already supervised.

**Cons**

- The service runs only Claude sessions; there's no interface for another
  program. A session that shells out to the runner spends Claude tokens on
  every on-prem child and reports nothing true about it. `ws restart` would
  then restart runners too.

### Delegate to systemd or launchd per runner

**Pros**

- Mature restart policies.

**Cons**

- Different per OS; the pod has no systemd user bus; one unit per child to
  create and clean up; environment injection differs per manager. Kept as
  an option for supervising the daemon itself, not each runner.

### The desktop app as the only daemon (no `ws daemon` without it)

**Pros**

- One host on a desktop, and no auto-started daemons.

**Cons**

- Fails in the ws-online pod's headless uses and in plain CLI use, and
  runners would stop being supervised whenever the app quits.

### The app stops the daemon, with `--keep-runners`, when it quits

**Pros**

- Nothing of the app's keeps running after Quit.

**Cons**

- Runners then run unsupervised until something starts a daemon: a crash
  isn't resumed and a finished runner isn't noticed by anything but
  readers. Becoming transient on Quit gives the same "nothing lingers"
  once the runners finish, without that gap.

### A bigger daemon now: runners, loops, watcher and the desktop backend

**Pros**

- One place for all background work, and the desktop app could become a
  thin client.

**Cons**

- It moves working code (the tick task, the watcher) without a need, and
  makes this design several times larger. The deferred list keeps the
  path open.

## Consequences

- New: `internal/daemon` (lock, socket server and client, supervisor,
  environment builder), `ws daemon run|status|stop` with guides, and the
  error codes `daemon_unavailable` (1) and `daemon_running` (4).
- `config.toml` gains `[daemon]` (`resume_on_start`) and `[runner.env]`;
  `CONFIG_DIR/secrets.env` and `CONFIG_DIR/daemon/` are new files.
- The desktop backend gains a module that keeps a daemon running: `ws
  daemon run --origin app` in the app, `--origin managed` in `ws-online`;
  `docs/online.md` and the desktop docs document it. Settings › Models
  gains the Runner service line, and the agent pane a Stop button.
- `make lint` gains a rule: `runner run` is started only by
  `internal/daemon`.
- How we'll know: e2e tests kill a runner and see it resumed, kill it four
  times and see `crashed`, stop and restart the daemon with
  `--keep-runners` and see the runner adopted, start a daemon over a
  `stopped` runner and see it resumed, and stop one runner and see it kept
  but not resumed ([0067](0067-runner-tests-use-vendored-fakes.md)). A
  daemon test sees an `app` daemon turn transient when its parent exits.
- Unverified: how macOS treats the sidecar's own session when the app is
  force-quit or updated in place; the desktop task checks it by hand.
- Unverified: behaviour across a real pod restart, and long-held `flock`
  on the pod's NFS volume.

## References

[^claude-daemon]: [ADR 0024](0024-claude-code-client.md) item 8 and `internal/claude/daemon.go` @98697f3 (read 2026-11-14). `Origin` says "'foreground' for `claude daemon run`, or 'transient — started on-demand by `claude` (pid N) in <dir>'".
[^ws-restart]: [ADR 0018](0018-restart-claude-daemon.md). "Starts a new daemon at once with a detached `claude daemon run`, so it inherits `ws`'s environment."
[^measured-ps]: Measured 2026-11-14 in the ws-online pod with `ps -eo pid,ppid,etime,args`, once: PID 1 `/usr/bin/tini -- /usr/local/bin/ws-online-entrypoint`, PID 7 `ws-online serve` (parent 1), `claude daemon run --origin transient --spawned-by {"label":"claude --bg",…}` (parent 1), `claude bg-pty-host … /tmp/cc-daemon-1000/…` (parent: the daemon), an MCP stdio server (parent: a session's process).
[^loop]: [ADR 0048](0048-agent-loop-scheduler.md), decision 7. "Nothing in `ws` runs in the background."
[^online]: [docs/online.md](../online.md) (read 2026-11-14). "The auto-review checker and loops run in the server, with or without a browser open"; `desktop/src-tauri/src/online/server.rs` spawns `loops::tick_forever`.
[^online-env]: [docs/online.md](../online.md), "Environment" and "Volumes" (read 2026-11-14).
[^pod]: [docs/online.md](../online.md), "Volumes" (read 2026-11-14): the home volume and `/data` persist; nothing else is mounted, so `/tmp` doesn't.
[^desktop]: [ADR 0050](0050-desktop-tray-notifications.md), "The window": "Closing it hides it; the app keeps running in the tray"; `desktop/src-tauri/src/env.rs` @98697f3: "An app opened from Finder or the Dock gets launchd's minimal PATH".
[^lockfile]: [ADR 0048](0048-agent-loop-scheduler.md), decision 6, and `internal/lockfile` @98697f3. "hold `CONFIG_DIR/loops/.lock` exclusively".
