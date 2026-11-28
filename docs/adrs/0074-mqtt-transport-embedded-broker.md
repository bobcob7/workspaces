# 0074. Controller and agents talk MQTT 5 over mutual TLS through a broker embedded in the `ws` daemon, one topic tree per environment, enforced by certificate

- Status: Accepted (2026-11-22), amended the same day (see Amendments); a third listener, `api_listen` (8885, HTTPS with mutual TLS, artifacts only), and the `api.update` request added by [0087](0087-artifacts-from-remote-environments.md); topics and ACLs unchanged
- Date: 2026-11-22

## Context

### Problem

Agents ([0073](0073-environment-agent-is-ws.md)) sit behind home or office
NAT, on Linux and Windows, and must work whether or not anyone is looking.
The user chose MQTT with mutual TLS. Who runs the broker, how do
requests, replies and terminal streams map onto topics, and how do we make
sure one remote can't read or command another?

### What a solution needs

1. **Outbound-only from remotes**: no inbound port on a remote.
2. **Mutual authentication**: only enrolled agents connect, and the
   controller is certain it's talking to its own broker.
3. **Isolation**: a compromised remote can't see or command another's
   topics, nor impersonate the controller.
4. **Presence**: online and offline are known promptly, including when a
   machine dies without disconnecting.
5. **Request/response and streams** (a terminal, a log tail) on top of
   publish/subscribe, with bounded sizes.
6. **No extra service to run** for the common case.

### Relevant facts

- The `ws daemon` is one long-lived process per config directory, hosted
  by the desktop app, run managed by `ws-online`, or auto-started
  ([0068](0068-ws-daemon.md)); Workspace Online is the always-on home
  ([0053](0053-workspace-online.md)).
- MQTT 5 has response topics and correlation data for request/response,
  user properties, per-message expiry, session expiry, and flow-control
  (`Receive Maximum`), and Last Will with delay[^mqtt5].
- Brokers authorize by client id, username or certificate fields, with
  per-topic ACLs; with TLS client certificates the common name is
  available to the broker's hooks.
- Go modules exist for an embeddable broker, `github.com/mochi-mqtt/server/v2`
  (v2.0.0 to at least v2.4.x), and a client, `github.com/eclipse/paho.golang`
  (v0.9.0 to v0.23.0)[^golist].

## Decision

We will embed an **MQTT 5 broker in the `ws` daemon**, listening for TLS
connections that require a client certificate signed by `ws`'s own CA
([0075](0075-environment-enrollment-and-certificates.md)). Agents and the
controller connect as clients; the broker is the only listener.

**Listeners.** `[environments] listen = "0.0.0.0:8883"` for agents (mutual
TLS), and `enroll_listen = "0.0.0.0:8884"` for first contact
([0075](0075-environment-enrollment-and-certificates.md)). Both off until
the first `ws env add`. The controller's own client connects in-process or
over a loopback listener with its own certificate, never the public one.

**Identity.** An agent's certificate common name is its environment id
(`env-<8 hex>`, set by `ws`, not chosen by the agent); the broker's hook
sets the MQTT client id to that name, overwriting the id the client
claims, and takes every ACL decision from it, ignoring the username and
password.

**Topics**, under `ws/v1/`:

| Topic | Publisher | Subscriber | Notes |
| --- | --- | --- | --- |
| `env/<id>/status` | agent | controller | retained; `online` + hello JSON; Last Will publishes `offline` |
| `env/<id>/snapshot` | agent | controller | retained; workspaces and agents, replaced on change and every 30 s |
| `env/<id>/req` | controller | agent | requests, QoS 1, with a response topic and correlation data |
| `env/<id>/res/<req>` | agent | controller | the reply, QoS 1 |
| `env/<id>/up` | agent | controller | agent-initiated requests (`cert.renew`): `{op, args}`, QoS 1, with a response topic (`env/<id>/req`) and correlation data |
| `env/<id>/stream/<sid>/out` | agent | controller | terminal and log bytes, QoS 0, with a sequence number |
| `env/<id>/stream/<sid>/in` | controller | agent | terminal input, resize, redraw, ping and close frames, QoS 1 (see Amendments, PR 4) |
| `ctl/events` | controller | nobody (reserved) | |

**ACL.** An agent may publish only to its own `status`, `snapshot`,
`res/#`, `stream/+/out` and `up`, and subscribe only to its own `req` and
`stream/+/in`. The controller may do everything under `env/#`. No other
client may publish under `ws/v1/#`. A client that violates an ACL is
disconnected by the broker's hook and the event logged (see Amendments).

**Requests.** A JSON object `{op, args}`, at most 256 KiB, with message
expiry equal to the operation's timeout (30 s by default, 10 min for a
`workspace.create`). A reply is `{ok, result}` or `{ok:false, error:{code,
message, details}}` using `ws`'s existing error codes
([0016](0016-output-contract.md)). A request that finds the agent
offline fails at the controller, not in a broker queue
([0072](0072-remote-environments.md)): the controller doesn't publish when
the retained `status` says `offline`, and a request that expires
unanswered fails `environment_offline`.

**Streams.** A terminal is a stream id from `terminal.open`. Bytes go in
chunks of at most 16 KiB with a sequence number; the receiver drops
nothing silently: on a gap it asks for a redraw (`terminal.redraw`), which
sends the screen's current state. Output the controller isn't reading is
dropped by the agent after 1 MiB queued, with a visible marker.

**Presence.** Keep-alive 30 s; Last Will with a 10 s delay publishes
`offline`; the agent publishes `online` on connect. The agent asks for a
session expiry of at least 10 s (a session expiry of 0 publishes the will
at once when the connection closes) and CleanStart on every connect, so a
reconnect starts clean and republishes its snapshot. A machine that drops
its connection is `offline` after about 11 s; one whose network goes silent
is `offline` after about 56 s (see Amendments).

**Reconnect.** Exponential backoff from 1 s to 60 s with jitter; an agent
never gives up.

### Why this option

1. Criteria 1 and 6: an embedded broker needs nothing else to run, and
   remotes dial out. The cost is that the `ws` instance's port must be
   reachable from the remotes (a port forward, a tunnel, or a host with a
   public address); for a laptop that sleeps, run the daemon under
   Workspace Online instead.
2. Criterion 2: both listeners require a CA-signed certificate; agents
   verify the broker's certificate against the CA they were given.
3. Criterion 3: ACLs keyed to the certificate common name, not anything
   the agent says.
4. Criterion 4: retained status plus a delayed Last Will.
5. Criterion 5: response topics for request/response; `QoS 0` for
   streams, so one slow terminal can't hold up control messages.

Where it's weak: MQTT isn't built for interactive byte streams; latency
to a terminal is a broker hop each way, and head-of-line blocking on one
TCP connection is shared with control messages. The spike measured it; see
Amendments (PR 4) for the result and the path chosen.

## Alternatives

### External broker (Mosquitto, EMQX)

**Pros:** mature, configurable, runs separately from `ws`'s restarts.
**Cons:** one more service, its certificates and ACL file kept in step
with `ws env add`. Can be supported later by letting `[environments]`
point at an external broker, since both sides are plain MQTT clients.

### gRPC or WebSocket with mutual TLS

**Pros:** better fit for streams and request/response.
**Cons:** not what the user chose; presence, retained snapshots and
fan-out would be built by hand.

## Consequences

- New dependencies: an embeddable MQTT 5 broker and an MQTT 5 client;
  both must be vendored or pinned and pass `make lint`.
- `ws daemon status` reports the broker and each environment's presence
  ([0068](0068-ws-daemon.md) amended); `ws-online`'s Docker image exposes
  8883/8884 and documents it.
- New config: `[environments] listen`, `enroll_listen`, and `public_name`
  (the DNS name in the server certificate and in enrollment codes).
- Conformance: Go tests, with the embedded broker on loopback, for ACL
  enforcement (an agent publishing to another's topic is disconnected),
  presence after a killed connection, request expiry, stream gaps and
  redraw, and a client without a certificate being refused.
- **Verified by the [broker spike](../design/remote-environments/broker-spike.md)**
  (loopback only): `github.com/mochi-mqtt/server/v2` v2.7.9 and
  `github.com/eclipse/paho.golang` v0.23.0 pass every required item, with
  the hook work listed under Amendments. Not measured: terminal latency and
  throughput over a real network.

## Amendments (2026-11-22)

**The `up` topic.** Agent-initiated requests, such as `cert.renew`, go to
`env/<id>/up` as `{op, args}`, with the response topic `env/<id>/req` and
correlation data; the controller replies there. The agent tells a reply
from a request by its correlation data. Without `up` the agent's ACL, which
allows it only to publish replies, had no topic for them
([0075](0075-environment-enrollment-and-certificates.md), amended).

**Corrections from the broker spike.** Each one replaces the text above it.

- Session expiry 0 publishes the will at once when the connection closes
  (measured 0.00 s), not after the delay. The agent asks for a session
  expiry of at least 10 s; the spike used 30 s.
- Silent-drop offline detection measured 56.0 s, not about 45 s. The broker
  notices the silence at 45 s (1.5 times the keep-alive), then waits out the
  10 s delay and up to a 1 s tick. A killed connection measured about 11 s.
- The broker takes the client id the client claims, not the certificate's.
  The hook sets it to the common name and clears the username.
- The broker doesn't disconnect a client that violates an ACL. It drops a
  QoS 0 publish silently and answers other violations with reason `0x87`,
  so the hook disconnects the client itself.
- The broker doesn't check will topics against the ACL, so a client could
  set a will on another's `status`. The hook checks the will topic on connect
  and refuses the connection when it isn't allowed.
- The broker closes a connection whose packet exceeds its maximum packet
  size, with no DISCONNECT, and doesn't advertise the size in CONNACK.
  Clients enforce their own cap on requests (256 KiB) and streams.
- The broker drops QoS 0 messages for a slow subscriber once its queue is
  full. The queue is `MaximumClientWritesPending`, set to 256 (the default
  8192 holds up to 128 MiB of 16 KiB chunks per slow client). The
  sequence-number gap check is therefore required, not optional.
- An in-process controller client has no certificate and skips the ACL, so
  the controller connects over a loopback listener with its own certificate.
- A reconnect while the broker still holds the old connection is a session
  takeover, and the broker mishandles a takeover of a live connection
  (mochi issue #562). The hook takes over the old session itself before the
  new one is accepted.
- The versions under Relevant facts are out of date. The latest mochi is
  v2.7.9 (2025-03-01) and the latest paho is v0.23.0 (2025-09-07). mochi's
  upkeep is slow, which is a maintenance risk, not a functional one.

## Amendments (PR 4, terminals)

Accepted by the owner for PR 4 ([0076](0076-environment-platforms-and-phasing.md)).
The decisions are the design's, in
[tasks-ui.md](../design/remote-environments/tasks-ui.md#decisions-this-design-adds-to-the-adrs).
Each one replaces the text above it.

- **Input is QoS 1.** `stream/<sid>/in` carries terminal input and resize at
  QoS 1, not 0: a lost keystroke can change a command, and the agent closes
  the terminal on an input gap rather than guess. Output stays QoS 0.
- **Frames, not requests.** `terminal.input`, `terminal.resize`,
  `terminal.redraw` and the ping are binary frames on `stream/<sid>/in`, not
  requests on `req`. Only `terminal.open` and `terminal.close` are requests. Closing a
  terminal does both: the controller sends a close frame (`c`) on
  `stream/<sid>/in` and the `terminal.close` request, so the agent ends the
  pty whichever arrives first.
  A frame is a version byte, a kind byte, a big-endian 64-bit sequence number
  and a body (`internal/envproto/terminal.go`). Output kinds are data (`d`),
  dropped (`g`) and exit (`x`); input kinds are input (`i`), resize (`r`),
  redraw (`w`), ping (`p`) and close (`c`).
- **Redraw.** The agent keeps no screen model. A redraw makes the program
  repaint by changing the pty's size by one row and back (two `SIGWINCH`),
  which repaints Claude Code and other full-screen programs; a line-mode
  shell shows only the marker. The controller asks for one on an output gap,
  and the agent after it drops output; at most one a second per terminal.
- **Drops on both sides.** The agent drops output after 1 MiB queued, as
  before, and the controller does the same when its client (the CLI) leaves
  1 MiB unread, with the same marker.
- **Lifetime.** Terminals don't survive a reconnect: the agent closes every
  terminal when its MQTT connection drops, and the controller ends the
  terminals of an environment that goes offline. The controller pings every
  15 s, and the agent closes a terminal with no frame for 60 s. An agent
  allows 8 terminals at once (`terminal_limit`), and closes a terminal whose
  input exceeds 128 KiB/s or 500 frames/s (`input_limit`); the CLI paces
  pasted input at 64 KiB/s.
- **Where it was weak: measured.** The spike
  ([terminal-spike.md](../design/remote-environments/terminal-spike.md))
  found echo latency and throughput within the design's limits on every
  profile, and broker memory bounded. It failed one criterion: a control
  request sent while a stream saturated the agent's single connection waited
  2.0 to 3.2 s, because the agent's own kernel send queue grew to several
  MB. Terminals stay on **one connection**, with the agent's socket bounding
  its not-yet-sent queue (`TCP_NOTSENT_LOWAT` 16 KiB). That cut the delay
  about tenfold, but on the 20 ms, 10 Mbit/s profile it measured **0.195 s**
  against the design limit of **0.142 s**, so it still misses the limit. The
  owner accepted this: a control request can wait about 0.2 s behind bulk
  terminal output on a slow uplink. A second connection passed in the spike
  and stays the fallback if it proves too slow in use.

## References

[^mqtt5]: OASIS, "MQTT Version 5.0", 2019-03-07. <https://docs.oasis-open.org/mqtt/mqtt/v5.0/mqtt-v5.0.html> (accessed 2026-11-22). Response Topic and Correlation Data properties (3.3.2.3.5, 3.3.2.3.6), Will Delay Interval (3.1.3.2.2).
[^golist]: Measured 2026-11-22: `go list -m -versions github.com/mochi-mqtt/server/v2 github.com/eclipse/paho.golang` from a scratch module; the first lists v2.0.0 through v2.4.x, the second v0.9.0 through v0.23.0. Only that the modules exist; their features are not yet checked.
