# Broker spike: embedding an MQTT 5 broker in the `ws` daemon

Date: 2026-11-22. Scope: the "Unverified" items of
[ADR 0074](../../adrs/0074-mqtt-transport-embedded-broker.md) and the broker
side of [ADR 0075](../../adrs/0075-environment-enrollment-and-certificates.md).
Everything below was run on loopback on one Linux box (go1.27.3 linux/amd64)
in a scratch module outside the repo; no repo file other than this one changed.

## Recommendation

**Use `github.com/mochi-mqtt/server/v2 v2.7.9` as the broker and
`github.com/eclipse/paho.golang v0.23.0` as the client.** All REQUIRED items
pass, with three things the ADRs get wrong (presence timing, session expiry,
"disconnect on violation") and two workarounds we must build into the hook
(same-id takeover race, will-topic ACL). The second candidate was not needed.
The main risk is upkeep, not function: see [Facts](#facts-item-9).

## Results

| # | Item | Result | Evidence (details below) |
| --- | --- | --- | --- |
| 1 | mTLS listener, hook reads CN and serial | PASS | No cert and a foreign-CA cert both refused; hook saw `cn=env-aaaa serial=<hex>` equal to the issued serial |
| 2 | Per-CN ACL, pub and sub, disconnect | PASS, with hook work | 9 violation kinds each ended in DISCONNECT `0x87`; env-bbbb received nothing; claimed id/username ignored |
| 3 | Response Topic, Correlation Data | PASS | Binary correlation data, response topic, user property and expiry intact both ways |
| 4 | Will delay 10 s, retained replaced, keep-alive | PARTIAL (PASS with session expiry >= 10 s) | Works only with session expiry >= 10 s (0 fires at once). Silent drop: broker notices at 45.0 s, `offline` at 56.0 s |
| 5 | Revocation by serial | PASS | `Revoke(serial)` disconnected the live client in under 1 ms; same cert refused after; a new cert with the same CN works |
| 6 | Expiry, session expiry 0, size, receive max | PASS (one gap) | Expiry honoured; session dropped; 256 KiB passes, larger closes the connection; Receive Maximum enforced both ways. Server does not advertise Maximum Packet Size |
| 7 | Controller client on a non-public listener | PASS | Loopback mTLS listener `ctl` with its own CN, plus mochi's inline client |
| 8 | Performance | PASS | Median 0.58 ms request/response; flood does not slow the fast subscriber or control traffic; memory needs a cap |
| 9 | Facts | PASS, maintenance risk | MIT; v2.7.9 on 2025-03-01; two new indirect deps; builds and vets on go 1.27 |

## Versions

- go1.27.3 linux/amd64; scratch `go.mod` says `go 1.27`.
- `github.com/mochi-mqtt/server/v2 v2.7.9`, `github.com/eclipse/paho.golang v0.23.0`.
- After `go mod tidy` the only other modules are `github.com/gorilla/websocket v1.5.3`
  and `github.com/rs/xid v1.4.0` (both indirect).
- Paho client: `paho.NewClient` over a `tls.Conn` wrapped in `packets.NewThreadSafeConn`.
  `autopaho` was not used.

## Hook and API names

mochi has no ACL file we use: everything is a `mqtt.Hook` (embed
`mqtt.HookBase`, list the hooks in `Provides`, add with `server.AddHook`).

- **Item 1 (identity).** `OnConnectAuthenticate(cl, pk) bool`. The listener is
  `listeners.NewTCP(listeners.Config{ID, Address, TLSConfig})` with
  `ClientAuth: tls.RequireAndVerifyClientCert`; `cl.Net.Conn` is the
  `*tls.Conn`, so `cl.Net.Conn.(*tls.Conn).ConnectionState().VerifiedChains[0][0]`
  gives `Subject.CommonName` and `SerialNumber`. `cl.Net.Listener` tells which
  listener the client came in on. Handshake is complete by then.
- **Item 2 (ACL).** `OnACLCheck(cl, topic, write bool) bool` for publish
  (`write=true`) and for subscribe filters and per-message delivery
  (`write=false`). Return false **and** call
  `server.DisconnectClient(cl, packets.ErrNotAuthorized)`; mochi alone only
  drops a QoS 0 publish silently, answers a QoS 1 publish with PUBACK `0x87`,
  and answers a bad subscribe with SUBACK `0x87`. Also needed:
  - set `cl.ID = CN` and clear `cl.Properties.Username` in
    `OnConnectAuthenticate`, and key the ACL on `cl.ID` (not on a map of live
    connections: mochi also calls `OnACLCheck` for deliveries to disconnected,
    persisted sessions, and a map lookup then denies and disconnects them);
  - `OnConnect(cl, pk) error`: check the **will topic** with the publish ACL.
    mochi publishes a will without any ACL check (`sendLWT` calls only
    `OnWill`), so without this a client can set a will on another's `status`.
    Returning an error closes the connection without a CONNACK (client sees EOF);
  - publishes to `$`-prefixed topics are dropped by mochi before the ACL runs
    (no disconnect); wildcard filters are checked as filters (a filter is
    allowed only if it is inside an allowed filter; `#`, `env/#`, `env/+/req`
    and `$share/...` were all refused).
- **Item 5 (revocation).** Two layers: `tls.Config.VerifyConnection` rejects a
  revoked serial in the handshake (so a revocation file is read there), and a
  `Revoke(serial)` method walks the live clients and calls
  `server.DisconnectClient(cl, packets.ErrNotAuthorized)`. Live clients are
  found through the hook's own `map[*mqtt.Client]Identity` filled in
  `OnConnectAuthenticate` and cleared in `OnDisconnect`. With TLS 1.3 the client
  gets no error from `tls.Dial`: the failure shows on its first write or read
  (`broken pipe` or `EOF`), so an agent must treat a failed CONNECT as a refusal.
  `server.Clients.GetAll()` also works for lookup, but has no certificate.
- **Item 7 (controller).** Two ways, both tested:
  1. a second listener bound to `127.0.0.1` (`ctl`) whose hook accepts only CN
     `ws-controller` (and the public listener refuses that CN): the controller
     connects with paho and its own certificate. Several controller connections
     need distinct ids: the hook sets `cl.ID = "ctl:" + claimed id`;
  2. `mqtt.Options{InlineClient: true}`: `server.Subscribe(filter, id, fn)`
     receives messages with all properties; `server.Publish(topic, payload,
     retain, qos)` cannot set a Response Topic or Correlation Data; use
     `cl := server.NewClient(nil, "x", "id", true)` with
     `cl.Properties.ProtocolVersion = 5` and `server.InjectPacket(cl, pkt)` to
     publish with properties. The inline client has no certificate and skips
     `OnACLCheck`. Prefer way 1 (it exercises the same path as agents).

## Measured results

### Items 1 to 3

- Without a client certificate and with one from another CA the connection
  fails right after the handshake (`write: broken pipe`).
- Hook log: `connect cn=env-aaaa serial=e1e7f4efe6af09fe6c53bda63 listener=agents`,
  the serial printed by the issuer was identical.
- A client with cert CN `env-aaaa` that sent ClientID `env-bbbb` and
  Username `ws-controller`: the broker's client list was
  `[env-aaaa env-bbbb inline]` (the real env-bbbb was not displaced); ACLs
  followed the certificate.
- Allowed: sub `ws/v1/env/env-aaaa/req`, `.../stream/+/in`; pub `status`,
  `snapshot`, `res/r1`, `stream/s1/out`. Each of these ended in DISCONNECT
  `0x87` (Not authorized): pub `env-bbbb/status` (QoS 0), pub `env-bbbb/res/x`
  (QoS 1), pub own `req`, sub `env-bbbb/req`, sub own `status`, sub `#`, sub
  `ws/v1/env/#`, sub `ws/v1/env/+/req`, sub `$share/g/...`. A will on
  `env-bbbb/status` was refused.
- Request/response: controller (ctl listener) to agent and back, 9 bytes of
  binary Correlation Data (`000102fffe636f7272`), Response Topic, a user
  property and Message Expiry (30) arrived unchanged.

### Item 4 (presence)

| Case | Measured |
| --- | --- |
| Unclean kill (TCP RST), will delay 10 s, **session expiry 0** | `offline` after **0.00 s** |
| Unclean kill, will delay 10 s, **session expiry 30** | `offline` after **10.99 s** (mochi checks delayed wills on a 1 s tick) |
| Clean DISCONNECT 0x00 (will discarded), delay 10 | no `offline` in 14 s |
| Kill, reconnect after 4 s, delay 10 | no `offline` (reconnect cancels the will) |
| Silent drop (proxy stops forwarding, sockets stay open), keep-alive 30 | broker noticed at **45.0 s** (`i/o timeout`), `offline` at **56.0 s** |
| Retained `online` | replaced by retained `offline`; a late subscriber got `offline` with the retain flag |

Why: MQTT 5 (3.1.3.2.2) publishes the will when the delay passes **or the
session ends, whichever is first**, and a session with expiry 0 ends when the
network connection closes. mochi follows this. mochi's keep-alive grace is
1.5 times the client's value.

### Item 6

- **Message Expiry.** A QoS 1 session (expiry 120 s) went offline, three
  messages were published (expiry 2 s, 60 s, none), 4 s later the client
  reconnected with CleanStart=false: the 2 s message was gone; the others were
  delivered with remaining expiry `56` and `86396`. A message with no expiry
  gets mochi's 24 h cap (`MaximumMessageExpiryInterval`), so always set it.
- **Session expiry 0.** After a clean disconnect the broker held no client and
  no subscription; reconnect with CleanStart=false returned session-present
  false and no queued message.
- **Maximum Packet Size.** Set to 256 KiB + 1 KiB. A 256 KiB payload was
  delivered. A 263 178 byte payload (sent on a raw connection) closed the
  connection (read `EOF`, no DISCONNECT packet) and was not delivered. The
  CONNACK did **not** carry the Maximum Packet Size property (nil), so clients
  can't learn the limit; the limit counts the whole packet, so allow headroom
  for topic and properties.
- **Receive Maximum.** Server set to 2: CONNACK advertised 2; a third
  unfinished QoS 2 publish got DISCONNECT `0x93`. A client advertising 1 that
  never acked got exactly 1 of 5 QoS 1 messages. (QoS 1 inbound never hits the
  limit because mochi acks immediately.)

### Item 8 (loopback, TLS 1.3, one process)

Request/response, 1000 sequential round trips (controller to agent and back,
QoS 1 both ways, through the broker, TLS 1.3):

| Request size | Median | p99 | Max |
| --- | --- | --- | --- |
| 64 B | 0.58 ms | 1.05 ms | 1.66 ms |
| 16 KiB | 0.76 ms | 1.45 ms | 3.47 ms |
| 256 KiB | 1.76 ms | 3.50 ms | 4.02 ms |

QoS 0 flood: one agent publishes 16 KiB messages as fast as it can for 10 s;
a fast subscriber (counts and drops) and a slow subscriber (a raw connection
that subscribed and then never read) are both subscribed; in parallel a
separate agent/controller pair runs sequential QoS 1 request/response.

| | Default `MaximumClientWritesPending` (8192) | 256 |
| --- | --- | --- |
| Published | 251 536 msgs (393 MiB/s) | 197 892 msgs (309 MiB/s) |
| Fast subscriber got | 100.0 % | 100.0 % |
| Slow subscriber got | 8 451 (rest dropped by broker, `MessagesDropped=243085`) | 515 (`MessagesDropped=197377`) |
| Control RTT during flood (median / p99 / max) | 0.43 / 1.94 / 5.4 ms (19 799 trips) | 0.48 / 1.74 / 4.9 ms (16 664 trips) |
| Baseline control RTT | 0.56 / 1.02 ms | 0.62 / 1.26 ms |
| Heap in use, before / peak | 8 / **584 MiB** | 7 / **46 MiB** |
| RSS, before / peak | 19 / 620 MiB | 18 / 59 MiB |

The slow subscriber does not block the publisher, the fast subscriber or
control traffic; mochi drops QoS 0 for it once its per-client queue
(`Capabilities.MaximumClientWritesPending`, default 8192 packets) is full, and
that queue is where the memory goes (8192 x 16 KiB per slow client). Set it
to about 256 (4 MiB of 16 KiB chunks) for terminals. The numbers include the
paho clients, which share the process. A second full run with the final hook
gave 0.61 / 0.61 / 1.95 ms medians for the three sizes and a 590 MiB peak heap
at the default queue, so run-to-run spread is about 10 %. Not measured: a slow client on the
*same* connection as control traffic (the ADR's head-of-line concern), QoS 1
traffic to a slow client, and anything over a real network.

### Same-id reconnect (found during the spike)

The agent id is its CN, so a reconnect while the broker still holds the old
connection (a half-open NAT mapping lasts up to 45 s) is a session takeover.
With mochi's own takeover, 300 rounds (100 kill, 100 clean, 100 takeover of a
live connection) lost a message in 0 rounds in the one plain run but in 5 to 10
rounds under `-race`, all of them takeovers of a live connection (the new client ends up not receiving: mochi
issue #562, still open). Doing the takeover in `OnConnectAuthenticate`
(disconnect the old client, wait for its `OnDisconnect`, then
`UnsubscribeClient`, `ClearInflights`, `Clients.Delete`; see `takeOver` in the
appendix) gave 0 lost in 7 race runs and 1 plain run (session expiry 0 and 30). One earlier `-race` run of this test did not finish in over 4 minutes and
I killed it; 11 later runs finished, so I could not reproduce or explain the
hang (mochi issue #488 describes a deadlock in `clients.go`).

## Facts (item 9)

- **License:** mochi MIT; paho.golang EPL-2.0 (headers say `EPL-2.0 OR BSD-3-Clause`);
  gorilla/websocket BSD-2-Clause; rs/xid MIT.
- **Releases:** mochi v2.7.9 was tagged 2025-03-01, and `main`'s last commit
  is the same day (the repository was last pushed 2026-09-08). The project's
  own issues ask "Is the project still maintained?" (#515, #491) and a
  contributor has 23 PRs waiting for a 2.8.0 (#538). paho v0.23.0: 2025-09-07.
- **Open issues that touch us (18 matched "mqtt5/tls/will"):** #562 closing
  connection can delete the new client with the same id (v2.7.9; worked
  around, above); #557 DISCONNECT with remaining length 1 loses its reason
  code so 0x04 "disconnect with will" discards the will; #559 Request Problem
  Information 0 strips user properties; #560 takeover resets send quota;
  #510 QoS 2 rejected via `OnPublish` gets PUBACK; #500 topic alias; #488
  deadlock in `GetByListener`; #404 `OnConnectAuthenticate` cannot set a
  CONNACK code. None blocks the design; #562 and #488 matter for the daemon.
- **go.mod footprint:** `go mod tidy` leaves 2 direct requirements (mochi, paho)
  and 2 indirect (`gorilla/websocket`, `rs/xid`). (mochi's own `go.mod`
  lists redis, pebble, badger and bolt for optional storage hooks: they are
  not built unless imported, and not in our `go.mod`.) None of the four is in
  the repo's `go.mod` today.
- **Build and vet on go 1.27.3:** `go build ./...` and `go vet` pass for the
  mochi packages we use (`server`, `listeners`, `packets`, `hooks/auth`) and
  for the spike; `go build -race` works. `go vet github.com/mochi-mqtt/server/v2/...`
  fails only because optional storage packages lack `go.sum` entries, and
  vetting paho's `packets` test files reports `result of fmt.Sprintln call not
  used` (a paho test file; not in our build).

## What the findings contradict in ADR 0074 and 0075

| ADR text | Corrected fact |
| --- | --- |
| 0074 Presence: "Last Will with a 10 s delay publishes `offline` ... Sessions don't persist (session expiry 0)" | With session expiry 0 the will is published at once when the connection closes (0.00 s measured). Use Session Expiry Interval >= 10 s (spike used 30) and CleanStart=true on every connect so nothing is resumed. |
| 0074 Presence: "a machine that loses power is `offline` within about 45 s" | Measured 56.0 s: 45 s (1.5 x keep-alive) to notice, plus 10 s delay, plus up to 1 s tick. For about 45 s use keep-alive 20 s and delay 10 s (30 + 11 s), or accept 56 s. |
| 0074 Identity: "the broker takes the MQTT client id ... from [the certificate]" | mochi uses the claimed id. The hook must overwrite `cl.ID` in `OnConnectAuthenticate`, or a client could claim another's id and take over its session (by code, `inheritClientSession` keys on id; not run without the rewrite). |
| 0074 ACL: "A client that violates an ACL is disconnected and the event logged" | mochi by default drops a QoS 0 publish silently and answers QoS 1 publish and subscribe with reason `0x87`. The hook must call `DisconnectClient` itself. Will topics are not ACL-checked by mochi (check in `OnConnect`); `$`-topic publishes are dropped silently. |
| 0074 Requests: "at most 256 KiB" (bounded sizes) | Enforced by `Capabilities.MaximumPacketSize` (whole packet, not payload), but not advertised in CONNACK, and the offender is closed with no DISCONNECT code. Clients must enforce their own cap. |
| 0074 Relevant facts: mochi "v2.0.0 to at least v2.4.x" | Latest is v2.7.9 (2025-03-01); the project looks stalled (see Facts). |
| 0074 Streams: "Output the controller isn't reading is dropped by the agent after 1 MiB" | The broker also drops, silently, per slow subscriber (8192 packets by default = 128 MiB of 16 KiB chunks); set `MaximumClientWritesPending` to about 256. The sequence-number gap check is therefore required, not optional. |
| 0074 Listeners: controller "connects in-process or over a loopback listener with its own certificate" | In-process (inline) client has no certificate, bypasses the ACL and its plain `Publish` can't carry Response Topic or Correlation Data. Use the loopback mTLS listener. |
| 0075 Revocation: "refuses its certificate serial ... and disconnects it now" | Works (handshake check plus `DisconnectClient`), but the refused agent sees only `broken pipe`/`EOF` at its first read (TLS 1.3), not a TLS error from dial. `ws env rm` should also delete the persisted session. |
| 0075 CA rotation (broker trusts two roots) | Not tested here; `tls.Config.ClientCAs` takes a pool with two certs, but that was not run. |

## Caveats

- Everything is loopback, one process; no real network or NAT. The
  silent-drop test used a TCP proxy that stops copying.
- Timings of wills have 1 s granularity (mochi's ticker).
- Not tested: CA rotation with two roots, certificate expiry on a live
  connection,
  shared subscriptions, QoS 2 to a slow client, a slow client sharing a
  connection with control traffic, persistence hooks, memory under many agents.
- The fallback broker was not needed, so none was tried.
- Scratch code is about 1 300 lines (`brk/` harness plus five test files); the
  appendix keeps the parts needed to reproduce.

## Appendix: code to reproduce (trimmed from the scratch module)

Scratch module: `go 1.27`, `require mochi v2.7.9, paho v0.23.0`. Imports omitted; helper names (`track`, `markRevoked`, `waitUntilGone`...) are condensed
from the scratch code, the hook logic is as run.
`ca.Client(cn)` issues an ECDSA P-256 client certificate (90 days, client-auth).

### Broker: listeners, hook, takeover, revocation

```go
// two mTLS listeners on loopback; the second is the "controller" listener
tcfg := &tls.Config{Certificates: []tls.Certificate{srv}, ClientCAs: ca.Pool,
    ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13,
    VerifyConnection: func(cs tls.ConnectionState) error { // revoked serial: refuse in the handshake
        if b.isRevoked(cs.PeerCertificates[0].SerialNumber.Text(16)) { return errors.New("revoked") }
        return nil }}
caps := mqtt.NewDefaultServerCapabilities()
caps.MaximumPacketSize = 256*1024 + 1024; caps.MaximumClientWritesPending = 256
s := mqtt.New(&mqtt.Options{Capabilities: caps, InlineClient: true})
s.AddHook(&hook{b: b}, nil)
s.AddListener(listeners.NewTCP(listeners.Config{ID: "agents", Address: addr, TLSConfig: tcfg}))
s.AddListener(listeners.NewTCP(listeners.Config{ID: "ctl", Address: "127.0.0.1:0", TLSConfig: tcfg.Clone()}))
s.Serve()

func (h *hook) Provides(x byte) bool {
    return bytes.Contains([]byte{mqtt.OnConnect, mqtt.OnConnectAuthenticate, mqtt.OnACLCheck, mqtt.OnDisconnect}, []byte{x})
}

func (h *hook) OnConnectAuthenticate(cl *mqtt.Client, pk packets.Packet) bool {
    tc, ok := cl.Net.Conn.(*tls.Conn)
    if !ok { return false }
    cs := tc.ConnectionState()
    if len(cs.VerifiedChains) == 0 { return false }
    crt := cs.VerifiedChains[0][0]
    cn, serial := crt.Subject.CommonName, crt.SerialNumber.Text(16)
    switch {
    case h.b.isRevoked(serial),
        cl.Net.Listener == "agents" && !envCN.MatchString(cn),   // ^env-[a-z0-9]+$
        cl.Net.Listener == "ctl" && cn != "ws-controller":
        return false
    }
    if cn == "ws-controller" { cl.ID = "ctl:" + cl.ID } else { cl.ID = cn } // claimed id ignored
    cl.Properties.Username = nil                                            // claimed username ignored
    if cn != "ws-controller" { h.b.takeOver(cl.ID) }
    h.b.track(cl, Identity{cn, serial, cl.Net.Listener})                    // for Revoke
    return true
}

func (h *hook) OnConnect(cl *mqtt.Client, pk packets.Packet) error { // will topic needs the publish ACL
    if crt, ok := peer(cl); ok && pk.Connect.WillFlag &&
        !h.b.allowed(crt.Subject.CommonName, pk.Connect.WillTopic, true) {
        return errors.New("will topic not allowed")
    }
    return nil
}

func (h *hook) OnACLCheck(cl *mqtt.Client, topic string, write bool) bool {
    if h.b.allowed(cl.ID, topic, write) { return true } // cl.ID is the verified CN, also for offline sessions
    h.b.S.DisconnectClient(cl, packets.ErrNotAuthorized)
    return false
}

func (b *Broker) allowed(id, topic string, write bool) bool {
    if strings.HasPrefix(id, "ctl:") { return strings.HasPrefix(topic, "ws/v1/") }
    p := "ws/v1/env/" + id + "/"
    if write { return covered(topic, []string{p + "status", p + "snapshot", p + "res/#", p + "stream/+/out"}) }
    return covered(topic, []string{p + "req", p + "stream/+/in"})
}
// covered: request (topic or filter) lies inside an allowed filter, segment by segment:
// literal must equal or meet "+"; requested "+" needs allowed "+"; requested "#" needs allowed "#".

func (b *Broker) Revoke(serial string) int { // outside the connection; also add to the revocation list
    b.markRevoked(serial)
    n := 0
    for _, cl := range b.clientsWithSerial(serial) { b.S.DisconnectClient(cl, packets.ErrNotAuthorized); n++ }
    return n
}

func (b *Broker) takeOver(id string) { // avoids mochi issue #562
    old, ok := b.S.Clients.Get(id)
    if !ok { return }
    b.S.DisconnectClient(old, packets.ErrSessionTakenOver)
    b.waitUntilOnDisconnectRan(old, 2*time.Second) // the hook removes it from the tracked set
    if old.Properties.Props.SessionExpiryInterval == 0 { b.waitUntilGone(id, 2*time.Second); return }
    b.S.UnsubscribeClient(old); old.ClearInflights(); b.S.Clients.Delete(id)
}
```

### Presence: will delay, unclean kill, silent drop

```go
conn := &paho.Connect{ClientID: "a", CleanStart: true, KeepAlive: 30,
    Properties: &paho.ConnectProperties{SessionExpiryInterval: u32(30)}, // 0 => will fires at once
    WillMessage: &paho.WillMessage{Topic: "ws/v1/env/env-aaaa/status", Payload: []byte("offline"), QoS: 1, Retain: true},
    WillProperties: &paho.WillProperties{WillDelayInterval: u32(10)}}
// agent publishes retained "online"; watcher (controller on ctl) subscribed to status.
tcp := tlsConn.NetConn().(*net.TCPConn); tcp.SetLinger(0); tcp.Close() // RST, no DISCONNECT
// measure time until the watcher receives "offline"  -> 10.99 s

// silent drop: the client dials this proxy instead of the broker (TLS stays end to end)
func (p *proxy) pipe(dst, src net.Conn) {
    buf := make([]byte, 32<<10)
    for { n, err := src.Read(buf); if n > 0 && !p.stop.Load() { dst.Write(buf[:n]) }; if err != nil { return } }
}
// p.stop.Store(true) swallows both directions, sockets stay open;
// OnDisconnect fired at +45.0 s (read i/o timeout), watcher got "offline" at +56.0 s
```

### Flood: slow subscriber and control traffic

```go
slow := rawTLSConn(ctl, "ws-controller")   // packets.CONNECT v5, then SUBSCRIBE ws/v1/env/env-aaaa/stream/+/out QoS 0
                                           // ...and never reads again
fast.Sink.Store(true)                      // paho client that only counts bytes
for !stop { pub.Publish(nil, &paho.Publish{Topic: "ws/v1/env/env-aaaa/stream/s1/out", QoS: 0, Payload: make([]byte, 16<<10)}) }
// in parallel: sequential QoS 1 request/response env-bbbb <-> controller (Response Topic + Correlation Data)
// memory: runtime.ReadMemStats HeapInuse and /proc/self/status VmRSS sampled every 200 ms
```
