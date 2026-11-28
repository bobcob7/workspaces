# 0087. An agent on a remote environment reaches the controller's artifact store over HTTPS with mutual TLS, authenticated by its enrolment certificate, on a third controller listener whose address the controller sends over MQTT

- Status: Accepted
- Date: 2026-11-27
- Supersedes: the "Remote environments" paragraph of [0078](0078-artifacts.md) (the refusal with `artifact_unreachable` and the out-of-scope note)
- Amends: [0074](0074-mqtt-transport-embedded-broker.md) (a third listener beside the broker and enrolment), [0075](0075-environment-enrollment-and-certificates.md) (the client certificate also authenticates that listener)

## Context

### Problem

The artifact store is the controller's ([0078](0078-artifacts.md)). An agent
on an enrolled remote environment runs its own `ws` with its own
`CONFIG_DIR` ([0073](0073-environment-agent-is-ws.md)), so 0078 refuses
every `ws machine artifact` command there with `artifact_unreachable`, and
an `@artifact:` reference in a prompt that reaches a remote is sent as plain
text with a warning. The wsenv audit ([audit.md](../design/wsenv/audit.md))
marked that row "OK", which it is not: an agent on a remote can't read a
screenshot a person shared, or share a pitch back. How does an agent on a
remote environment use the controller's store, files up to the existing
10 MiB, with the same ownership, atomicity, privacy and quota rules as a
local agent, and fail clearly when it can't?

### What a solution needs

1. **Same commands, same output.** `ws machine artifact add|list|show|get|put|rm`
   and `comment list|add|resolve` behave and print as they do locally;
   `@artifact:` references in `ws claude --env`, `ws machine spawn --env`
   and a spawn on a remote are checked and get the hint line.
2. **Files up to 10 MiB** without inflating them or splitting them into
   pieces each side must reassemble.
3. **No new credential.** The enrolment certificate ([0075](0075-environment-enrollment-and-certificates.md))
   identifies the machine; revocation, expiry and CA rotation keep working.
4. **The store's rules hold**: the root-tree owner check on `rm`, 0600
   files written through a temporary file and renamed, the 2 GiB quota and
   the per-kind limits, all enforced by the controller.
5. **Clear failures**: an unreachable controller, a refused certificate,
   and either side running an older `ws` each give `artifact_unreachable`
   with a reason, never a store on the remote that nobody reads
   ([0072](0072-remote-environments.md): never fall back to local).
6. **MQTT keeps its job.** Control messages, presence, snapshots and
   terminals are not slowed by bulk file transfer.

### Relevant facts

- MQTT requests are capped at 256 KiB (`envproto.MaxRequestBytes`), and the
  broker closes a connection whose packet exceeds its maximum packet size
  without a DISCONNECT ([0074](0074-mqtt-transport-embedded-broker.md),
  amendments). A 10 MiB file is at least 40 such messages, and 54 once
  base64-encoded into JSON (4/3 inflation)[^b64].
- Each agent has one MQTT connection, shared by control requests and
  terminal streams; the terminal spike measured a control request waiting
  0.195 s behind bulk terminal output on a 20 ms, 10 Mbit/s link, against a
  limit of 0.142 s, and the owner accepted that only for terminals
  ([0074](0074-mqtt-transport-embedded-broker.md), amendments PR 4).
- The controller already serves HTTPS: `POST /v1/enroll` on
  `enroll_listen` (8884), with no client certificate
  (`internal/environment/enroll.go`, `tlsconfig.go` `enrollTLS`). The
  broker listener (8883) requires a client certificate through `brokerTLS`,
  a `GetConfigForClient` that reads the CA trust and server certificate at
  each handshake, so CA rotation needs no restart.
- The agent's certificate is ECDSA P-256, client-auth only, CN = its
  environment id, 90 days (`environment/store.go` `signRecord`); the broker
  checks the CN, the serial against `environments/revoked`, the record's
  state and the issuer in `policy.connect` (`environment/policy.go`).
  `envproto.PeerFromState` turns a `tls.ConnectionState` into that `Peer`.
- Go's TLS 1.3 server with `RequireAndVerifyClientCert` refuses a
  certificate from another CA and an expired one in the handshake; the
  client sees `remote error: tls: certificate required` and `remote error:
  tls: expired certificate` as a `*net.OpError` with `Op == "remote error"`
  on its first request, and `Op == "dial"` when nothing listens. A 10 MiB
  `POST` body over loopback took 15-20 ms[^probe].
- On the vega cluster the broker's ports are plain-TCP LoadBalancer
  services and must never go through the nginx ingress; the pod's
  NetworkPolicy must allow them (handbook, "how a change reaches the vega
  cluster").
- The agent's `agent.toml` is decoded strictly: an unknown key is an error
  (`envagent/files.go` `loadAgentFile`), so a new key there would break an
  older `ws` on the same machine.
- `envproto.Protocol` is 4 ([0082](0082-environment-scopes-for-children.md)).
  An agent's hello carries its protocol; the controller refuses an
  operation whose `Since` is above it (`environment_outdated`).

## Decision

### Transport: HTTPS with mutual TLS, on a third listener

The controller's daemon gains **`[environments] api_listen`**, default
`"0.0.0.0:8885"`, started and stopped with the broker and the enrolment
listener, and off until the first `ws env add` like them. `api_listen = "off"` (or `""`)
starts no API listener; the default applies only when the key is unset. It serves HTTP/1.1
over TLS 1.3 with the broker's TLS configuration (`brokerTLS`): a client
certificate signed by a trusted root is required, the server certificate is
the one for `public_name`, and both are read at each handshake, so CA
rotation ([0075](0075-environment-enrollment-and-certificates.md)) applies
unchanged. It answers only `/v1/artifacts` and below.

**Identity.** Every request runs `policy.connect` on the peer from
`envproto.PeerFromState(r.TLS)` (CN, serial, issuer; client id = CN): a
revoked serial, a removed or pending environment, or an untrusted issuer is
refused with 403 and `artifact_unreachable` (reason `refused`). Go's
handshake already refuses an expired certificate or one from another CA.
The checks run per request, so a revocation applies to the next request on
a kept-alive connection. The CN maps to the environment record; its id and
name are the request's environment.

**Who in the environment.** The request names the calling workspace in
headers: `Ws-Workspace` (its id), `Ws-Workspace-Name` and `Ws-Root` (its
tree's root on the remote), each a valid workspace id
(`artifact.ValidWorkspaceID`). The controller can't verify them beyond the
environment: the trust boundary is the machine, as it is for local agents,
whose caller is whatever workspace their working directory is in.

**Ownership.** A remote agent's artifacts record `root` as
`<environment id>/<root>` (for example `env-1a2b3c4d/plan-auth-5e6f7a8b`).
A `/` can't occur in a workspace id, so a remote tree never matches a local
one or one on another environment. `rm` keeps 0078's rule: only the tree
that created the artifact. `list --mine` compares the same string. The
actor gains an optional `environment` field (the environment's name,
omitted for local actors so their JSON is unchanged), and reads recorded by
`get` carry it as `environment` in `links.json`
([0084](0084-bidirectional-artifact-links.md)).

**Wire.** Paths, all answering JSON in exactly the shape the local command
prints, except the content route:

| Method and path | Body | Answer |
| --- | --- | --- |
| `GET /v1/artifacts?kind=&mine=&limit=` | - | `list`'s JSON |
| `POST /v1/artifacts?name=&title=&note=` | the file's bytes | `add`'s JSON, 201 |
| `GET /v1/artifacts/{id}` | - | `show`'s JSON |
| `GET /v1/artifacts/{id}/content?rev=` | - | the revision's bytes, `application/octet-stream`, with `Ws-Rev`, `Ws-Sha256`, `Ws-Content-Type`; records the read |
| `POST /v1/artifacts/{id}/revisions?base_rev=&note=` | the file's bytes | `put`'s JSON |
| `DELETE /v1/artifacts/{id}` | - | `{"id","removed":true}` |
| `GET /v1/artifacts/{id}/threads?open=` | - | `comment list`'s JSON |
| `POST /v1/artifacts/{id}/threads` | JSON `{body, reply, quote, line}` | `comment add`'s JSON |
| `POST /v1/artifacts/{id}/threads/{thread}/resolve` | JSON `{reopen}` | `comment resolve`'s JSON |

Errors are 4xx/5xx with `{"error": {code, message, details}}`, the
envelope enrolment uses, carrying `ws`'s own codes and details
(`artifact_conflict` with `latest`, `anchor_not_found` with its
occurrences), so the remote command exits as the local one would. File
bodies are raw bytes with a `Content-Length`: no base64 and no chunking. The
controller refuses a `Content-Length` over 10 MiB before reading, reads at
most 10 MiB + 1 bytes through `http.MaxBytesReader` (413,
`artifact_invalid`), then hands the bytes to the store, which applies the
per-kind limits, the quota (`artifact_full`) and its atomic 0600 writes
under the artifact's lock, as for a local caller. At most 4 requests with a
body are in flight (503, `operation_limited`), so the controller holds at
most about 40 MiB of uploads. Server timeouts: headers 10 s, read and write
2 min each, idle 60 s, headers at most 16 KiB.

The downloaded file is written on the remote as `get` writes it locally:
into `<workspace>/.ws-artifacts/<id>/r<n>/<name>` or `--out`, streamed into
a temporary file (0600) in that directory, its size and SHA-256 checked
against `Ws-Rev`'s revision and `Ws-Sha256`, synced, then renamed.

### Discovery: `api.update` over MQTT, kept in `environment/api.toml`

The agent learns the address the way it learns new CA roots: the hello
gains `api_endpoint` (what the agent has, `""` when none), and whenever an
online hello at protocol 5 or later names a different endpoint than the
controller's (`public_name` with `api_listen`'s port), the controller sends
**`api.update`** `{"endpoint": "host:port"}` on `env/<id>/req`. The agent
checks it (a `host:port` with a port from 1 to 65535, at most 255
characters), writes `CONFIG_DIR/environment/api.toml` (`version = 1`,
`endpoint`, `updated_at`; 0600, temporary file renamed), answers
`{"endpoint"}`, and republishes its hello. A separate file, not a key in
`agent.toml`, because `agent.toml` is decoded strictly and an older `ws`
on the machine must still read it. `ws machine artifact` reads `api.toml`,
`agent.toml`'s `server_name` and the key, certificate and roots under
`CONFIG_DIR/environment/` itself, and dials the controller directly: the
environment agent's process isn't in the path, so the file transfer never
touches its MQTT connection, and a reconnecting agent doesn't block it.

Enrolment is unchanged (code `ws-enroll-v1`, `EnrollResponse`): a new
machine gets `api.update` within seconds of its first connect, and machines
enrolled before this ADR get it the same way, with no re-enrolment.

### Versions

`envproto.Protocol` becomes **5**; `ProtocolArtifacts = 5`. `api.update`
is a `CallerInternal` operation with `Since: 5`. **Both the controller and
the remote need the new `ws`:**

| Controller | Remote | What happens |
| --- | --- | --- |
| new | new | works |
| new | older (protocol ≤ 4) | no `api.update` is sent; the remote's `ws machine artifact` refuses as before; `ws claude --env` and `ws machine spawn --env` from the controller warn that the environment runs protocol N and its agent can't read artifacts |
| older | new | no `api.update` arrives, so `api.toml` is missing: `artifact_unreachable`, reason `no_endpoint`, telling the person to upgrade `ws` on the controller and keep `ws env agent run` connected |
| new, port not reachable | new | the dial fails: `artifact_unreachable`, reason `offline`, naming the endpoint and the port |

`artifact_unreachable` (exit 1) keeps its code and gains
`details.reason`: `no_endpoint`, `offline` (dial failure or timeout),
`certificate` (the TLS handshake refused this machine's certificate, or
the controller's didn't verify), `refused` (403 from the identity check: the environment is revoked, removed, or pending)
and `busy` is not a reason (it is `operation_limited`). No new error code.

### References in prompts

`artifactHints` checks every `@artifact:` reference in the prompt before
anything is created: in the local store on the controller, and through the
API on an enrolled remote (`artifact_unknown` for one that doesn't exist).
It appends the hint line in both cases, including for a prompt bound for a
remote (`ws claude --env`, `ws machine spawn --env`); the hint is added once,
since `artifact.AppendHint` skips a prompt that already carries it. When
the remote's check can't reach the controller, it warns and still appends
the hint, so the agent gets a clear `artifact_unreachable` from `get` rather
than a silently dropped reference.

### Deployment (follow-up, not done here)

The controller's port 8885 must be reachable from every remote, like 8883
and 8884. On the vega cluster, in vega-infra: add port 8885/TCP to the
LoadBalancer Service that carries 8883 and 8884 (Cilium LB-IPAM, same
static IP; never the nginx ingress), and allow ingress on 8885 in the
ws-online pod's NetworkPolicy. A controller elsewhere opens or forwards the
port. Until then remotes get `artifact_unreachable`, reason `offline`.

### Why this option

1. **Same commands**: the controller answers with the same JSON the local
   command prints, so the remote prints it as it is; every op is one
   request (two for `get`).
2. **10 MiB**: one streamed body, measured at 15-20 ms on loopback; no
   base64, no reassembly, no 256 KiB cap.
3. **No new credential**: the enrolment certificate, verified against the
   same trust and checked by the same `policy.connect` as the broker; CA
   rotation, renewal and `ws env rm` apply without new code.
4. **Store rules**: the controller's existing store does every write, so
   locking, atomic 0600 writes, limits and quota are the local ones; the
   owner check gains only the environment prefix.
5. **Clear failures**: each cause has its own reason; nothing falls back to
   the remote's own store.
6. **MQTT untouched**: bulk bytes go on their own TCP connection, so they
   can't add to the head-of-line delay the terminal spike measured.

Where it's weak: a third port to open, which is a deployment step (above)
and one more thing a firewall can block; the workspace headers are
trusted within an environment; and the remote command needs the key and
certificate readable, which they are (same user, 0600), but a renewal that
replaces them mid-read makes the command retry once.

## Alternatives

### Chunked `artifact.*` requests on the agent's `up` topic (0078's sketch)

**Pros**

- No new port or deployment change; the MQTT connection is already up.

**Cons**

- 54 or more base64 JSON messages per 10 MiB file, each acknowledged, with
  reassembly, ordering, retry and a temporary area on both sides: a second
  transport built inside MQTT.
- Shares the one connection with control requests and terminals, whose
  delay behind bulk output already misses its limit on slow links.
- Every chunk passes through the agent process and the broker's 256-message
  queue; a dropped agent loses the transfer.

### Serve the API on the enrolment listener (8884) with `VerifyClientCertIfGiven`

**Pros**

- No new port; the agent already knows the address from its code.

**Cons**

- The enrolment listener is deliberately single-purpose and
  unauthenticated, with 10 s timeouts and a 64 KiB body cap tuned for
  enrolment (0075: "answers nothing else"); mixing optional client
  certificates into it makes every route's authentication a handler's job
  and widens what an unauthenticated peer can reach.

### Multiplex HTTP onto the broker port (8883) by ALPN

**Pros**

- No new port.

**Cons**

- The broker (mochi) owns that listener; splitting connections by ALPN
  before it needs a custom listener in front of it and changes what 0074's
  spike verified.

### Relay through the environment agent's socket

The CLI would send to `ws env agent run`, which would proxy to the
controller.

**Pros**

- Only the agent process reads the key.

**Cons**

- Adds a hop and a proxy that must stream 10 MiB, and fails whenever the
  agent process is restarting, though the controller is reachable. The key
  is the same user's file either way.

## Consequences

- Controller side: `internal/environment` gains the API listener, the
  identity check and `api.update`; `internal/cli` the handlers over the
  store; `[environments] api_listen` (config, `ws config`, docs);
  `ws daemon status` shows the address. Agent side: `internal/envagent`
  gains `api.toml`, the `api.update` handler and an HTTPS client;
  `ws machine artifact` and `artifactHints` gain the remote path.
- `envproto.Protocol` is 5; tests that pin it (`envproto/scope_test.go`)
  and the operation table (`envproto/ops_test.go`) change.
- `artifact.Actor` gains `environment` (omitted when empty);
  `artifact.json` stays version 1, since older readers ignore the field.
- `docs/state.md` gains `environment/api.toml`; `docs/tools/ws-machine.md`,
  `ws-artifact.md`, `ws-env.md`, `docs/config.md` and the `ws llm` guides
  drop "refused on a remote".
- The wsenv audit's artifact row is corrected (it said OK).
- Conformance (Go tests): an end-to-end test where an enrolled agent's
  `ws machine artifact` commands reach a real controller's store over the
  mTLS listener and back; handshake refusals for another CA, an expired
  certificate and no certificate; 403 for a revoked serial and a removed
  environment; 413 just over 10 MiB and success at exactly 10 MiB;
  `offline`, `no_endpoint` and protocol-4 cases.
- Unverified: the port through the vega cluster's LoadBalancer and
  NetworkPolicy (the deployment follow-up); throughput over a real WAN.
- Not done: recording citations from a spawn **on** a remote (0084's
  `cited` links); a remote's `ws artifact` people command, which still uses
  its own store.

## References

[^b64]: RFC 4648, "The Base16, Base32, and Base64 Data Encodings," §4. <https://www.rfc-editor.org/rfc/rfc4648#section-4> (accessed 2026-11-27). "The encoding process represents 24-bit groups of input bits as output strings of 4 encoded characters."
[^probe]: Measured 2026-11-27 in the ws-online pod, linux/amd64, go1.27.3: an `httptest` TLS 1.3 server with `RequireAndVerifyClientCert` and a throwaway CA; one run each of a valid client certificate with a 10 MiB `POST` body (200, 20 ms and 15 ms in two runs), a certificate from another CA, an expired one, none, and a closed server. Loopback only.
