# Constraints for `env-scope-ui`: environment scopes in the desktop app

What this pod can and can't check, how long each check takes, the values
tests pin, and the repo rules every task follows. Probed 2026-11-24 on
`feat/env-scope-ui` at `220a463` (cut from `feat/env-scope`, PR #89, open).
The decisions are in [design.md](design.md), the order in [tasks.md](tasks.md).

## 1. Environment proof (probed, not assumed)

Pod: x86_64 (`uname -m`), Go 1.27.3, Rust/cargo from the image, system
Node **v20.19.2** (npm 9.2.0). Playwright browsers are cached in
`~/.cache/ms-playwright` (`chromium-1248`, plus `chromium_headless_shell-1208`
which the designer installed for Playwright 1.58.2).

| Command (from the repo root unless noted) | Result | Wall time |
| --- | --- | --- |
| `cd desktop/src-tauri && cargo check` (default `desktop` feature, GTK/webkit) | `Finished dev profile` | 65 s (deps partly cached) |
| `cd desktop/src-tauri && cargo clippy --all-targets -- -D warnings` | clean | 20 s after `cargo check` |
| `cd desktop/src-tauri && cargo test --lib model` / `cargo test --lib tray` | 59 passed / 74 passed | 86 s for both (first test build) |
| `cd desktop && npm ci` (Node 22.21.1) | ok | 12 s |
| `cd desktop && npx vitest run src/features/agents src/features/environments` (Node 22) | 20 files, 310 tests passed | 15 s |
| `cd desktop && npx tsc --noEmit` (Node 22) | ok | 10 s |
| `cd desktop && npx vitest run src/features/agents/tree.test.ts` with system Node 20.19.2 | 34 passed | — |
| `go test -count=1 ./internal/cli/ -run 'List\|Env'` | ok | 29 s |
| `go vet ./internal/cli/` | ok | 6 s |
| Vite dev server `npx vite --port 1427 --strictPort` + Playwright 1.58.2 headless Chromium + `test/tauri-mock.js`, one 1100×750 screenshot | Vite answered in 1-3 s; shot in 1.7 s; 0 `unhandled command` logs | ~15 s incl. browser install |

`cargo test` with one argument only: `cargo test --lib model tray` fails
(`unexpected argument 'tray'`); run one filter per command.

### Node 22 (required for vitest)

`vitest` declares `engines.node ^22.12.0 || ^24.0.0 || >=26.0.0` (vite and
jsdom also accept `^20.19.0`). One vitest file ran on Node 20.19.2, but that
is outside vitest's supported range: **use Node 22 for every desktop check**.
Install it from the official tarball into the job's tmp dir, SHA256 checked
(10 s here):

```bash
T=$CLAUDE_JOB_DIR/tmp/node; mkdir -p "$T" && cd "$T"
V=v22.21.1; A=linux-x64; [ "$(uname -m)" = aarch64 ] && A=linux-arm64
curl -fsSLO https://nodejs.org/dist/$V/SHASUMS256.txt
curl -fsSLO https://nodejs.org/dist/$V/node-$V-$A.tar.xz
grep " node-$V-$A.tar.xz\$" SHASUMS256.txt | sha256sum -c -   # must print OK
tar -xJf node-$V-$A.tar.xz
export PATH="$T/node-$V-$A/bin:$PATH"; node --version            # v22.21.1
```

`PATH` doesn't persist between Bash calls: prefix each desktop command with
the `export PATH=...` line. Then `cd desktop && npm ci` once.

### Playwright (screenshot pass, orchestrator only)

`playwright` is **not** a dependency of `desktop/`. Install it outside the
repo (e.g. `$CLAUDE_JOB_DIR/tmp/pw`, `npm i playwright@1.58.2`), then
`npx playwright install chromium-headless-shell` (8 s) if the revision it
wants isn't cached; `playwright@latest` (1.65.1) wants chromium 1255, also
not cached. An ESM script must live in the directory that has the
`node_modules` (`NODE_PATH` is ignored for `import`). Load the mock with
`page.addInitScript({ path: "desktop/test/tauri-mock.js" })` before
`page.goto`. Stop Vite afterwards and leave no `.playwright-mcp/` behind.

## 2. What can't run here

| Thing | Why | Instead |
| --- | --- | --- |
| A real second environment, a real ledger written by a real `scope.spawn` | one pod | T1 writes the ledger in tests with `envscope.NewLedger(dir).Put`, and `scope.json` with `envscope.Write`; the desktop side is fed by fixtures and the mock |
| The Tauri shell (`npm run tauri dev`) | no display | `cargo check`/`clippy`/`cargo test`, and Vite + mock in Playwright |
| `make desktop-test` in one go | runs `npm ci` with the system Node 20 | run its steps by hand with Node 22 (§4) |
| Windows / macOS builds | no host | not touched by this feature |

Heavy suites one at a time: `cargo test -j 2`, `go test -p 2`,
`npx vitest run --maxWorkers=2 --testTimeout=60000`. `internal/cli` in full
takes ~50 s; use `-run` while iterating.

## 3. Pinned values a task can break

| Pin | Where | What to do |
| --- | --- | --- |
| Exact `ws list --json` output | `internal/cli/list_test.go`: **12** `assert.JSONEq` calls (lines ~101-105 and 426 among them) | T1 adds `"scope_parent":null,"scope":null` to every entry object in each of the 12 strings. No other test file pins `ws list` JSON (`grep -rl '"agent_state":null' --include=*_test.go` → only `list_test.go`) |
| `ws list` docs | `docs/tools/ws-list.md` (JSON example ~line 100-125, `--json` row line 88), `internal/cli/guides/ws-list.md` (field list ~line 71) | T1 documents both fields in both. `internal/cli/llm_test.go` checks guides name only real error codes and flags: T1 adds neither |
| `Entry` struct literals in Rust | **9** sites: `model.rs` (`workspace_entry`, `session_entry`), `reviews/ask.rs:~219`, `reviews/fix.rs:~424`, `reviews/runs.rs:~110`, `reviews/view.rs:~505`, `tray/attention.rs:~302` and `:~393`, `tray/watcher.rs:~207` (find them with `grep -rn "snapshot_stale:" desktop/src-tauri/src`) | T2 adds `scope_parent: None, scope: None` to every literal except `workspace_entry` |
| Tauri command lists | `desktop/src-tauri/src/lib.rs` `invoke_handler` (~line 104), `online/dispatch.rs` command list (~line 90) and match (~line 200) | **unchanged**: no task adds a command. If you think you need one, stop and report |
| TS `Entry` mirror | `desktop/src/api.ts` `Entry` (~line 33) | T3 adds `scopeParent`, `scope` (required, nullable). `tsc` then fails on every object literal typed `Entry` that isn't built by a factory |
| Test fixture factory | `desktop/src/test/fixtures.ts` `entry()` (~line 40) | T3 adds `scopeParent: null, scope: null` defaults |
| Screenshot mock | `desktop/test/tauri-mock.js` `entry()` (~line 31), `remoteEntry()` (~line 63), `data.entries` (~line 700-890), `data.environments` (~line 396: `linux-server` online `env_7f3a`, `win-rig` offline `env_b20d`) | T3 adds defaults and the fixtures in tasks.md. Nothing tests this file: check it with `node --check` |
| Mock description | `desktop/README.md` "Screenshots without the backend" (~line 383) | T3 adds one paragraph naming the new sample entries |
| `parentOf` / `childrenOf` semantics | `tree.test.ts`, `status.test.ts`, `Sidebar.test.tsx`, `ChildrenCard.test.tsx`, `EntryPane.test.tsx`, `RootProgress.test.tsx` | same-environment behaviour must not change: every existing test passes unedited |
| Tray "top-level" rule | `tray/attention.rs` `is_tracked` and its tests (`child.parent_id = Some(...)` at ~377, ~489) | T2 adds the `scope_parent` condition and one test; existing tests unchanged |
| Phone breakpoint | `EntryPane.tsx` `PHONE_QUERY = "(max-width:720px)"` | reuse; don't add a new breakpoint |

## 4. Verify commands (copy exactly)

```bash
# Go (T1)
go test -p 2 -count=1 ./internal/cli/ -run 'TestApp_runList|List'
go test -p 2 -count=1 ./internal/cli/ ./internal/envscope/
go vet ./internal/cli/ && test -z "$(go tool golangci-lint fmt --diff ./internal/cli/...)"
go tool golangci-lint run ./internal/cli/...
go test -count=1 -run '^TestRepository$' ./internal/blanklines/

# Rust (T2)
cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings
cd desktop/src-tauri && cargo test -j 2 --lib model && cargo test -j 2 --lib tray
cd desktop/src-tauri && cargo clippy --no-default-features --features online --all-targets -- -D warnings

# TypeScript (T3-T5), after the Node 22 PATH export and npm ci
cd desktop && npx tsc --noEmit
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/agents src/features/environments src/test
cd desktop && node --check test/tauri-mock.js
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000     # whole suite, once at the end
```

The Rust `default` feature build (GTK/webkit) works in this pod; a `Text
file busy` (ETXTBSY) failure in `cargo test` is a known flake: rerun with
`-- --test-threads=1`.

## 5. Repo rules

- Commits: conventional prefix as in `git log` (`feat:`, `fix:`, `test:`,
  `docs:`, `style:`), imperative, concise. **No `Co-Authored-By` trailer**
  (repo rule; it overrides the implementer role's prompt). Commit and push
  after each file group and before the slowest suite.
- Never wait on a permission prompt: list refused or unrunnable commands under
  `## Not run here` in the report and record `PASS` if everything that could
  run passed. Never wait with a loop on `pgrep -f <pattern>` (it matches
  itself); run long jobs in the background and wait for the notice.
- Go (handbook + `make lint`): no blank lines inside function bodies; one
  test file per source file; `Test<Receiver>_<Method>` with `t.Run` per
  behaviour; testify `require`/`assert`; errors wrapped
  `fmt.Errorf("doing x: %w", err)`; early returns. JSON fields `snake_case`,
  a field with no value is `null`, never missing.
- Rust (handbook): rustfmt, Clippy `-D warnings`, inline `#[cfg(test)]`
  unit tests beside the code, no `unwrap()` outside tests, small functions.
- TypeScript (handbook): `strict`, `readonly` fields, function components,
  CSS Modules, Vitest + Testing Library queries by role/label, no `any`.
- Don't change CLI semantics beyond the two additive `ws list --json` fields
  (T1). Don't touch `feat/env-scope` or anything under `internal/` except the
  files T1 names.
