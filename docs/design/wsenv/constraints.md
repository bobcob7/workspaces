# wsenv: environment constraints

Every task in [tasks.md](tasks.md) links this page. It lists what the pod
that runs the implementers can and can't do, the repo's rules that the
tests enforce, and the pinned values the tasks touch. Measured on
2026-11-27 in the ws-online pod (linux/amd64, 48 CPUs, 128 GiB, Go 1.27.3,
Node 20.19.2, Claude Code 2.1.332, installed `ws` v0.5.0).

## What the pod can run

| Can | Can't |
| --- | ----- |
| `go build ./...`, `go test -p 2 ./...`, `go tool golangci-lint run ./...` (pinned through the `tool` directive in `go.mod`) | Desktop tests: `make desktop-test` needs Node 22.12+ or 24.12+; the pod has Node 20.19.2. **No task in this feature touches `desktop/`.** |
| `make build` (writes `./bin/ws`) | A real remote environment: there is no second machine or enrolled agent. Remote behaviour is tested only through `internal/environment/envtest` (an in-process MQTT broker) and the fakes listed below |
| `claude --version`, `claude agents --json`, `claude daemon status`, `claude auth status` (each about 0.3-0.6 s) | A signed-in `claude` from inside an agent session: the shared Claude Code daemon has `CLAUDE_CODE_OAUTH_TOKEN` in its environment, but sessions don't inherit it, so `claude auth status` in a session says `"loggedIn": false`. **Tests must never start a real Claude Code session**; use the `claudeClient` fake in `internal/cli` and a fake `claude` script for `internal/claude` (see `internal/claude/*_test.go`, which already build one with `exectest`) |
| The remote Docker daemon (`DOCKER_HOST`) | Docker bind mounts or published ports (the daemon is remote). Nothing here needs Docker |

- **Run heavy checks one at a time** and with `-p 2`: `go test -p 2 ./internal/cli/` takes several minutes; never run two package suites at once.
- **The installed `ws` is older than the branch.** `/usr/local/bin/ws` is v0.5.0 (built 2026-11-25). Anything a task adds is exercised only through `go test` or a fresh `./bin/ws` from `make build`. Never run `ws` commands other than `ws machine ...` (workspace rule), and never `./bin/ws` against the real `WS_CONFIG_DIR`: use `WS_CONFIG_DIR="$(mktemp -d)" ./bin/ws ...`.
- **No network beyond GitHub and loopback** is needed or assumed.

## Repo rules the build enforces

- **No blank lines inside function bodies.** `go test -count=1 -run '^TestRepository$' ./internal/blanklines/` fails on any. It runs as part of `make lint`.
- **`make lint`** runs golangci-lint (config `.golangci.yml`: `contextcheck`, `errorlint`, `exhaustive`, `gosec`, `nilnil`, `noctx`, `forcetypeassert`, ...) plus grep rules:
  - `claude` is executed only from `internal/claude/` (ADR 0024). New code that runs `claude` (the overlay probe, the pool) goes there.
  - `git` only from `internal/git/`, `gh` only from `internal/gh/`.
  - `internal/environment/envtest` is imported only from `_test.go` files.
- **Go testing standard** (Agent Handbook, Go: Testing): one test file per source file, `Test<Receiver>_<Method>` names, `t.Run` subtests per behaviour, testify (`require` for preconditions, `assert` for checks), `t.Context()`, `t.Parallel()` only for tests that do I/O, moq mocks generated from `interfaces.go` only.
- **ADRs** live in `docs/adrs/NNNN-*.md` and are indexed in `docs/adrs/README.md`. The next free number is **0085** (used by this feature's ADR).
- **Commits:** imperative mood, conventional prefix (`feat:`, `fix:`, `docs:`, `test:`), no `Co-Authored-By` trailer or any AI attribution (user rule, 2026-11-27).
- **Failing-first tests:** every bug fix in tasks.md names a test that fails on the branch before the fix. Commit the test first (`test: ...`), then the fix, so a reviewer can check out the first commit and see it fail.

## Pinned values the tasks touch

| Value | Where | Touched by |
| ----- | ----- | ---------- |
| `envproto.Protocol = 4`, `ProtocolReviewsLoops = 3`, `ProtocolScopes = 4` | `internal/envproto/payloads.go:9-15` | No task bumps the protocol. A fix that needs a new operation or field is out of scope (see Follow-ups in tasks.md) |
| `ws config list` fixed keys: `defaults.host`, `defaults.owners`, `hosts`, `review.timeout`, `review.remove_finished`, listed always | `internal/config/edit.go:395` `fixedRules`, pinned by `internal/cli/config_test.go:87` and `internal/config/edit_test.go:43`, and by the desktop's Settings fixtures (`desktop/src/test/fixtures.ts`) | T3 adds `claude.config_dir` as a **conditional** key, listed only when config.toml has a `[claude]` table (like `runnerFixedRules`), so these pins and the desktop fixtures don't change |
| `runner.roles.implementer.runner` is `keys[len(keys)-9]` | `internal/config/runner_test.go:511` | T3 must not add keys after the runner role keys |
| Claude Code minimum version 2.1.302 | `internal/claude/version.go` (ADR 0024) | unchanged |
| Claude Code's config lock: a `.claude.json.lock` directory, stale after 10 s | `internal/claude/trust.go:25-30` | T2 writes the overlay's `.claude.json` under the same lock |
| Workspace state is deleted with `os.RemoveAll(state/<id>)` | `internal/workspace/remove.go:40`, `create.go:256`, `leftovers.go:34` | T2 puts the overlay in `state/<id>/claude/`. `os.RemoveAll` removes symlinks, not their targets, so the shared `projects/`, `plugins/` and the rest survive |

## Facts about Claude Code measured here (2026-11-27)

These ground ADR 0085; each was run in this pod with Claude Code 2.1.332.

1. **The background daemon and `claude agents` are per config directory.** `claude daemon status` with the default config reports sock dir `/tmp/cc-daemon-1000/3bfeb895` and 13 workers; with `CLAUDE_CONFIG_DIR=<empty dir>` it reports `not running` with sock dir `/tmp/cc-daemon-1000/f68de010`. `claude agents --json` lists 13 sessions by default and `[]` with the other directory, even when that directory's `jobs/` is a symlink to the default one. So a session started with another `CLAUDE_CONFIG_DIR` runs under another daemon and is listed only with that directory.
2. **A daemon costs about 170 MiB** (RSS of `claude daemon run`: 174 584 KiB) and exits once its workers are gone ("to let it idle-exit: wait for (or cancel) bg workers").
3. **`claude agents --json` takes 0.31-0.57 s** per call (3 runs each, default and empty directory).
4. **Sessions don't inherit the daemon's `CLAUDE_CODE_OAUTH_TOKEN`.** The daemon process's environment has it; this session's doesn't, and `claude auth status` here says `"authMethod": "none"`. A `claude --bg` with a new `CLAUDE_CONFIG_DIR` run from inside a session starts a new daemon from the session's environment, which has no token.
5. **`claude auth status` honours `CLAUDE_CONFIG_DIR`** and prints `configDirectory` and `projectsDirectory` under it; it doesn't start a daemon. In a fresh directory Claude Code creates only `.claude.json` and `backups/`.
6. **The shared `~/.claude` shows contention today:** 616 leftover `policy-limits.json.stamp.json.tmp.*` files at its top level, a 264 KiB `~/.claude.json` with 1602 project entries that every session rewrites, and 16 `jobs/` entries for 13 roster workers (the daemon does not delete job directories it doesn't own).

Not verifiable here: macOS Keychain credentials under a custom `CLAUDE_CONFIG_DIR`, how Claude Code writes `.credentials.json` on token refresh (in place, or by rename, which would replace a symlink with a private copy), and whether a caller's environment reaches a session through an existing daemon. ADR 0085 designs around each.
