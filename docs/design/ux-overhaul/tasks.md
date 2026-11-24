# UX overhaul tasks

Goal: more room for the conversation/terminal, and PR hints that persist and cover every forge.
Desktop app: `desktop/` (React + Redux Toolkit + MUI + CSS Modules, Vitest; Rust backend in `desktop/src-tauri`).
Rules: TypeScript strict, CSS Modules, add or update Vitest tests next to the code. Do NOT add a Co-Authored-By trailer to commits (project rule overrides any role prompt). Commit conventional-style, imperative.
Environment: node may be v20; if `npm ci` or vitest refuses, install Node 24 tarball (SHA256 checked) into $CLAUDE_JOB_DIR/tmp. Run heavy checks one at a time. Never wait on a permission prompt: list refused commands under `## Not run here`.

Parallel: T1 ∥ T2 ∥ T3 (disjoint files; T1 is Rust only).

## T1: PR links that persist and cover all forges (Rust)
Files: `desktop/src-tauri/src/jobs.rs`, `model.rs` (fn `links`, `workspace_entry`, other `links(...)` caller ~line 424), new `desktop/src-tauri/src/pr_links.rs` if needed, `lib.rs` for `mod`.
Background: `Entry.links` come only from Claude Code's `jobs/<id>/state.json` `children` (`JobChild{href,kind}`). Claude Code reports only some PRs (in practice GitHub) and drops a PR link once it is merged or closed. The state also has `link_scan_path` (the session transcript).
Required:
1. Derive PR links ourselves: scan the transcript at `link_scan_path` (when present and readable; cap read size, e.g. last 4 MiB, never fail) for pull request URLs of ANY forge: GitHub `https://HOST/OWNER/REPO/pull/N` and Forgejo/Gitea `https://HOST/OWNER/REPO/pulls/N`. Dedupe by href, normalise (strip trailing punctuation, `#...`, `?...`, `/files` etc. subpaths), kind `"pr"`.
2. Union them with Claude's `children` links (keep Claude's order first, then scanned, deduped by href).
3. Make them sticky: persist the set of PR links seen per session in a ws-owned file under the app's data/state dir (find how other state is stored in this crate, e.g. `cache.rs`/`loops.rs`; atomic write, tolerate missing/corrupt file) so a link seen once keeps being returned after Claude Code drops it (merged/closed PR). Never persist non-PR links.
4. Keep all existing behaviour and tests; model entry `links` stays `Vec<Link>`.
Acceptance (unit tests in the Rust files): a transcript containing a github `/pull/4` and a forgejo `https://git.example.com/o/r/pulls/7` yields both; a link present in an earlier read but absent from a later `children` and transcript is still returned; corrupt/missing state file does not panic; trailing `)` or `.` stripped.
Verify: `cd desktop/src-tauri && cargo test -j 2 && cargo clippy --all-targets -- -D warnings` and the `--no-default-features --features online` variants (see `make desktop-test`).

## T2: Collapsible agent header, more room for the conversation (TS/CSS)
Files: `desktop/src/features/agents/EntryPane.tsx`, `EntryPane.module.css`, `ChildrenCard.tsx`, `ChildrenCard.module.css`, `RootProgress*`, `RunCard*` (only if needed), their tests, and a new `uiSlice` field ONLY if needed (T3 also edits `features/ui/uiSlice.ts`: if you need state, keep it in component state or localStorage-backed hook instead, to avoid a conflict).
Required:
1. EntryPane's header (`<header>`): add a toggle (button with aria-label "Collapse details"/"Expand details", aria-expanded) that collapses everything except one compact row: status dot/title and the primary action buttons. Collapsed hides meta chips, detail, task, result, PR links, RootProgress, ChildrenCard, RunCard. Persist the choice per browser (localStorage, try/catch) and default to expanded for a new agent; this reuses the phone `detailsOpen` mechanism where sensible.
2. The conversation/terminal must take all remaining height: `.root` is a flex column; header blocks must not grow unbounded. Give the combined non-conversation area (meta/detail/result/links/children/run card) a `max-height` (about 40vh) with `overflow-y: auto` when expanded so it can never push the conversation off screen. ChildrenCard's list is already scrollable (`max-height: 220px`); ensure it still scrolls and also its own card is not clipped. The `.result`/`.detail` text must be scrollable when long.
3. PR links row stays visible in the compact row as chips when collapsed? No: keep it simple, hide when collapsed.
Acceptance: Vitest tests for toggle (aria-expanded flips, content hidden, persistence); tests still pass. 
Verify: `cd desktop && npm ci && npm run typecheck && npm test -- --maxWorkers=2 --testTimeout=60000`.

## T3: Collapsible sidebar (TS/CSS)
Files: `desktop/src/App.tsx`, `App.module.css`, `desktop/src/features/agents/Sidebar.tsx` + css (only for a collapse button), `desktop/src/features/rail/Rail*` (only if the collapse button lives there), `features/ui/uiSlice.ts` (+ test), App tests.
Required:
1. Add `sidebarCollapsed: boolean` to ui state with action `sidebarToggled` (persist to localStorage with try/catch; load in initial state safely). Default false.
2. When collapsed the `<aside className={styles.sidebar}>` is not rendered/hidden (width 0, no border) so the main pane gets the width; the Rail stays. A visible toggle button (aria-label "Collapse sidebar"/"Expand sidebar", aria-expanded, tooltip) lives in the top bar (left of the brand) on wide screens. Optional keyboard shortcut Ctrl/Cmd+B (ignore when typing in inputs and in the terminal).
3. Phone layout (max-width 720px) unchanged: there the list/detail panes already toggle; the button must be hidden there.
Acceptance: Vitest tests for slice reducer, toggle button behaviour and aria state.
Verify: same as T2.
