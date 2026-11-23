# Terminal spike: latency, throughput, slow reader, memory

**Verdict: NO-GO on the head-of-line criterion; GO on echo latency,
throughput and broker memory.** Keystroke echo and throughput over the
embedded broker are well within [D1](tasks-ui.md)'s thresholds, and broker heap
growth is far below 8 MiB per stream. But a control request (the stand-in for
`workspace.list`) sent while one stream saturates the agent's single TLS
connection takes about 2 s on the 20 ms profile, against a limit of
2 × idle + 100 ms (about 142 ms), and a slow in-process handler that sleeps
inline stalls the agent's read loop (1.7 s on loopback). Under D1 this is the
"echo or head-of-line fails, memory passes" branch: **a second MQTT connection
per agent for streams** (client id `<env-id>.t`). The non-blocking handler (a
bounded channel) is required and works.

## Setup

- Harness: `internal/mqttbroker/termspike_test.go` and `throttle_test.go`,
  both behind the `spike` build tag. The real broker (`mqttbroker.New`,
  mochi v2.7.9) with a real mTLS agent connection (paho v0.23.0 through
  `internal/mqttclient`), the controller side as the broker's in-process
  `Publish` and `Subscribe`.
- The agent connects through an in-process TCP proxy that delays each
  direction by RTT/2 and paces bytes with a token bucket. Its socket buffers
  are set to 32 KiB and it holds at most 16 chunks of 4 KiB.
- Frames are the placeholder format: 8-byte big-endian sequence number plus
  payload. Input is QoS 1 on `stream/<sid>/in`, output QoS 0 on
  `stream/<sid>/out`.
- Machine: AMD Ryzen Threadripper 3960X, 48 threads, Linux 6.18, `go1.27.3
  linux/amd64`. The pod shares the machine with other jobs.
- Commit: see `git log` for `feat/env-ui-t1`.
- Command: `go test -tags spike -count=1 -run '^TestSpike_' -v -timeout 15m
  ./internal/mqttbroker/` took 411 s. The three echo profiles run in parallel
  (each is 100 s at 10 keys/s), so their CPU use overlaps slightly.

## 1. Keystroke echo round trip

1000 keys at 10 per second; controller publishes QoS 1, agent echoes at QoS 0.

| Profile | RTT | p50 | p95 | p99 | max | D1 limit (p50 / p99) |
| --- | --- | --- | --- | --- | --- | --- |
| loopback | 0 | 0.27 ms | 0.36 ms | 0.43 ms | 1.30 ms | n/a |
| 20ms-10mbit | 20 ms | 20.77 ms | 20.90 ms | 21.06 ms | 21.93 ms | 35 ms / 80 ms |
| 80ms-2mbit | 80 ms | 81.57 ms | 82.41 ms | 82.90 ms | 86.26 ms | n/a |

All 1000 keys echoed on every profile. The broker adds under 1 ms to the link
round trip.

## 2. Throughput, 16 KiB QoS 0 chunks, 10 s

Received bytes are counted inside the 10 s window.

| Profile | Sent frames | Received frames | Lost (gaps) | MiB/s | Share of link | D1 limit |
| --- | --- | --- | --- | --- | --- | --- |
| loopback | 169107 | 169107 | 0 | 263.81 | n/a | n/a |
| 20ms-10mbit | 943 | 836 | 0 | 1.18 | 99.2 % | ≥ 70 % |
| 80ms-2mbit | 338 | 168 | 0 | 0.24 | 98.9 % | n/a |

The sent-minus-received difference on the throttled links is data still queued
in socket buffers and the proxy when the window closed (all of it arrived
during the drain second; no gaps).

## 3. Slow reader

The controller's in-process handler is `fast` (counts only), `inline` (sleeps
5 ms in the broker's callback) or `buffered` (a channel of cap 64, drained by a
goroutine that sleeps 5 ms; a full channel drops the frame). While the agent
saturates one stream, the controller sends 5 control requests (QoS 1 `req/<n>`,
agent answers QoS 1 `res/<n>`, 200 ms apart); the table is how long the
controller waited. "Answered" counts replies within 10 s.

| Profile | Idle p50 | Handler | Answered | p50 under load | max under load | Dropped by handler | Gaps seen |
| --- | --- | --- | --- | --- | --- | --- | --- |
| loopback | 0.44 ms | fast | 5/5 | 18.87 ms | 23.09 ms | 0 | 0 |
| loopback | 0.44 ms | inline | 5/5 | 1742.50 ms | 1819.52 ms | 0 | 0 |
| loopback | 0.44 ms | buffered | 5/5 | 18.66 ms | 21.86 ms | 37857 | 37745 |
| 20ms-10mbit | 20.94 ms | fast | 5/5 | 2002.98 ms | 2146.78 ms | 0 | 0 |
| 20ms-10mbit | 20.94 ms | inline | 5/5 | 1976.87 ms | 2241.99 ms | 0 | 0 |
| 20ms-10mbit | 20.94 ms | buffered | 5/5 | 1951.08 ms | 2506.11 ms | 0 | 0 |
| 80ms-2mbit | 82.22 ms | fast | 2/5 | 7550.63 ms | 7550.63 ms | 0 | 0 |
| 80ms-2mbit | 82.22 ms | inline | 2/5 | 9809.50 ms | 9809.50 ms | 0 | 0 |
| 80ms-2mbit | 82.22 ms | buffered | 3/5 | 9572.09 ms | 9905.19 ms | 0 | 0 |

(The p50 and max columns cover only the answered requests.)

What the numbers say:

- **The broker never dropped a frame.** The only gaps are the ones the
  bounded channel made on loopback; "gaps seen" equals the handler's drops
  within a few frames' timing. The `MaximumClientWritesPending = 256` limit
  applies to subscriber connections, and the in-process handler isn't one, so
  this test doesn't exercise it.
- **An inline slow handler blocks the agent's read loop.** On loopback a
  control request waits 1.7 s with the handler sleeping inline, 19 ms with a
  non-blocking handler. The handler must hand off to a bounded buffer, as D2
  already specifies.
- **A saturated stream delays control traffic on the same connection**,
  whatever the handler: 2.0 s on the 20 ms profile even with `fast`, so the
  delay is queueing behind the stream's bytes in the sender's socket buffer,
  the paced link and the proxy, not the broker. At 2 Mbit/s it passes 7 s.
  The keep-alive (30 s) rides the same connection, and its delay is bounded by
  these same figures: none came near the 45 s that would drop the connection.
- The probe measures a flooding sender (the stream never pauses). A pty that
  produces output in bursts queues far less, but the agent's design allows up
  to 1 MiB queued, which is about 0.8 s on this link.

## 4. Broker memory

`runtime.HeapInuse` after a forced GC (before and after) and the maximum of
20 samples over 5 s (during). It covers the whole test process: the broker,
the agent's paho client, the proxy and the test. Streams are saturated to an
inline slow reader.

| Profile | Streams | Before | Peak | After | Growth | Per stream | D1 limit per stream |
| --- | --- | --- | --- | --- | --- | --- | --- |
| loopback | 1 | 3.5 MiB | 4.6 MiB | 2.9 MiB | 1.1 MiB | 1.1 MiB | 8 MiB |
| loopback | 4 | 3.4 MiB | 6.3 MiB | 3.4 MiB | 2.9 MiB | 0.7 MiB | 8 MiB |
| loopback | 8 | 4.0 MiB | 7.8 MiB | 4.0 MiB | 3.8 MiB | 0.5 MiB | 8 MiB |
| 20ms-10mbit | 1 | 2.9 MiB | 5.0 MiB | 2.9 MiB | 2.0 MiB | 2.0 MiB | 8 MiB |
| 20ms-10mbit | 4 | 3.5 MiB | 6.4 MiB | 3.8 MiB | 2.9 MiB | 0.7 MiB | 8 MiB |
| 20ms-10mbit | 8 | 4.3 MiB | 8.0 MiB | 4.4 MiB | 3.8 MiB | 0.5 MiB | 8 MiB |
| 80ms-2mbit | 1 | 3.0 MiB | 5.4 MiB | 3.0 MiB | 2.4 MiB | 2.4 MiB | 8 MiB |
| 80ms-2mbit | 4 | 3.6 MiB | 6.4 MiB | 3.8 MiB | 2.7 MiB | 0.7 MiB | 8 MiB |
| 80ms-2mbit | 8 | 4.3 MiB | 8.0 MiB | 4.4 MiB | 3.6 MiB | 0.5 MiB | 8 MiB |

Heap returns to its starting level after the streams end. Growth is a small,
roughly fixed amount (one 16 KiB frame in flight per stream plus buffers), not
a queue.

## Verdict

D1 requires, on the 20 ms profile:

| Criterion | Measured | Result |
| --- | --- | --- |
| echo p50 ≤ RTT + 15 ms | 20.77 ms (limit 35 ms) | pass |
| echo p99 ≤ RTT + 60 ms | 21.06 ms (limit 80 ms) | pass |
| throughput ≥ 70 % of the link | 99.2 % | pass |
| control request under a saturated stream ≤ 2 × idle + 100 ms | 1.95 to 2.0 s against about 142 ms | **fail** |
| broker heap growth ≤ 8 MiB per slow stream | 0.5 to 2.0 MiB | pass |
| non-blocking handler never stalls the agent's connection | 19 ms with the handler buffered (loopback) | pass (the inline handler does stall it: 1.7 s) |

**NO-GO**, because the head-of-line criterion fails.

## What changes

D1's branch for "echo or head-of-line fails, memory passes": a second MQTT
connection per agent for streams, client id `<env-id>.t`, same certificate;
`OnConnectAuthenticate` accepts exactly the suffix `.t`; the ACL lets that
client publish only `stream/+/out` and subscribe only `stream/+/in` under its
own id; T4 then publishes and subscribes streams on it. The topics, T6 and
later tasks are unchanged. This adds one task (broker hook plus agent second
connection, `sonnet`) before T4. The controller's handler must be non-blocking
whichever way streams travel.

Caveat for the orchestrator: the proxy paces each TCP connection
independently, so a second connection is isolated here by construction. On a
real link both connections share the bottleneck queue, and a saturated stream
can still delay the control connection by the queue's depth. The agent's
1 MiB output queue (D2) and a smaller socket send buffer for the stream
connection bound that; the spike did not measure it.

## Not measured

- A real WAN: loss, jitter, bufferbloat at a shared bottleneck, and two
  connections sharing one link.
- Windows.
- The TLS handshake on a real network (connections are made once per run).
- Many agents at once (one agent per run).
- A slow external subscriber connection and the `MaximumClientWritesPending`
  drop path (the controller is an in-process subscriber).
- A bursty pty producer instead of an unpaced flood.
