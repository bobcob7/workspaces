# Constraints for `env-scope-followups`: rollup, `ws list` nesting, New agent scope

What this pod can and can't check, how long each check takes, the values
tests pin, the hostile-input limits, and the repo rules every task follows.
Probed 2026-11-25 on `main` at `f87fd52` (after #89 and #90 merged). The
decisions are in [design.md](design.md) and
[ADR 0083](../../adrs/0083-env-scope-followups.md); the order is in
[tasks.md](tasks.md).

## 1. Environment proof (probed, not assumed)

Pod: x86_64, 48 CPUs, 128 GB RAM (`free -m`: ~103 GB available), Go
1.27.3, cargo/rustc 1.99.1, system Node **v20.19.2**, `ws` v0.4.0 installed
(`./bin/ws` after `make build` is `v0.4.0-4-gf87fd52`). Playwright
headless shells cached: `chromium_headless_shell-1208` (Playwright 1.58.2)
and `-1255` (1.65.1).

| Command (repo root unless noted) | Result | Wall time |
| --- | --- | --- |
| `go build ./... && make build` | ok | 33 s |
| `make lint` (golangci-lint, both tag sets, grep rules, `internal/blanklines`) | `0 issues`, exit 0 | 27 s |
| `go test -p 2 -count=1 ./internal/cli/ -run 'TestApp_annotateScopes\|TestApp_runList\|Progress\|MachineChildren\|Remote'` | ok | 11 s |
| `go test -p 2 -count=1 ./internal/envscope/ ./internal/environment/ ./internal/machine/` | ok | 14 s |
| Node 24.22.3 official tarball, SHA256 checked (below) | `node-v24.22.3-linux-x64.tar.xz: OK` | 10 s |
| `cd desktop && npm ci` (Node 24) | ok | 15 s |
| `cd desktop && npx tsc --noEmit` (Node 24) | ok | 8 s |
| `cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/create src/features/agents src/features/environments` | 25 files, 391 tests passed | 33 s (61 s on the first, cold run) |
| `cd desktop && node --check test/tauri-mock.js` | ok | <1 s |
| `cd desktop/src-tauri && cargo test -j 2 --lib commands` (cold, default `desktop` feature) | 32 passed, 2 ignored | 184 s (compiles the crate; later runs are seconds) |
| `./bin/ws list --json --all` object sizes, re-encoded compactly | 8 objects, mean 712 B, max 810 B; `progress`+`rollup` ≤ 196 B with a note | — |

Not probed this run: the full `go test ./...` (about 5 min in earlier
runs; leave it to the verifier), `cargo clippy`, the `online` feature
build, a screenshot (env-scope-ui's constraints §1 recorded Vite + mock +
Playwright working on this image; both Chromium revisions are cached now).

### Node 24 (required for vitest; system Node 20 is outside vitest's range)

```bash
T=$CLAUDE_JOB_DIR/tmp/node; mkdir -p "$T" && cd "$T"
V=v24.22.3; A=linux-x64; [ "$(uname -m)" = aarch64 ] && A=linux-arm64
curl -fsSLO https://nodejs.org/dist/$V/SHASUMS256.txt
curl -fsSLO https://nodejs.org/dist/$V/node-$V-$A.tar.xz
grep " node-$V-$A.tar.xz\$" SHASUMS256.txt | sha256sum -c -   # must print OK
tar -xJf node-$V-$A.tar.xz
export PATH="$T/node-$V-$A/bin:$PATH"; node --version            # v24.22.3
```

`PATH` doesn't persist between Bash calls: prefix each desktop command with
the `export PATH=...` line. Then `cd desktop && npm ci` once.

### Playwright (screenshot pass only)

Not a `desktop/` dependency. Install outside the repo
(`$CLAUDE_JOB_DIR/tmp/pw`, `npm i playwright@1.58.2`); the ESM script
lives beside that `node_modules`. `npx vite --port 1427 --strictPort` in
`desktop/` (background), `page.addInitScript({ path: "desktop/test/tauri-mock.js" })`
before `page.goto`. Stop Vite afterwards; leave no `.playwright-mcp/`.

## 2. What can't run here

| Thing | Why | Instead |
| --- | --- | --- |
| A second environment, a real snapshot with progress, a real `scope.children` over MQTT | one pod | Go tests write snapshots with the store helpers in `list_env_test.go` (`writeRemoteSnapshot`, `enrollEnvironment`) and the ledger with `envscope.NewLedger(dir).Put`; controller tests use the existing `environment` test helpers (`scope_test.go`) |
| The Tauri shell (`npm run tauri dev`) | no display | `cargo test`/`clippy`, and Vite + `test/tauri-mock.js` in Playwright |
| `make desktop-test` in one go | runs `npm ci` with system Node 20 | run its steps by hand with Node 24 (§4) |
| Windows / macOS | no host | nothing here is platform-specific |

Heavy checks one at a time: `go test -p 2`, `npm test -- --maxWorkers=2
--testTimeout=60000` (or `npx vitest run --maxWorkers=2 --testTimeout=60000`),
`cargo test -j 2 ... -- --test-threads=1`. A `Text file busy` (ETXTBSY)
failure in `cargo test` is a known flake: rerun with `-- --test-threads=1`.

## 3. Pinned values a task can break

| Pin | Where | What to do |
| --- | --- | --- |
| Exact `ws list --json` objects | `internal/cli/list_test.go`: **12** `assert.JSONEq`; `list_env_test.go` pins `"progress":null,"rollup":null` on remote objects | none of these tests has a ledger, so F2/F3 must leave them **unedited and passing** (the no-ledger path is unchanged) |
| Snapshot objects | `env_agent_test.go` `TestAgentSnapshots_Workspaces` (2 subtests: `[]`, and `Contains "id":"quiet"`) | F2-T2 keeps both and adds subtests |
| Table output | `list_test.go` table tests (`printList`), `list_env_test.go` ENV column tests | no-ledger output byte-identical (F3-T1) |
| `--parent` | `list_env_test.go` / `list_test.go` `--parent` subtests (`grep -n "parent:" internal/cli/list*_test.go`) | same-environment results unchanged; `usage` for two `--env` still |
| Progress guide facts | `llm_test.go` "the facts agents get wrong are where they look": `ws-machine-progress.md` must keep `ws machine result PASS`, "`show` is optional", "leaves `done` alone", "one unit of your own" | docs tasks append, never reword those |
| Guides | `llm_test.go`: every flag in its guide, JSON documented, `Exit codes` section names only real codes | no task adds a flag or an error code |
| `envproto.ScopeChild` | `internal/envproto/scope_test.go`, `internal/environment/scope_test.go` (`grep -rn "ScopeChild{" internal`) | F2-T1 adds `Rollup`; literals that list every field get `Rollup: nil` |
| Protocol | `envproto.Protocol = 4` | unchanged (no op added) |
| Rust `NewAgent` literals | **7**: `commands.rs` tests at ~l. 821, 836, 841, 895, 926, 957 and `reviews/fix.rs` `new_agent` (~l. 200) (`grep -rn "NewAgent {" desktop/src-tauri/src`) | F1-T2 adds `env_scope: None` to each (no `Default` derive) |
| Tauri command lists | `lib.rs` `invoke_handler`, `online/dispatch.rs` `COMMANDS` | **unchanged**: no task adds a command |
| TS `NewAgent` | `desktop/src/api.ts` ~l. 172 | F1-T3 adds optional `envScope` |
| Test fixtures | `desktop/src/test/fixtures.ts`: `fakeApi` (`configList` ~l. 683, `createAgent` ~l. 692, `environmentsList` ~l. 795), `configListing()` ~l. 421 (no `environments.default_scope`), `remoteEnvironment()` ~l. 74 | F1-T3 adds a `configListing` override helper; defaults unchanged |
| Screenshot mock | `desktop/test/tauri-mock.js`: `entry()` ~l. 31, `remoteEntry()` ~l. 66, `leafRollup` ~l. 73, `plan-scope` family ~l. 893-923, `config` ~l. 927, `environments` ~l. 398 (`linux-server` online `env_7f3a`, `win-rig` offline `env_b20d`), `create_agent` ~l. 1518 | F2-T4 and F1-T3 edit exactly the parts their task names; nothing tests this file: `node --check` it |
| Mock description | `desktop/README.md` "Screenshots without the backend" (~l. 398) | each mock edit adds one sentence there |
| Existing NewAgentDialog tests | `NewAgentDialog.test.tsx` (`not.toHaveProperty("environment")` for local, etc.) | must pass unedited |

## 4. Verify commands (copy exactly)

```bash
# Go
go build ./...
go vet ./internal/... && test -z "$(go tool golangci-lint fmt --diff ./internal/...)"
go tool golangci-lint run ./internal/...
go test -count=1 -run '^TestRepository$' ./internal/blanklines/
go test -p 2 -count=1 ./internal/envscope/ ./internal/environment/ ./internal/machine/ ./internal/envproto/
go test -p 2 -count=1 ./internal/cli/                       # ~50 s; use -run while iterating
make lint                                                   # once, before the final push

# Rust (F1-T2)
cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings
cd desktop/src-tauri && cargo test -j 2 --lib commands -- --test-threads=1
cd desktop/src-tauri && cargo test -j 2 --lib reviews::fix -- --test-threads=1
cd desktop/src-tauri && cargo clippy --no-default-features --features online --all-targets -- -D warnings

# TypeScript, after the Node 24 PATH export and npm ci
cd desktop && npx tsc --noEmit
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/create src/features/environments src/features/agents
cd desktop && node --check test/tauri-mock.js
cd desktop && npm test -- --maxWorkers=2 --testTimeout=60000     # whole suite, once at the end of a UI task
```

## 5. Hostile-input limits (every reviewer checks the ones its task names)

`machine.record` copies, snapshots and `scope.children` replies are produced
by other machines; the ledger and `config.toml` by any process of the uid.

| Limit | Value | Task |
| --- | --- | --- |
| Snapshot array decoded by `envscope.ListedOf` | ≤ 1 MiB (else error, environment skipped) | F2-T1 |
| Workspaces kept per environment | ≤ 4096 (`MaxListedPerEnvironment`; rest skipped) | F2-T1 |
| Workspace id kept | 1-128 bytes, no control characters (else skipped) | F2-T1 |
| A remote `rollup` used | `0 ≤ done ≤ total ≤ 1_000_000_000` (`MaxRollupSteps`), else zero + warning | F2-T1, F2-T4 |
| Merged sums | saturate at `MaxRollupSteps`, `done ≤ total` | F2-T1 |
| Ledger hops in one merge | ≤ 64 (`MaxMergeDepth`); each ledger entry merged once (cycles add zero) | F2-T1 |
| Ledger entry with the same environment as its parent | ignored | F2-T1 |
| Same-environment parent links | seen set (cycles end) | F2-T1, F3-T1 |
| Ledger file | existing: ≤ 8 MiB, ≤ 4096 entries, strict decode, validated ids | (unchanged) |
| `scope.children` `rollup` | used only if `Valid()` | F2-T3 |
| Snapshot `progress.note` | always `null` in new snapshots (size) | F2-T2 |
| `envScope` names | 1-64 names; each 1-64 bytes, no whitespace, comma, control char, leading `-`; unique; non-empty list | F1-T2 |
| `default_scope` preselection | only `local` or enrolled names; others listed as left out | F1-T3 |
| Paths | no path built from a snapshot id, ledger id or scope name | all |

## 6. Repo rules

- Commits: conventional prefix as in `git log` (`feat:`, `fix:`, `test:`,
  `docs:`), imperative, concise. **No `Co-Authored-By` trailer** (repo rule;
  it overrides the implementer role's prompt). Commit and push after each
  file, and before the slowest suite.
- Never wait on a permission prompt: put refused or unrunnable commands
  under `## Not run here` in the report and record `PASS` if everything that
  could run passed. Never wait with a loop on `pgrep -f <pattern>` (it
  matches itself); run long jobs in the background and wait for their
  notice; before `ws machine result`, make sure none of your background jobs
  still runs.
- Go (handbook + `make lint`): **no blank lines inside function bodies**
  (`internal/blanklines` test); one test file per source file
  (`rollup.go` → `rollup_test.go`, `list_tree.go` → `list_tree_test.go`,
  `list_rollup.go` → `list_rollup_test.go`); `Test<Receiver>_<Method>` or
  `Test<Func>` with `t.Run` per behaviour; testify `require` for
  preconditions, `assert` for checks; `t.Context()`; `t.Parallel()` only for
  tests that block on I/O; errors wrapped `fmt.Errorf("doing x: %w", err)`;
  early returns; JSON `snake_case`, empty values `null`, never missing.
- Rust (handbook): rustfmt, Clippy `-D warnings`, inline `#[cfg(test)]`
  tests, no `unwrap()` outside tests, small functions.
- TypeScript (handbook): `strict`, `readonly`, function components, CSS
  Modules or MUI as the file around it does, Vitest + Testing Library
  queries by role/label, no `any`.
- Failing-first: every new behaviour's test is committed failing first
  (commit `test: …`, then the fix). Tests that pin behaviour that already
  works (F1-T1) say so in the report, and the reviewer mutates the code to
  see them fail.
