# 0073. The environment agent is `ws` itself, run as `ws env agent run`; the controller sends it whole operations and it runs them with the same code as a local `ws`

- Status: Accepted (2026-11-22)
- Date: 2026-11-22

## Context

### Problem

A remote environment ([0072](0072-remote-environments.md)) needs a
program on the remote machine that does what `ws` does locally: clone
into a cache, create workspaces, start and supervise Claude Code
sessions, list and remove them, attach a terminal. The user wants to
install it on a Linux server and a Windows rig, and does not want it to
bundle Claude Code. What is the agent, and what does it do for the
controller?

### What a solution needs

1. **No second implementation** of workspaces, trees, records,
   `ws machine`, or session handling.
2. **Claude Code isn't shipped with it**: the agent finds the `claude`
   already installed and signed in, and says clearly when it isn't.
3. **Children and `ws machine` work on the remote** as they do locally,
   with state on the remote.
4. **The controller can't make the agent run an arbitrary command** it
   has no business running, beyond what starting a session already
   allows.
5. **Small**: one binary to install, no inbound ports.

### Relevant facts

- `ws` is one Go binary with subcommands
  ([0004](0004-single-binary-with-subcommands.md)); the `ws daemon`
  ([0068](0068-ws-daemon.md)) is already a long-lived process per config
  directory, and `ws-online` ([0053](0053-workspace-online.md)) already
  drives `ws` through its JSON commands.
- `ws` runs Claude Code only as a child process (`claude --bg`,
  `claude logs`, `claude attach`, `claude stop`;
  [0024](0024-claude-code-client.md)); it contains no Claude Code.
- Records, locks and removal passes are files under `CONFIG_DIR`
  ([state](../state.md)); an agent started from inside a workspace
  reaches `ws` by the working directory ([0044](0044-workspace-trees.md)).
- Claude Code reads a token from `CLAUDE_CODE_OAUTH_TOKEN` or its own
  login, and `ws` passes its environment on
  ([0053](0053-workspace-online.md)).
- Platform-specific code today: pty (`pty_linux.go`, `pty_darwin.go`,
  `pty_other.go`), peer credentials, process tables and power state each
  have linux/darwin/other files; `internal/lockfile`, `internal/gate`,
  `internal/shell`, `internal/mcp`, `internal/daemon` use `syscall` or
  `x/sys/unix`[^grep].

## Decision

We will make the agent a mode of the same binary: **`ws env agent run`**
(for people; hidden from agents' `ws llm`, like the other non-`machine`
commands).

**What it is.** A long-lived process on the remote that holds one MQTT
connection to the controller's broker
([0074](0074-mqtt-transport-embedded-broker.md)), authenticated by its
client certificate ([0075](0075-environment-enrollment-and-certificates.md)).
It has its own `CONFIG_DIR` on the remote: its own repo cache, `state/`,
`workspaces/`, and `secrets.env`. Nothing about it is stored on the
controller except a snapshot.

**What the controller asks.** A fixed set of operations, each the remote
`ws`'s existing code path run in-process with the same arguments and the
same JSON result as the CLI command:

| Operation | Runs |
| --- | --- |
| `workspace.create` (`claude`, `shell` or `runner` kind, specs, name, prompt, flags) | `ws claude` / `ws shell` (but see Amendments, PR 4: a shell is `terminal.open`) |
| `workspace.list`, `workspace.rm`, `workspace.rename`, `workspace.hide` | the matching commands |
| `agent.status`, `agent.logs`, `agent.send`, `agent.stop` | Claude Code's `agents`, `logs`, `stop` and `ws`'s send |
| `terminal.open`, `terminal.input`, `terminal.resize`, `terminal.close`, `terminal.redraw` | `claude attach` or the shell, in a pty (see Amendments, PR 4) |
| `files.list`, `files.read` (bounded) | the desktop's file and diff views |
| `review.start`, `review.status`, `review.list`, `review.report`, `review.decide`, `review.acted`, `review.profiles` | `ws review …`, run in-process on the remote ([0078](0078-reviews-and-loops-on-environments.md)) |
| `loop.list`, `loop.show`, `loop.add`, `loop.edit`, `loop.pause`, `loop.resume`, `loop.rm`, `loop.run`, `loop.history`, `loop.history_run`, `loop.history_clear`, `loop.memory_*` | `ws loop …`, run in-process on the remote ([0078](0078-reviews-and-loops-on-environments.md)) |
| `cache.*` | later |
| `config.get`, `config.set` | `ws config`, for a fixed allow-list of keys |

Results of `review.*` and `loop.*` are redacted by the agent ([0078](0078-reviews-and-loops-on-environments.md)); the allow-list of a finding's decision suggestion index starts at 0.

An unknown operation is refused. There is no "run this command" operation
and no way to write an arbitrary path; a request's paths must lie under the
agent's `workspaces/` (`outside_workspace`, as
[0046](0046-ws-machine.md)).

**What it publishes.** Its hello (version, OS and architecture, whether
`claude` is found, its version, and whether it's signed in), a snapshot of
its workspaces and agents on change and at least every 30 s, and each
terminal and log stream a request opened.

**Claude Code.** Not bundled and not installed by the agent. Its hello
reports `claude: missing | present | signed_out`; `workspace.create` of a
Claude session on `missing` fails `claude_missing`, as it does locally. A
token can go in the agent's `secrets.env` as `CLAUDE_CODE_OAUTH_TOKEN`
([0066](0066-runner-security-boundaries.md)); the agent never accepts one
over the network.

**Agents on the remote use the remote `ws`.** The agent's `PATH` entry for
`ws` is the same binary, so an agent that runs `ws machine spawn` there
creates its child in the remote's `CONFIG_DIR` with the remote parent link
([0044](0044-workspace-trees.md)): inheritance of the environment needs no
code. `ws llm` there is unchanged.

### Why this option

1. Criteria 1 and 3: it's the existing code, so trees, records, locks and
   `ws machine` behave the same, and a new `ws` operation reaches remotes
   by adding a row to the table.
2. Criterion 2: Claude Code is found on `PATH`, not shipped. The cost is
   that it has to be installed and signed in on each remote first.
3. Criterion 4: an allow-list of operations with typed arguments. What a
   session can do once it runs is the user's account's, as in
   [0066](0066-runner-security-boundaries.md).
4. Criterion 5: no inbound ports; the agent dials out.

Where it's weak: the remote's `ws` must be updated with the controller's
(the hello's version is checked; a controller refuses operations an agent
is too old for, `environment_outdated`); and the table is a second
interface to keep in step with the CLI.

## Alternatives

### A separate small agent binary

**Pros:** smaller, no `ws` dependencies on the remote.
**Cons:** a second implementation of creation, trees and records, which
would drift. Children on the remote would need their own `ws` anyway.

### SSH instead of an agent

Run `ws --json` over SSH.

**Pros:** nothing to install but `ws`.
**Cons:** needs inbound access to each machine and a Windows SSH server;
no presence, no streams, no push; the user asked for an agent over MQTT.

## Consequences

- New `docs/tools/ws-env.md` (`ws env add|list|rm|rotate`,
  `ws env agent enroll|run|status`); `ws llm` doesn't list the agent
  commands to agents.
- New package `internal/envagent` (operations table, snapshot publisher)
  and `internal/environment` (controller side: records, client).
- Conformance: for each operation, a Go test that the agent's result equals
  the CLI's `--json` for the same input; that an unknown operation and a
  path outside `workspaces/` are refused; that `claude: missing` fails a
  Claude start with `claude_missing`.
- **Unverified:** that `ws`'s commands can be called in-process rather
  than through a subprocess. If they can't without large changes, the
  agent runs `ws <cmd> --json` as a subprocess; the table and the results
  are the same.

## Amendments (PR 4, terminals)

Accepted by the owner for PR 4 ([0076](0076-environment-platforms-and-phasing.md)).

- **A shell isn't a `workspace.create` kind.** It is `terminal.open` with
  kind `shell`: the workspace lives as long as the terminal. The agent runs
  **its own executable** (`os.Executable()`) as `ws shell` in a pty, the one
  operation that isn't in-process, because a shell needs its own process and
  a controlling terminal. It sets `WS_ENVIRONMENT` in that pty so the
  workspace records its environment.
- **Terminal operations.** `terminal.open` and `terminal.close` are
  requests; `terminal.input`, `terminal.resize` and `terminal.redraw` are
  frames on the stream ([0074](0074-mqtt-transport-embedded-broker.md),
  Amendments). Attach takes a **workspace**, and the agent reads its Claude
  session id.
- **Protocol 2.** The hello's `protocol` became 2 with `terminal.open` and
  `terminal.close` (`Since: 2`); an agent from PR 3 answers every PR 3
  operation as before and refuses terminals with `environment_outdated`.
  Reviews and loops later raised it to 3.

## References

[^grep]: Measured 2026-11-22 on the repository at `2cbb103`: `grep -rln "syscall\.\|golang.org/x/sys/unix" internal cmd --include=*.go`, excluding tests, lists `internal/{backup,claude,cli,daemon,exectest,gate,lockfile,mcp,proc,shell}` and `cmd/`.
