# tokio-burn: ws-online pegs one core and stalls /healthz

Root cause (investigated 2026-11-25, repro'd locally): `Notifier::run` (desktop/src-tauri/src/online/server.rs:153) lists every 4 s;
`commands::list` (src/commands.rs:93-99) calls `PrLinks::augment` per session; `augment` (src/pr_links.rs:44) calls `scan_file`
synchronously on an async worker, reading up to 4 MiB of each transcript; `pr_hrefs_in` (src/pr_links.rs:128-151) is
O(links x text) because `next_scheme` runs `find("https://")` and `find("http://")` over the whole rest on every iteration (and
`hrefs.contains` is O(n^2)). On the live pod: 48 transcripts, 4.1 s CPU per full listing > the 4 s tick, so one worker is always busy and
requests queued on it (/healthz) time out.

Environment limits: arm64/x86 pod; `cargo test -j 2` one at a time; build features `--no-default-features --features online`
(from desktop/src-tauri). No Co-Authored-By trailer in commits (project rule). Commit and push after each file.
All edits stay in desktop/src-tauri/src.

## T1 (small): make `pr_hrefs_in` linear
Files: src/pr_links.rs only.
- Failing-first test: `pr_hrefs_in` over ~4 MiB of text containing 50_000 non-PR `https://example.com/x` links and no `http://`
  must return in < 200 ms in a debug test build (currently takes many seconds; run it first and record the failure), and still return
  the same results for the existing tests. Add a test with both schemes interleaved and one with duplicates keeping order of first appearance.
- Fix: one forward pass. Find the next scheme by scanning `rest` once (e.g. search for "http" then check `s://` / `://` prefix, or
  `memchr`-free manual find of "http" with check), never scanning past the nearest match; dedupe with a `HashSet<String>` alongside the
  Vec (keep order). Keep `pr_href`/`pr_hrefs_in` signatures and outputs identical.
- Mutation checks (E4): after the tests pass, revert the fix to `next_scheme` double-find on a scratch copy and confirm the perf test fails;
  break the dedupe and confirm the duplicates test fails. Report survivors.
- Verify: `cd desktop/src-tauri && cargo test -j 2 --no-default-features --features online pr_links`

## T2: stop scanning on the async worker, and cache unchanged transcripts
Depends on T1. Files: src/pr_links.rs, src/commands.rs (and tests).
- Cache in `PrLinks`: `scan_file` results keyed by (path, file len, mtime) so an unchanged transcript is not re-read or re-scanned
  (a `Mutex<HashMap<PathBuf,(u64,SystemTime,Vec<String>)>>` inside `PrLinks`; bounded, e.g. drop entries not seen in the current
  `augment` pass is not required, cap at 512 entries). Failing-first test: calling `augment` twice with the same transcript scans once
  (expose a `#[cfg(test)]` scan counter), and rescans after the file grows.
- Do not hold the std Mutex while scanning or across any await.
- Offload: in `commands::list`, the synchronous `model::merge(..., &job_for, ...)` (file reads + scans) must not stall a worker. Use
  `tokio::task::block_in_place` when `tokio::runtime::Handle::current().runtime_flavor()` is multi-thread, else run inline (tests use
  current_thread). Failing-first test: a `#[tokio::test(flavor = "multi_thread", worker_threads = 2)]` that runs `list` over a jobs dir
  with a transcript whose scan takes long (e.g. a large file, or `PrLinks` test hook sleeping 500 ms in the scan) while a concurrent
  `tokio::spawn`ed task must be polled within 100 ms; it fails before the change.
- Mutation checks as above (remove the cache lookup -> counter test fails; remove block_in_place -> responsiveness test fails).
- /healthz (server.rs:410) already does no I/O; do not change it.
- Verify: `cd desktop/src-tauri && cargo test -j 2 --no-default-features --features online` then `cargo clippy --no-default-features --features online --all-targets -- -D warnings` and `cargo fmt --check`.
