# ws env

Decided in [0073](../adrs/0073-environment-agent-is-ws.md),
[0074](../adrs/0074-mqtt-transport-embedded-broker.md) and
[0075](../adrs/0075-environment-enrollment-and-certificates.md). Each
subcommand is in the [`ws llm`](ws-llm.md) guides: `ws llm env add`,
`list`, `rm`, `rotate`, `ca rotate` and `ca status`.

```
ws env add <NAME> [--json]
ws env list [--json]
ws env rm <NAME> [--json]
ws env rotate <NAME> [--json]
ws env ca rotate [--finish] [--force] [--json]
ws env ca status [--json]
ws env agent enroll <CODE> [--json]
ws env agent run
ws env agent status [--json]
ws env agent unenroll [--json]
```

A remote environment is another machine, such as a Linux server, that runs `ws` as an agent and connects to yours. You add it
here, paste the code it prints onto that machine, and from then on it shows
in `ws env list` and can run work for you. Connections are TLS with client
certificates; the agent connects to your `ws` on `public_name`.

The commands above with `agent` run on the remote machine. `ws env agent`
is hidden from `ws --help` and `ws llm`, so the agent commands are described
here only.

## Setup

Set the name the remote machine can reach you at, once:

```console
$ ws config set environments.public_name ws.example.com
```

`public_name` is a DNS name or IP address. Without it, `ws env add` refuses
with `usage`, and the daemon doesn't open the environment listeners. The
listeners are on 8883 (the broker, MQTT over TLS) and 8884 (enrollment, HTTPS),
and the remote machine must reach both. Forward them if you're behind a
router: `8883` and `8884`.

## Enrolling

```
$ ws env add linux-server
Code (works once, shown once):
  ws-enroll-v1.eyJoIjoid3MuZXhhbXBsZS5jb206ODg0NCIs...

On linux-server:
  ws env agent enroll ws-enroll-v1.eyJoIjoid3Mu...
  ws env agent run            # or install the service: see below
```

The code is `ws-enroll-v1.` followed by base64url JSON with the enroll host
(`ws.example.com:8884`), the SHA-256 fingerprint of the CA's certificate, and
a 128-bit secret. The secret is stored only as a hash. The code works once,
and has no expiry until it's used, removed with `ws env rm`, or replaced with
`ws env rotate`. It's printed on stdout only and never logged.

On the remote, `ws env agent enroll` connects to the enroll host, checks the
server's chain against the pinned fingerprint before it sends anything secret,
sends its certificate request and the secret once, and gets back a
certificate for 90 days and the CA. A mismatch aborts before the secret leaves
the machine.

## Files

On your machine, under `CONFIG_DIR`:

- `environments/ca/`: the CA key (mode 0600) and certificate, and the
  listeners' server certificate. The key is read only by the daemon.
- `environments/<id>.toml`: one record per environment: `state` is
  `pending` or `enrolled`, the secret's hash, and the current certificate's
  serial, expiry and CA. A pending record has no certificate. `prior_serials`
  lists up to 5 earlier certificates that renewal superseded.
- `environments/revoked`: the serials of the certificates `ws env rm` and
  `ws env rotate` revoked: the current one and up to 5 that renewal
  superseded. Connections with them are refused.
- `state/.environments/<name>/snapshot.json`: the last workspace snapshot
  each agent sent, which `ws env list` counts.

On the remote, under its `CONFIG_DIR/environment/`: the agent's key (mode
0600), its certificate, and the CA certificate it verifies your `ws` with.

## Renewal and CA rotation

Certificates last 90 days. The agent renews its certificate over the live
connection once it's within `[environments] renew_before_days` of expiring
(default 30, from 1 to 60), and re-checks every hour, so a machine that was
asleep renews as soon as it's back. A certificate that expires while the agent
is offline needs `ws env rotate <name>`.

The CA lasts a year. `ws env ca status` warns once it's within
`renew_before_days`. To rotate it, `ws env ca rotate`:

1. makes a new CA. The broker trusts both CAs, and serves a server certificate
   signed by the new one;
2. sends the new CA bundle to every connected agent, which keeps it until it
   has it;
3. signs each agent's next renewal with the new CA. `ws env ca status` counts
   how many environments still hold a certificate from the old one.

Then `ws env ca rotate --finish` drops the old CA once none do. `--force`
drops it anyway: the environments still on it are disconnected and listed as
`stranded`, and they need `ws env rotate`. Connected environments stay
connected until then. Nothing is lost if `ws` is interrupted mid-rotation: the
next CA command finishes it first.

## Revoking

`ws env rm <name>` revokes the environment's current certificate and up to 5
earlier ones that renewal superseded, deletes its record and its snapshot, and
reloads the daemon, which disconnects it. The agent
can't reconnect with that certificate. `ws env agent unenroll` on the remote
deletes its files; until then the controller still lists it.

## Running the agent

On the remote, `ws env agent run` keeps the connection and runs its work. It
logs JSON lines to stderr and exits 0 on SIGTERM or interrupt. Keep it running
with the service manager of the machine:

systemd user unit (`~/.config/systemd/user/ws-env-agent.service`):

```ini
[Unit]
Description=ws environment agent
After=network-online.target

[Service]
ExecStart=%h/.local/bin/ws env agent run
Restart=always

[Install]
WantedBy=default.target
```

Then `systemctl --user enable --now ws-env-agent`.

launchd plist (`~/Library/LaunchAgents/com.bobcob7.ws-env-agent.plist`):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.bobcob7.ws-env-agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/ws</string>
    <string>env</string>
    <string>agent</string>
    <string>run</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
</dict>
</plist>
```

Then `launchctl load ~/Library/LaunchAgents/com.bobcob7.ws-env-agent.plist`.

### Windows (ADR 0081, not yet run on a real machine)

The Windows build is cross-built, vetted and linted for amd64 and arm64; an
allow-list of packages is tested on `windows-latest`. It has not yet run on a
real Windows machine with Claude Code signed in.

Install `ws.exe` to `%LocalAppData%\Programs\ws\` and install Git for Windows:
loop gates run through its `sh.exe`. To start the agent at log on, run this
in Command Prompt:

```bat
schtasks /Create /SC ONLOGON /TN ws-env-agent /TR "\"%LocalAppData%\Programs\ws\ws.exe\" env agent run"
```

Terminals and `agent.send` are refused with `platform_unsupported` until
ConPTY is built. WSL2 is the alternative: run the Linux agent inside WSL2.

Not verified on Windows, here or in CI:

- whether the `claude` background commands (`--bg`, `agents`, `logs`,
  `attach`, `stop`, `daemon run`) exist and behave on native Windows;
- `claude attach` in a terminal, since ConPTY isn't built;
- a real Task Scheduler run, including ending it and logging off;
- that another local user can't read the agent's key or the daemon socket;
- paths over 260 characters without `LongPathsEnabled`, and antivirus or
  sharing violations on files the agent writes.

## Agent commands

These run on the remote machine. Each prints the same JSON its `ws llm`
guide shows, or text without `--json`.

| Command | Does |
| --- | --- |
| `ws env agent enroll <CODE>` | Enrolls this machine with the code `ws env add` printed. Refused with `environment_exists` if it's already enrolled, `enrollment_used` if the code was used, and `enrollment_failed` if the server's CA doesn't match the code or the server refuses |
| `ws env agent run` | Connects and runs until stopped. Refused with `environment_unknown` if not enrolled |
| `ws env agent status [--json]` | Shows the enrollment: the environment, its broker, and when its certificate renews |
| `ws env agent unenroll [--json]` | Deletes this machine's key and certificate. The controller lists the environment until `ws env rm` |

## JSON output

`ws env list --json` is the table's JSON, with `status` (`pending`,
`online`, `offline`, or `unknown` without a daemon), `last_seen`, `hello`
(the agent's `ws` and Claude versions, OS and architecture) and `workspaces`
(the count in its last snapshot). Every field is always present: a value that
isn't known is `null`, and a list is `[]`.

## Limits

An agent's snapshot lists its Claude Code sessions, `agents`, newest started
first and at most 200. A session with no start time comes last, and the
sessions past the 200th are left out of the snapshot. Each snapshot and each
reply is at most 256 KiB.

## Operations

Once enrolled and connected, the controller runs work on the environment
through a fixed table of operations. Each is the remote's own `ws` code path,
run in-process with the same arguments and the same JSON result as the CLI
command ([0073](../adrs/0073-environment-agent-is-ws.md)). The controller's
side is `ws claude --env` and `ws list --env` ([`ws claude`](ws-claude.md#environments),
[`ws list`](ws-list.md)); the rest are used by them.

| Operation | Runs | Since | Timeout |
| --- | --- | --- | --- |
| `workspace.create` (`claude` and `runner` kinds) | `ws claude`, or `ws claude --runner ws` | 1 | 10 min |
| `workspace.list` | `ws list --json --all` | 1 | 30 s |
| `workspace.rm` | `ws rm`, with `--keep-children` | 1 | 2 min |
| `workspace.rename` | `ws rename` | 1 | 30 s |
| `workspace.hide` | `ws hide` and `ws unhide` | 1 | 30 s |
| `agent.status` | Claude Code's `agents`, for one session | 1 | 30 s |
| `agent.logs` | Claude Code's `logs`, at most 2000 lines | 1 | 30 s |
| `agent.send` | a message to a Claude Code or `ws` runner agent | 1 | 90 s |
| `agent.stop` | Claude Code's `stop` | 1 | 30 s |
| `review.start` | `ws review start --requester none` | 3 | 10 min |
| `review.status`, `review.list`, `review.report`, `review.profiles` | `ws review list`, `report`, `profiles` (a page of a report is at most 192 KiB) | 3 | 30 s |
| `review.decide`, `review.acted` | `ws review decide`, `ws review acted` | 3 | 30 s |
| `loop.list`, `loop.show`, `loop.history`, `loop.history_run`, `loop.memory_list`, `loop.memory_get` | `ws loop list`, `history`, `memory` (read only) | 3 | 30 s |
| `loop.add`, `loop.edit`, `loop.pause`, `loop.resume`, `loop.rm`, `loop.history_clear`, `loop.memory_set`, `loop.memory_rm`, `loop.memory_clear` | the matching `ws loop` commands | 3 | 30 s |
| `loop.run` | `ws loop run [--force]` | 3 | 15 min |

`Since` is the protocol the operation needs (see below). A `shell` kind
isn't in the table: a shell is a terminal ([Terminals](#terminals)).

**Protocol.** The hello carries `protocol`, the newest operation protocol the
agent speaks: 1 with PR 3's operations, 2 with the terminal operations (PR 4),
3 with `review.*` and `loop.*` and the snapshot's `loops` (PR 4, [0080](../adrs/0080-reviews-and-loops-on-environments.md)), and 0 from an agent older than PR 3. An
operation whose `Since` is above the agent's protocol is refused before it is
sent, with `environment_outdated` (exit 1). Its details are `environment`,
`op`, `ws_version`, `protocol` and `need_protocol`, so an agent older than
an operation's `Since` doesn't run it: one from PR 3 refuses `terminal.open`
and `terminal.close`. The version string isn't compared:
`git describe` strings don't order reliably.

**Requests, timeouts and deduplication.** Each request carries a
`request_id`, 16 lowercase hex characters from `crypto/rand`, and a
`deadline`. The agent answers on `env/<id>/res/<request_id>`, and runs each
operation under its deadline. The controller waits until the deadline plus
15 s. The agent keeps each request id, running or finished, in memory: a
finished one for 15 minutes, at most 512 of them, the oldest finished dropped
first. A duplicate of a running request waits for it and gets the same reply;
a duplicate of a finished one gets the stored reply. So an operation runs
once, even when the controller republishes it after a reconnect. If the agent
restarts mid-create, the store is lost, and the removal pass on the remote
removes the half-built workspace ([0015](../adrs/0015-workspace-lifetime-follows-session.md)).
A call that times out fails `environment_offline` with details
`{"environment", "request_id", "op", "reason": "timeout"}`. The operation may
still have run; `ws list --env <name>` shows what exists.

**Security.** The operations are an allow-list, checked three times
([0072](../adrs/0072-remote-environments.md), D6):

- The agent refuses an operation it doesn't know (`usage`), and one whose
  caller isn't the controller. The controller refuses one on the agent's
  channel whose caller isn't the agent. The daemon's call endpoint refuses
  any operation that isn't controller-callable.
- Arguments decode strictly: an unknown key is refused, and each field is
  checked before anything runs. A model matches
  `^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`; a name is 1 to 64 bytes without
  control characters (`name_invalid`); a prompt is 1 to 65536 bytes and a
  message 1 to 16384 bytes; `tail` is 0 to 2000. There are no free-form
  Claude arguments: only a prompt and a model.
- A spec must not start with `-` and must parse as a repo spec on the agent,
  which refuses local paths and `file://` (`spec_invalid`). A Claude prompt
  can't start with `-`, since `claude` would read it as a flag (`usage`).
- A workspace reference is an id, or an absolute path under the agent's
  `workspaces/` with no `..` element; anything else is `outside_workspace`
  (exit 2), and nothing runs.

## Reviews on an environment

`ws review start --env <name>` ([`ws review`](ws-review.md)) runs the whole
review on that machine: its clone cache, review workspace, Claude session and
reports are in the agent's `CONFIG_DIR`. The controller keeps the forge side:
the pull request list, posting comments, and the Fix and Ask agents. **No
token crosses the network:** no operation takes one, and results are redacted
([0080](../adrs/0080-reviews-and-loops-on-environments.md)).

Set these up **on the remote machine**, as the user that runs `ws env agent
run`:

1. **GitHub:** install `gh` and run `gh auth login` for each host
   (`gh auth status` must list it), or set `GH_TOKEN` in the agent's service
   environment (the systemd `EnvironmentFile=` or the launchd
   `EnvironmentVariables`).
2. **Private repositories on any forge:** git credentials for `git fetch`, such
   as a credential helper or SSH keys.
3. **Forgejo or Gitea:** nothing more. A review starts at the head commit, and
   `WS_FORGEJO_TOKEN_*` stays on the controller.
4. **Claude Code** signed in.

Without these, `review.start` fails with `gh_missing`, `gh_auth`,
`clone_failed`, `fetch_failed` or `claude_missing`, and `details.environment`.

On a remote, `ws review start` with `bypassPermissions` and no confirm flag
is refused, in the agent and in the desktop. Whether it may ever be allowed is
an open owner decision. A finding's decision suggestion index counts from 0.
Reviews are limited to 12 starts an hour per agent. `ws review wait --env`
polls `review.status`; a report is read in pages of at most 192 KiB, at most
1000 pages. A remote markdown or html report has empty submission summaries
(a known gap). Profiles on a remote are read only: edit them there with `ws
review profiles add`.

## Loops on an environment

A loop made with `ws loop add --env <name>` lives on that machine: its record,
history, memory and reports are in the agent's `CONFIG_DIR`, and the
controller holds only a summary of it in the snapshot's `loops` (at most 100;
no prompt, script or variables), which `ws loop list --env <name> --cached`
reads.

- **The agent ticks while connected.** It runs `ws loop tick --quiet
  --max-starts 2` every 60 s, the first time one period after it connects.
- **Offline means paused.** No tick runs, nothing is marked `paused`, and
  nothing is made up: on reconnect each overdue loop is due once, and the
  agent starts two per pass, so ten overdue loops start over five minutes.
- **Don't also run another ticker** (`ws loop tick --watch`, or a desktop app)
  on that machine with the same `CONFIG_DIR`. `loops/.lock` serializes them,
  but that ticker keeps running loops while the agent is offline.
- **Trust.** A loop's script and prompt run as the agent's user on that
  machine, like any Claude agent the controller starts there: anyone who can
  use this controller can run code as that user on every enrolled machine.
  Saving a loop with a script or `bypassPermissions` needs confirming
  (`--yes`, or the desktop's dialog); so do `ws loop rm` and clearing history
  or memory. Set `[environments] agent_loop_scripts = false` in the remote's
  `config.toml` to refuse scripts from the controller
  ([`ws config`](ws-config.md)); existing scripts still run.
- **Rates, per agent:** `loop.run` 30 an hour and one per loop per 60 s; adding,
  editing and removing loops 60 an hour. Over a limit the error is
  `operation_limited` with `retry_after_seconds`.
- **Redaction.** The agent replaces the values of its own tokens and secrets,
  and common token formats, with `[redacted]` in every review and loop result.
  It is best effort: it does not catch base64 or URL-encoded secrets, and a
  cut after redaction can leave a short token prefix. A write containing
  `[redacted]` is refused. List pages are capped at 1000 pages, and a
  truncated memory preview doesn't load the full value on open (a follow-up).
- **Not run here:** Fix and Ask on the environment, remote profile editing,
  redacting `agent.logs`, and `cache.*` are later.

## Terminals

A terminal is a process in a pseudo-terminal on the agent's machine: Claude
Code's `claude attach` for a workspace ([`ws attach --env`](ws-attach.md)), or
`ws shell` ([`ws shell --env`](ws-shell.md)). `terminal.open` (kind `attach`
or `shell`, `Since: 2`, 30 s) returns a stream id, and `terminal.close` ends
it. The CLI reaches them through the daemon's `POST
/v1/environments/<name>/terminal`, which upgrades the connection, so it never
speaks MQTT.

**Frames.** Bytes travel as binary frames on `env/<id>/stream/<sid>/out`
(agent to controller, QoS 0) and `.../in` (controller to agent, QoS 1). A
frame is a version byte (1), a kind byte, a big-endian 64-bit sequence number
and a body:

| Kind | Direction | Body |
| --- | --- | --- |
| `d` data | out | 1 to 16 KiB of output |
| `g` dropped | out | the count of bytes dropped, 8 bytes |
| `x` exit | out | JSON `{"code", "reason"}`; the reason is `exited`, `closed`, `idle`, `input_gap`, `input_limit` or `disconnected` |
| `i` input | in | 1 to 4 KiB |
| `r` resize | in | columns and rows, two big-endian 16-bit numbers, each 1 to 1000 |
| `w` redraw | in | none |
| `p` ping | in | none |
| `c` close | in | none |

**Limits.** At most 8 terminals per agent (`terminal_limit`, exit 4). Output
the reader isn't taking is dropped after 1 MiB queued, by the agent and by the
controller for its client, with a visible marker. The agent closes a terminal
whose input exceeds 128 KiB/s or 500 frames/s, or has a gap in its input
sequence, rather than guess; the CLI paces pasted input at 64 KiB/s.

**Lifetime.** Terminals don't survive a reconnect: the agent closes every
terminal when its MQTT connection drops, and the controller ends those of an
environment that goes offline, so `ws attach` says so and exits 1
(`environment_offline`). The controller pings every 15 s, and the agent closes
a terminal with no frame for 60 s. The agent keeps no screen model: on an
output gap the controller asks for a redraw, and the agent does after it drops
output, at most once a second. A redraw changes the pty's size by one row and
back, which repaints Claude Code and other full-screen programs; a line-mode
shell shows only the marker.

**Latency.** Terminals share the agent's one connection with control requests.
Its socket bounds the not-yet-sent queue (`TCP_NOTSENT_LOWAT` 16 KiB), which
cut a control request's delay under bulk output about tenfold. On a 10 Mbit/s
uplink with a 20 ms round trip, the
[spike](../design/remote-environments/terminal-spike.md) measured **0.195 s**
against the design's limit of **0.142 s**, so it still misses it: a control
request can wait about 0.2 s behind a `cat` of a large file. Typing echo and
throughput met their limits. A second connection passed in the spike and is
the fallback.

## Exit codes

| Situation | Error code | Exit |
| --- | --- | --- |
| Arguments or a flag error, or `public_name` isn't set | `usage` | 2 |
| A name that isn't valid, or `local` | `name_invalid` | 2 |
| No environment with that name | `environment_unknown` | 3 |
| A name already taken, an enrollment on a machine already enrolled | `environment_exists` | 4 |
| A code already used | `enrollment_used` | 4 |
| The agent already has 8 terminals open | `terminal_limit` | 4 |
| The agent's OS has no pty support (today Windows): terminals and `agent.send` are refused (ADR 0081) | `platform_unsupported` | 1 |
| `--finish` while environments hold old-CA certificates | `ca_in_use` | 4 |
| Enrollment refused, or a certificate has expired | `enrollment_failed` | 1 |

See [`ws llm`](ws-llm.md) for the guides of each subcommand.

## Scopes

A workspace's scope is the set of environments it may start children on. A
person grants it with `ws claude --env-scope` (see [ws claude](ws-claude.md)).
The controller keeps a ledger, `environments/scopes.json`, of the remote
workspaces it created with a scope and of every cross-environment child. For a
child request, it checks that the child's ledger parent is on the requesting
environment, and it bounds a spawn claim by the ceiling the ledger gives the
requester. Protocol 4 carries scopes; an agent on an older protocol is
`environment_outdated`.
