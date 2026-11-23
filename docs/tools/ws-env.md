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

`Since` is the protocol the operation needs (see below). A `shell` kind
isn't in the table: a shell needs the terminal streams, which come with the
next release ([0072](../adrs/0072-remote-environments.md), deviation 2).

**Protocol.** The hello carries `protocol`, the newest operation protocol the
agent speaks: 1 in this release, and 0 from an agent older than it. An
operation whose `Since` is above the agent's protocol is refused before it is
sent, with `environment_outdated` (exit 1). Its details are `environment`,
`op`, `ws_version`, `protocol` and `need_protocol`, so an agent from before
this release runs none of the operations. The version string isn't compared:
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

## Exit codes

| Situation | Error code | Exit |
| --- | --- | --- |
| Arguments or a flag error, or `public_name` isn't set | `usage` | 2 |
| A name that isn't valid, or `local` | `name_invalid` | 2 |
| No environment with that name | `environment_unknown` | 3 |
| A name already taken, an enrollment on a machine already enrolled | `environment_exists` | 4 |
| A code already used | `enrollment_used` | 4 |
| `--finish` while environments hold old-CA certificates | `ca_in_use` | 4 |
| Enrollment refused, or a certificate has expired | `enrollment_failed` | 1 |

See [`ws llm`](ws-llm.md) for the guides of each subcommand.
