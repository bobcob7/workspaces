# ws-online CPU burn: root cause (hypothesis A)

Verdict: PASS. The busy loop is the notifier's 4 s listing, whose synchronous PR-link transcript scan runs on a tokio worker.

## Root cause
Chain, all on async workers with no `spawn_blocking`:
- `online/server.rs:153-163` `Notifier::run`: every `WATCH_INTERVAL` (4 s, `tray/watcher.rs:16`) it calls `commands::list` when the watcher is stale (`tray/watcher.rs:76-80`, 4 s). `MissedTickBehavior::Skip`.
- `commands.rs:93-99` `list` builds `job_for` for every `claude agents` entry, which calls `pr_links.augment(id, job)`.
- `pr_links.rs:44-50` `augment` calls `scan_file(link_scan_path)` for each session synchronously.
- `pr_links.rs:107-125` `scan_file` reads up to `SCAN_TAIL_BYTES` (4 MiB, `pr_links.rs:15`) of the transcript and runs `pr_hrefs_in`.
- `pr_links.rs:128-143` `pr_hrefs_in`: loop `while let Some(start) = next_scheme(rest)` (L131). `next_scheme` (L146-151) runs `text.find("https://")` AND `text.find("http://")` over the whole remaining text on every iteration. When one scheme is absent that scan covers the entire rest, so the cost is O(links x 4 MiB). `hrefs.contains` (L137) is also O(n^2).
- Reviews checker `reviews/checker.rs:~381` (`commands::list` in `check_steps`) calls the same path every 5 min and on forced refresh. The `/api/invoke` list command and the UI do too.

Pod data: 48 jobs have a `linkScanPath`, with transcript tails up to 4 MiB (biggest file 19 MB). The standalone release-mode copy of `scan_file` over all 48 real transcripts took **4.1 s CPU in total** (worst single file 0.24 s). That is at least one listing's worth of CPU per 4 s tick, so one core is saturated, 99% user time (matches sibling B's 99/1 user/kernel). The pod is slower and loaded, so a listing takes longer than its 4 s tick. `Skip` ticking then makes it continuous. The hot tid migrates because the timer wake re-schedules the task onto whichever worker is free, not because of a spin.

The pod's pid 7: uptime 1348 s and summed worker utime about 1320 s, i.e. about 1 core since start. Samples of 2 s windows showed tid 38, 43, 40 hot in turn (+205 ticks each, 100%). The first notifier listing starts right at boot, so the burn begins immediately after restart.

Healthz symptom: a request whose task lands on the blocked worker (its LIFO slot or local queue) waits for the whole synchronous scan. Locally it took 9-16 s, over the 3 s liveness timeout.

## Reproduction (done, one process)
- `cargo build -j 2 --no-default-features --features online --bin ws-online --target-dir <scratch>` (debug build).
- Fake `ws` (prints `{"workspaces":[],"warnings":[]}`; `daemon` sleeps) and fake `claude agents` (JSON list of the 48 real job ids) first on PATH. HOME and WS_ONLINE_DATA_DIR in scratch; `CLAUDE_CONFIG_DIR` points at the real jobs (read only) or a scratch copy.
- `WS_ONLINE_ADDR=127.0.0.1:18177 WS_ONLINE_USERNAME=u WS_ONLINE_PASSWORD=p WS_ONLINE_WEB_DIR=<dir with index.html>`.
- Observed, real jobs (with `linkScanPath`): one tokio-rt-worker at 91-99.9% after 25 s, no log output, `curl /healthz` 9.3 s and 16.4 s.
- A/B, same binary and agents, `linkScanPath` removed from all `state.json` copies: all threads 0% CPU, healthz 0.0009 s.

## Ruled out
- Static read of the HTTP server, websocket `pump` (`server.rs:559-600`; the `Some(msg)=outgoing.recv()` branch only disables for that select), Hub, push.rs, auth rate limiter `prune` (finite), `daemon_host::supervise` (1 s to 60 s backoff, 60 s on exit 4), `loops::tick_forever` (60 s), `reviews::run` (30 s wake, `next_delay` >= 1 min, interval validated 1-1440), env.rs/open.rs sleeps (std threads).
- Idle scratch server with no transcripts: 0% CPU, so no loop in the server, interval or daemon-host code.
- MQTT/8883/8884 are Go (`internal/environment`), a separate process from pid 7.

## Proposed failing-first test and fix
- Test (pr_links.rs): `pr_hrefs_in` over 4 MiB of text with 50k `https://` non-PR links and no `http://` must finish in < 100 ms (currently seconds). Plus a `commands::list` test with many large transcripts under `tokio::time::pause`/a worker-block check that a concurrent task is polled within 100 ms.
- Fix: (1) make `pr_hrefs_in` linear: one pass, find the next scheme from `rest` only, cache each search's result or use a single `memmem`/regex over the text, and a `HashSet` for dedupe; (2) cache scans per transcript keyed by (path, len, mtime) so unchanged transcripts are skipped; (3) run `list`'s `job_for` and scans inside `tokio::task::spawn_blocking`; (4) avoid re-listing when `watcher` isn't stale (Notifier and checker both list).
- Note: the pod's log shows nothing because no error is involved.
