# Terminal spike: latency, throughput, slow reader, memory

**Verdict: NO-GO on the head-of-line criterion for the default single
connection; GO on echo latency, throughput and broker memory.** A control
request (the stand-in for `workspace.list`) sent while one stream saturates
the agent's single TLS connection takes 2.0 to 3.2 s on the 20 ms profile,
against [D1](tasks-ui.md)'s limit of 2 × idle + 100 ms (about 142 ms). The
cause is the agent's own kernel TCP send queue (2.5 to 4.0 MB, because the
agent's socket is untuned and Linux autotuning lets it grow to `tcp_wmem`
max), not the broker, the proxy or the slow handler. It appears only under
bulk output at line rate (a `cat` of a big file), not for a terminal at a few
hundred KB/s. [Section 5](#5-alternatives-bounding-the-send-queue) measures
the fixes: bounding the agent's socket (`SO_SNDBUF` or `TCP_NOTSENT_LOWAT`)
on the single connection cuts the delay about 10 times (to 0.17 to 0.30 s)
but still misses D1 on the 20 ms / 10 Mbit profile; a second connection
passes, and `TCP_NOTSENT_LOWAT` 16 KiB on it also keeps typing echo fast
during bulk output. The measured recommendation is in section 5.

## Setup

- Harness: `internal/mqttbroker/termspike_test.go` and `throttle_test.go`,
  both behind the `spike` build tag. The real broker (`mqttbroker.New`,
  mochi v2.7.9) with a real mTLS agent connection (paho v0.23.0 through
  `internal/mqttclient`), the controller side as the broker's in-process
  `Publish` and `Subscribe`.
- The agent connects through an in-process TCP proxy that delays each
  direction by RTT/2 and paces bytes with a token bucket. The proxy's own
  sockets have 32 KiB buffers (the kernel doubles them) and it holds at most
  16 chunks of 4 KiB per direction. These settings are on the proxy's sockets
  only: the agent's socket is untuned, so Linux autotuning grows its send
  buffer to `tcp_wmem` max (4 MiB here). A reviewer saw 2.8 to 4.0 MB in the
  agent socket's `tx_queue` (`/proc/net/tcp`) during the slow-reader test;
  section 5 reads the same queue with `SIOCOUTQ`.
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

The sent-minus-received difference on the throttled links (107 frames, about
1.7 MB, on the 20 ms profile; 170 frames on the 80 ms profile) is not shown to
have arrived: the counters were read after a 1 s plus 2 × RTT wait, which is
shorter than the time the agent's kernel send queue (the one in section 3)
needs to drain, so those frames were still in flight when received was read.
"Lost (gaps)" counts only forward sequence gaps, so it cannot see frames lost
at the tail. The throughput itself is measured inside the 10 s window and is
unaffected.

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
- **An inline slow handler delays the broker's read of the agent's replies.**
  The inline handler runs on the broker's per-client read loop for the agent's
  connection, which delays reading the agent's QoS 1 `res`. On loopback a
  control request waits 1.7 s with the handler sleeping inline, 19 ms with a
  non-blocking handler. The handler must hand off to a bounded buffer, as D2
  already specifies.
- **A saturated stream delays control traffic on the same connection**,
  whatever the handler: 1.95 to 2.0 s on the 20 ms profile with `fast`,
  `inline` and `buffered` alike (the later section 5 runs of the same case
  gave 2.6 to 3.2 s, depending on how far the queue had grown). The delay is
  the agent's own kernel send queue: `SIOCOUTQ` on the agent's socket reads
  2.5 to 4.0 MB during the flood, which at 1.25 MB/s is 2 to 3 s, and a
  control reply queues behind all of it. It is not the broker, and it is not
  the proxy: the proxy's buffers hold at most about 0.15 s. At 2 Mbit/s the
  same queue gives 7 s. This is sender-side bufferbloat, real on a WAN, and it
  is an unbounded send buffer rather than the link.
- **It appears only under bulk output at line rate.** The flood never pauses.
  A terminal at a few hundred KB/s does not fill 10 Mbit/s, so no queue forms
  and a control request would see about the idle latency. The failing case is
  a bulk dump such as `cat bigfile`, which is what D1's "saturated stream"
  means.
- **Keep-alive delay was not measured.** The 30 s keep-alive rides the same
  connection; D1 asks for its delay, and this spike only argues it is bounded
  by the control-request figures above (none came near the 45 s that would
  drop the connection).
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
| non-blocking handler never stalls the agent's connection | 1951 ms with the handler buffered on the 20 ms profile (19 ms on loopback) | **fail**, for the same reason as the head-of-line row (the kernel send queue, not the handler); the inline handler additionally delays the broker's read loop for the agent's connection (1.7 s on loopback) |

**NO-GO** for the default single connection, because the head-of-line
criterion fails.

Memory is weak evidence: the in-process handler sleeps inline, which blocks the
broker's read of the agent, so the flood backs up in TCP buffers and not in
broker heap. The test cannot show queueing in the broker, and a slow external
subscriber (the real case) is not measured, so the memory "pass" and the
choice of the second-connection branch over the WebSocket branch rest on it.

## What changes

D1's branch for "echo or head-of-line fails, memory passes": a second MQTT
connection per agent for streams, client id `<env-id>.t`, same certificate;
`OnConnectAuthenticate` accepts exactly the suffix `.t`; the ACL lets that
client publish only `stream/+/out` and subscribe only `stream/+/in` under its
own id; T4 then publishes and subscribes streams on it. The topics, T6 and
later tasks are unchanged. This adds one task (broker hook plus agent second
connection, `sonnet`) before T4. The controller's handler must be non-blocking
whichever way streams travel.

Caveat for the orchestrator, and why this branch is not the only option:
the second connection removes only the agent-side send-buffer queue, because
control traffic gets its own socket with an empty buffer. It does not remove a
shared bottleneck queue (the agent's uplink, a home router): both connections'
packets enter it and control traffic still waits behind the stream's backlog
there. Only bounding the stream's bytes in flight (the options in section 5)
and D2's output queue bound that. The failure appears only under bulk output
at line rate. A single connection with a bounded send buffer is an
alternative to the second connection, so [section 5](#5-alternatives-bounding-the-send-queue)
measures both, alone and combined.

## 5. Alternatives: bounding the send queue

Same broker, proxy and probe as section 3, behind the `spike` tag
(`termspike_alt_test.go`; `TestSpike_alternatives` and
`TestSpike_alternativesTight`), re-run as one sweep of 44 cells in 197 s (the
two profiles ran in parallel; the cells of a profile ran one after another).
The agent's socket options are set in a `net.Dialer` `Control` function on the
client's dial, before connect. `mqttclient` has no dial hook yet, so the
harness has its own paho-based agent connection (the same CONNECT, publish and
subscribe) and changes no production code. Per cell:

- **Control request**: the controller publishes QoS 1 `req/<n>`, the agent
  answers QoS 1 `res/<n>` on its control connection; 5 requests 200 ms apart,
  first idle, then after 1 s of a 16 KiB QoS 0 flood on one stream. The p99
  column is the maximum of the 5 samples. D1's limit is 2 × that cell's idle
  p50 + 100 ms (142 ms on 20ms-10mbit), checked against the p50, with all 5
  answered.
- **Echo**: 20 keystrokes 100 ms apart, idle; 10 more while the flood runs
  (informational: D1 judges idle echo, and under a flood the echo shares the
  stream's queue).
- **Stream throughput** while the probe runs, as a share of the link.
  "Peak send queue" below is the largest `SIOCOUTQ` (unsent plus
  unacknowledged bytes) sampled every 20 ms on the stream's agent socket.
- Cells: (a) single connection, default buffer; (b) `SO_SNDBUF` 64 KiB (the
  kernel doubles it to 128 KiB); (c) `TCP_NOTSENT_LOWAT` 16 KiB; (d) both;
  (e) the stream on a second connection with default buffers (the control
  connection stays default); (f1) and (f2) the second connection with (b) or
  (c) on the stream connection. Extra single-connection bounds: (g1)
  `SO_SNDBUF` 32 KiB, (g2) 16 KiB, (g3) `TCP_NOTSENT_LOWAT` 4 KiB, (g4)
  32 KiB with 4 KiB.
- Two proxies. **separate**: the existing proxy, which paces each connection
  alone (so a second connection is isolated by construction; it holds about
  128 KiB per connection ahead of the paced link, 100 ms at 10 Mbit/s).
  **shared-64k**: a new proxy where both connections share one FIFO of 64 KiB
  per direction drained at the link rate, then an RTT/2 delay line. It also
  has 32 KiB (doubled) receive buffers per socket ahead of the FIFO; a 4 KiB
  receive buffer to remove them starved the stream and was dropped.
- Profiles: 20ms-10mbit (D1's) and 40ms-100mbit (a high bandwidth-delay
  path, about 500 KB BDP).

The baseline (a) is re-measured here: 2.6 to 3.2 s on 20ms-10mbit (section 3
saw 1.95 to 2.0 s; the queue's fill varies by run), with a 3.5 to 4.0 MB peak
send queue.

### 20ms-10mbit, per-connection pacing

| Cell | Idle p50 | Control p50 | Control p99 (max of 5) | Limit | Echo idle p50 | Echo under flood p50 | Stream, % of link | D1 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| a single, default buffer | 21.04 ms | 3201.45 ms | 3270.74 ms | 142.08 ms | 21.12 ms | 2972.99 ms | 99.3 % | **fail** |
| b single, SO_SNDBUF 64K | 21.10 ms | 299.99 ms | 328.34 ms | 142.20 ms | 21.50 ms | 225.96 ms | 99.3 % | **fail** |
| c single, NOTSENT_LOWAT 16K | 21.43 ms | 195.49 ms | 198.24 ms | 142.86 ms | 21.17 ms | 138.91 ms | 99.8 % | **fail** |
| d single, both | 21.36 ms | 182.98 ms | 207.87 ms | 142.72 ms | 21.18 ms | 145.86 ms | 99.9 % | **fail** |
| e second, default buffer | 21.63 ms | 21.91 ms | 22.29 ms | 143.25 ms | 21.05 ms | 3092.22 ms | 100.2 % | pass |
| f1 second, SO_SNDBUF 64K | 21.94 ms | 21.94 ms | 22.01 ms | 143.89 ms | 20.91 ms | 254.31 ms | 99.0 % | pass |
| f2 second, NOTSENT_LOWAT 16K | 21.14 ms | 21.67 ms | 21.81 ms | 142.27 ms | 20.84 ms | 129.78 ms | 100.3 % | pass |
| g1 single, SO_SNDBUF 32K | 20.97 ms | 209.08 ms | 261.05 ms | 141.95 ms | 21.10 ms | 198.65 ms | 99.9 % | **fail** |
| g2 single, SO_SNDBUF 16K | 20.95 ms | 169.81 ms | 182.64 ms | 141.91 ms | 20.83 ms | 138.81 ms | 99.1 % | **fail** |
| g3 single, NOTSENT_LOWAT 4K | 21.12 ms | 182.66 ms | 198.06 ms | 142.25 ms | 20.90 ms | 140.24 ms | 99.9 % | **fail** |
| g4 single, SO_SNDBUF 32K + NOTSENT_LOWAT 4K | 20.98 ms | 195.02 ms | 196.49 ms | 141.97 ms | 21.16 ms | 144.52 ms | 99.2 % | **fail** |

### 20ms-10mbit, shared 64 KiB bottleneck queue

| Cell | Idle p50 | Control p50 | Control p99 (max of 5) | Limit | Echo idle p50 | Echo under flood p50 | Stream, % of link | D1 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| a single, default buffer | 21.41 ms | 2638.55 ms | 2951.14 ms | 142.83 ms | 21.11 ms | 2640.31 ms | 99.4 % | **fail** |
| b single, SO_SNDBUF 64K | 21.15 ms | 290.66 ms | 311.82 ms | 142.30 ms | 20.99 ms | 242.79 ms | 99.7 % | **fail** |
| c single, NOTSENT_LOWAT 16K | 21.12 ms | 182.23 ms | 209.09 ms | 142.24 ms | 21.06 ms | 161.05 ms | 99.7 % | **fail** |
| d single, both | 21.01 ms | 182.69 ms | 201.58 ms | 142.01 ms | 21.00 ms | 150.47 ms | 99.7 % | **fail** |
| e second, default buffer | 21.13 ms | 81.72 ms | 83.44 ms | 142.26 ms | 21.02 ms | 3188.20 ms | 99.3 % | pass |
| f1 second, SO_SNDBUF 64K | 21.04 ms | 80.91 ms | 82.61 ms | 142.07 ms | 21.03 ms | 234.68 ms | 99.6 % | pass |
| f2 second, NOTSENT_LOWAT 16K | 21.06 ms | 80.44 ms | 81.16 ms | 142.13 ms | 20.95 ms | 149.28 ms | 99.9 % | pass |
| g1 single, SO_SNDBUF 32K | 21.21 ms | 265.85 ms | 288.55 ms | 142.43 ms | 21.76 ms | 224.06 ms | 99.8 % | **fail** |
| g2 single, SO_SNDBUF 16K | 21.50 ms | 194.36 ms | 196.03 ms | 143.01 ms | 21.23 ms | 132.67 ms | 99.7 % | **fail** |
| g3 single, NOTSENT_LOWAT 4K | 21.64 ms | 182.26 ms | 234.80 ms | 143.27 ms | 21.48 ms | 153.87 ms | 99.7 % | **fail** |
| g4 single, SO_SNDBUF 32K + NOTSENT_LOWAT 4K | 21.36 ms | 168.90 ms | 195.33 ms | 142.72 ms | 21.44 ms | 153.89 ms | 99.7 % | **fail** |

### 40ms-100mbit, per-connection pacing

| Cell | Idle p50 | Control p50 | Control p99 (max of 5) | Limit | Echo idle p50 | Echo under flood p50 | Stream, % of link | D1 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| a single, default buffer | 41.13 ms | 372.23 ms | 388.75 ms | 182.25 ms | 41.68 ms | 343.39 ms | 99.4 % | **fail** |
| b single, SO_SNDBUF 64K | 41.06 ms | 65.96 ms | 74.19 ms | 182.11 ms | 40.94 ms | 62.13 ms | 99.3 % | pass |
| c single, NOTSENT_LOWAT 16K | 41.00 ms | 55.99 ms | 56.84 ms | 182.00 ms | 40.93 ms | 53.45 ms | 99.4 % | pass |
| d single, both | 40.91 ms | 56.53 ms | 58.78 ms | 181.81 ms | 40.94 ms | 52.53 ms | 99.3 % | pass |
| e second, default buffer | 40.94 ms | 41.51 ms | 41.83 ms | 181.88 ms | 41.09 ms | 231.81 ms | 99.3 % | pass |
| f1 second, SO_SNDBUF 64K | 40.76 ms | 41.66 ms | 42.02 ms | 181.52 ms | 41.01 ms | 62.01 ms | 99.4 % | pass |
| f2 second, NOTSENT_LOWAT 16K | 41.12 ms | 40.70 ms | 41.35 ms | 182.23 ms | 41.20 ms | 52.07 ms | 99.3 % | pass |
| g1 single, SO_SNDBUF 32K | 41.10 ms | 63.97 ms | 66.54 ms | 182.19 ms | 41.66 ms | 59.11 ms | 99.3 % | pass |
| g2 single, SO_SNDBUF 16K | 41.00 ms | 50.08 ms | 55.73 ms | 182.00 ms | 40.87 ms | 48.64 ms | 99.2 % | pass |
| g3 single, NOTSENT_LOWAT 4K | 41.21 ms | 55.99 ms | 57.15 ms | 182.42 ms | 40.96 ms | 52.51 ms | 99.4 % | pass |
| g4 single, SO_SNDBUF 32K + NOTSENT_LOWAT 4K | 41.25 ms | 56.94 ms | 61.08 ms | 182.51 ms | 40.95 ms | 51.89 ms | 99.3 % | pass |

### 40ms-100mbit, shared 64 KiB bottleneck queue

| Cell | Idle p50 | Control p50 | Control p99 (max of 5) | Limit | Echo idle p50 | Echo under flood p50 | Stream, % of link | D1 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| a single, default buffer | 41.40 ms | 367.48 ms | 382.30 ms | 182.81 ms | 40.98 ms | 339.37 ms | 99.3 % | **fail** |
| b single, SO_SNDBUF 64K | 41.07 ms | 66.69 ms | 69.04 ms | 182.13 ms | 40.95 ms | 62.82 ms | 99.3 % | pass |
| c single, NOTSENT_LOWAT 16K | 41.18 ms | 56.80 ms | 57.49 ms | 182.36 ms | 40.92 ms | 53.13 ms | 99.3 % | pass |
| d single, both | 41.25 ms | 56.13 ms | 59.60 ms | 182.51 ms | 41.14 ms | 53.11 ms | 99.5 % | pass |
| e second, default buffer | 41.30 ms | 47.19 ms | 47.61 ms | 182.60 ms | 41.35 ms | 323.70 ms | 99.3 % | pass |
| f1 second, SO_SNDBUF 64K | 41.24 ms | 47.43 ms | 47.81 ms | 182.47 ms | 41.43 ms | 64.68 ms | 99.4 % | pass |
| f2 second, NOTSENT_LOWAT 16K | 41.12 ms | 46.90 ms | 47.87 ms | 182.24 ms | 40.97 ms | 54.07 ms | 99.4 % | pass |
| g1 single, SO_SNDBUF 32K | 41.46 ms | 65.77 ms | 66.29 ms | 182.91 ms | 41.02 ms | 60.73 ms | 99.4 % | pass |
| g2 single, SO_SNDBUF 16K | 41.51 ms | 57.60 ms | 59.51 ms | 183.02 ms | 41.51 ms | 51.79 ms | 99.4 % | pass |
| g3 single, NOTSENT_LOWAT 4K | 41.56 ms | 57.17 ms | 58.79 ms | 183.13 ms | 41.46 ms | 53.05 ms | 99.3 % | pass |
| g4 single, SO_SNDBUF 32K + NOTSENT_LOWAT 4K | 41.29 ms | 57.29 ms | 58.27 ms | 182.58 ms | 41.36 ms | 54.14 ms | 99.3 % | pass |

Peak send queues (the same in every table): (a) and (e) 2.5 to 4.0 MB; (b) and
(f1) 127 KiB; (c), (d), (f2), (g3) and (g4) 64 to 80 KiB; (g1) 95 KiB;
(g2) 48 to 55 KiB.

### What the numbers say

- **The cause is confirmed**: the unbounded send queue is the delay. Any
  bound on the agent's socket cuts the control request from 2.6 to 3.2 s to
  0.17 to 0.30 s on 20ms-10mbit, and to 56 to 66 ms (limit 182 ms) on
  40ms-100mbit, with the stream still at 99 % of the link.
- **On 20ms-10mbit no single-connection option passes D1.** The best are
  `TCP_NOTSENT_LOWAT` (c, g3, g4) at 169 to 195 ms and `SO_SNDBUF` 16 KiB (g2)
  at 170 to 194 ms, against about 142 ms, a miss of 27 to 55 ms. `SO_SNDBUF`
  64 KiB (b) is worse (290 to 300 ms), because the kernel holds up to
  127 KiB. Tighter bounds do not help: `TCP_NOTSENT_LOWAT` 4 KiB gives the
  same 182 ms as 16 KiB, because the remaining queue is 64 to 80 KiB of sent,
  unacknowledged bytes (`SIOCOUTQ`), 64 to 100 ms at 10 Mbit/s. That is
  consistent with the proxy's buffers ahead of the paced link, which
  `TCP_NOTSENT_LOWAT` does not bound; this attribution was not tested
  separately. The reply
  to a control request has to wait behind that backlog on one connection.
  This residual includes the harness's own buffering, so it is an upper bound
  on what a real path with a smaller queue would show; it was not separated
  out.
- **On 40ms-100mbit every bounded single connection passes** (50 to 66 ms
  against 182 ms), because the same bytes in flight take a tenth of the time
  at ten times the rate.
- **A second connection passes on both profiles and both proxies**: 21 to
  22 ms with per-connection pacing (idle plus nothing) and 80 to 82 ms through
  the shared 64 KiB queue on 20ms-10mbit (the queue's 52 ms plus buffers), 47
  ms on 40ms-100mbit. The shared queue costs about 60 ms; it is not free, and
  the limit still holds.
- **Bounding the stream socket also matters for typing during bulk output.**
  With default buffers, the echo of a keystroke typed during the flood waits
  2 to 3 s even on the second connection (e), because it queues behind the
  flood on the same stream socket. With `TCP_NOTSENT_LOWAT` 16 KiB (f2) it
  waits 0.13 to 0.15 s on 20ms-10mbit and 52 to 54 ms on 40ms-100mbit;
  `SO_SNDBUF` 64 KiB (f1) gives 0.23 to 0.25 s. D1 does not test this, but it
  is what a user sees.
- **Throughput cost of a small send buffer cannot be seen here.** Both TCP
  legs of the harness are loopback, so the proxy acknowledges instantly and the
  agent's socket never has a long ack clock; every `SO_SNDBUF` cell reads
  99 % of the link, including 16 KiB on a 40 ms path. That is not evidence of
  no cost. On a real path the window cannot exceed the send buffer, so a
  64 KiB `SO_SNDBUF` (about 64 KiB usable) at 40 ms RTT would cap at roughly
  1.6 MB/s, 13 % of 100 Mbit/s (a calculation, not a measurement), which
  would cripple the high bandwidth-delay case. `TCP_NOTSENT_LOWAT` bounds
  only the not-yet-sent queue and does not cap the congestion window, so it
  is expected to keep throughput; the harness cannot confirm that either.
  The measured throughput column therefore says nothing for or against either
  option.
- Each cell has 5 control samples and one run, so differences under about
  20 ms (for example 182 against 195 ms) are noise; run-to-run, the baseline
  alone moved from 2.0 to 3.2 s.

### Recommendation

D1 on 20ms-10mbit is not passed by any single-connection option measured
here, so the second-connection plan stands (T1a to T1d, D11 to D17):

- **Use the second connection, and bound the stream socket with
  `TCP_NOTSENT_LOWAT` (16 KiB), not `SO_SNDBUF` 64 KiB.** It passes D1 (21 ms
  to 82 ms against 142 ms), keeps the stream at the link rate in every cell
  (and, unlike `SO_SNDBUF`, is not expected to cap the window on a long fat
  path), and cuts echo during bulk output from 2 to 3 s to about 0.15 s. This
  changes T1a's `WriteBuffer` (D14's "64 KiB `SO_SNDBUF`") into a
  not-sent-low-water option, applied in `mqttclient`'s dial with a
  `net.Dialer` `Control` function; no task is added or removed.
- **The simplest option, if the owner accepts the miss.** A single connection
  with `TCP_NOTSENT_LOWAT` 16 KiB (c) gives a control request of 182 to
  195 ms (169 to 195 ms across the tighter bounds) during a bulk dump on a 10 Mbit/s link, 40 to 55 ms over D1's
  142 ms, and passes on the 100 Mbit/s path (56 ms against 182 ms). The only
  code is the dial option in `mqttclient` (T1a's option, renamed). It would
  remove: T1b (broker identity and ACL), T1c (agent stream connection, its
  lifecycle), T1d (re-measure), the `StreamClientID` half of T1a, and D11 to
  D17 and D18's ADR amendments; T4, T6, T7, T8, T11 and T18 mention the stream
  connection and their text would need the stream-connection wording removed
  (that was not audited). The cost is that bulk output on a slow link delays
  control requests by about 0.18 s instead of the second connection's 0.02 to
  0.08 s, and that this was measured only against this harness's proxy, which
  itself holds 64 to 100 ms ahead of the link.
- **What the owner should decide:** whether D1's limit (2 × idle + 100 ms)
  is a hard gate for bulk output on a 10 Mbit/s link. If it is, keep the
  second connection with `TCP_NOTSENT_LOWAT`. If a 0.18 s control delay
  during a `cat` of a big file is acceptable, the single connection with
  `TCP_NOTSENT_LOWAT` removes T1b to T1d and D11 to D17. The WebSocket
  fallback is not needed either way: nothing here fails on throughput or
  memory. Neither choice was checked on a real WAN or for throughput on a
  long fat path.

## Not measured

- A real WAN: loss, jitter and real bufferbloat at a shared bottleneck.
  Section 5's shared queue is a model (one 64 KiB FIFO).
- The 30 s keep-alive's delay under a saturated stream (argued, not
  measured).
- The throughput cost of a small `SO_SNDBUF` on a real high bandwidth-delay
  path (section 5: the harness cannot show it).
- Windows.
- The TLS handshake on a real network (connections are made once per run).
- Many agents at once (one agent per run).
- A slow external subscriber connection and the `MaximumClientWritesPending`
  drop path (the controller is an in-process subscriber).
- A bursty pty producer instead of an unpaced flood.
