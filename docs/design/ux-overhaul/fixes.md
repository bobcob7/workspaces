# F1: fixes from the whole-branch review of PR #84
Files: desktop/src-tauri/src/pr_links.rs, commands.rs, desktop/src/features/ui/SidebarToggle.tsx (+tests).
Write each test first and see it fail, then fix.
1. Rescan only when a transcript changed: cache per session (mtime, len) in `PrLinks` and skip the read when unchanged. Run the scan under `tokio::task::spawn_blocking` if the call site is async and this is simple; otherwise leave the call as is and say so.
2. Cap kept links at 50 per session (drop the oldest) and drop persisted sessions that no longer appear in the current listing; do the pruning only when the listing succeeded and is non-empty.
3. Add tests: tail truncation of a transcript over 4 MiB (link in the cut head not found, link in the tail found), a failed save does not panic or lose in-memory links, per-session isolation, and `commands::list` wiring if feasible (else a test of the `job_for` closure helper).
4. Fix the stale comment in commands.rs: say `job_for` always returns a job (empty when no state file), or restore None semantics unless there is a job or kept links; keep existing behaviour tests passing.
5. SidebarToggle tooltip: show "Ctrl+B" unless the platform is Mac (`navigator.platform`/userAgent test), "⌘B" on Mac; add a test.
Do not refactor storage (out of scope). Verify: cargo test -j 2, cargo clippy --all-targets -- -D warnings, --no-default-features --features online variants, cd desktop && npm run typecheck && npx vitest run src/features/ui src/App. No Co-Authored-By trailer.
