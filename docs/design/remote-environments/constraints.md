# Constraints for PR 2, "Core"

What a task in [tasks.md](tasks.md) breaks if it forgets something, and
what the build pod can't run. Measured 2026-11-22 on `feat/env-core` at
`be15521`. Every task names the rows here that it touches; it must update
each one in the same commit as the code that changes it.

## Pinned values in the code

### Error codes (`internal/cli`)

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/cli/errors.go`, `const` block of `errorCode` | every code, with a comment naming its ADR | a new code isn't declared here |
| `internal/cli/errors.go`, `errorCode.exitCode()` | a `switch` listing **every** code under its exit code; the `exhaustive` linter fails on a code that is missing | a new code isn't added to its `case` |
| `internal/cli/errors_test.go`, `TestErrorCode_exitCode` | sample codes per exit code (not exhaustive) | add the new codes to the matching subtest |
| `internal/cli/llm_test.go`, `TestGuideTopics` / "the overview lists every error code under its exit code" | parses every `errorCode` constant from the package's non-test `.go` files (`declaredErrorCodes`) and requires each in the row `\| <exit> ` of the `## Output and exit codes` table of `internal/cli/guides/ws.md` | a new code isn't in the overview's row for its exit code |
| `internal/cli/llm_test.go`, "exit code sections name only real error codes" | every `` `snake_code` `` in a guide's `## Exit codes` section must be a declared code | a guide names a code before it is declared |
| `docs/adrs/0016-output-contract.md`, the `Status:` line | lists each ADR that added codes | add "`enrollment_failed`, `enrollment_used`, `environment_unknown`, `environment_exists` and `ca_in_use` added by 0075 (PR 2)" |

New codes in this PR (T8): `enrollment_failed` (1), `environment_unknown`
(3), `enrollment_used` (4), `environment_exists` (4), `ca_in_use` (4).
`environment_offline`, `environment_outdated` and `environment_mismatch`
(ADR 0072) belong to PR 3 and must **not** be declared here: an unused
constant fails `unused`, and a declared code must be in the overview.

### Command tree and `ws llm` guides (`internal/cli`)

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/cli/app.go`, `newRootCmd` | the slice of people commands added under `peopleGroup` | `newEnvCmd()` isn't added |
| `internal/cli/app_test.go:342`, `TestApp_newRootCmd` / "has every command" | `assert.ElementsMatch` over the exact top-level names `claude, shell, restore, restart, continue, loop, list, rm, rename, hide, unhide, cache, config, daemon, review, llm, machine, runner` | `env` isn't added to the list |
| `internal/cli/llm.go`, `guideTopicsBeforeReview()` | ordered list of every visible command path | each visible `env …` path isn't listed (after `"daemon stop"`) |
| `internal/cli/llm_test.go`, `TestGuideTopics` | every **non-hidden** command (walk skips `cmd.Hidden` subtrees, `help`, `completion`) has `guides/ws-<path>.md`; every guide file has a topic; no duplicates; every local flag (`--finish`, `--force`, …) appears in its guide; every command that supports `--json` has a `## JSON output` section with a ```` ```json ```` block; every command guide starts `# ws <path>\n`, has a synopsis line `\nws <path>`, a non-empty `## Exit codes` and `## Examples`; the overview `guides/ws.md` contains `` `ws llm <path>` `` for every topic | any of these is missing |
| `internal/cli/guides/*.md` | embedded with `//go:embed guides/*.md` | a stray file without a topic fails "every guide is a command's" |
| `internal/cli/guides/ws-daemon-status.md` | its JSON example | `ws daemon status` gains `environments` (T6) |

`ws env agent` and its subcommands are **hidden** (`Hidden: true` on the
`agent` command), as ADR 0073 says, so they need no guide and don't appear in
`ws llm`. The visible topics this PR adds are exactly: `env`, `env add`,
`env list`, `env rm`, `env rotate`, `env ca`, `env ca rotate`,
`env ca status` (8 guide files).

### Config keys (`internal/config`)

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/config/config.go`, `coreFile` | the strictly decoded tables (`defaults`, `hosts`, `review`); unknown keys there are `ErrInvalid` | `[environments]` must be added here so its unknown keys are errors |
| `internal/config/runner.go`, `runnerTableNames()` = `runner, endpoints, models, daemon` | tables cut out of strict decoding and decoded leniently | **don't** add `environments` here: it is a core table |
| `internal/config/edit.go`, `Settings` | key order: fixed keys, then runner/daemon keys (only if those tables exist), then profiles, then entries | the `environments.*` keys must be appended **last** and only when `meta.IsDefined("environments")`, or the index pins below move |
| `internal/config/edit_test.go:34`, `TestSettings` "default file gives the five fixed keys" | exactly 5 settings for `defaultFile` | `defaultFile` gains an `[environments]` table (keep it commented-out or absent; this PR leaves `defaultFile` unchanged) |
| `internal/config/edit_test.go:49` | `require.Len(t, settings, 7)` for default + one profile | same |
| `internal/config/runner_test.go:500`, `TestRunnerEdit_Settings` | `keys[5]`, `keys[9]`, `keys[14]`, `keys[19]`, `keys[len(keys)-9]` | environment keys are inserted anywhere but the end |
| `internal/config/edit.go`, `valueOf`, `defaultOf`, `leafType`/`parseKeyWith` | `switch rule.key` per fixed key | each new key needs a case |
| `docs/config.md` `## Schema` and `### Layout` | one `###` section per table, the layout block of `CONFIG_DIR` | add `### [environments]` after `### [daemon]`, and `environments/` to the layout |
| `docs/tools/ws-config.md` `## Keys` table | one row per key | add the four `environments.*` rows after `daemon.resume_on_start` |
| `desktop/src/test/fixtures.ts:392`, `desktop/src-tauri/src/ws_config.rs:327` | fixtures of `ws config list --json` for the default file | unaffected as long as the default file lists no new key (it doesn't); don't touch desktop |

### Daemon (`internal/daemon`)

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `daemon.Serve(ctx, p, origin, sup, log)` | signature; callers: `internal/cli/daemon.go:75`, `internal/cli/daemon_test.go:46`, `internal/cli/runner_test.go:297`; internal `serve(...)` callers: `internal/daemon/server.go:59`, `server_test.go:61,89,112`, `autostart_test.go:72` | T6 adds an `envs EnvironmentHost` parameter: every caller passes `daemon.NopEnvironments{}` |
| `internal/daemon/interfaces.go` `//go:generate go tool moq -out moq_test.go . RunnerSupervisor processReader processStarter RunnerRecords` | the moq list | add `EnvironmentHost` and run `go generate ./internal/daemon/` |
| `daemon.Status` JSON (`client.go`) | fields `running … warnings`; documented in `guides/ws-daemon-status.md` and `docs/tools/ws-daemon.md` | the new `environments` field isn't documented |
| `desktop/src-tauri/src/runner_service.rs:14` `DaemonStatus` | serde struct **without** `deny_unknown_fields` | nothing: a new field is ignored by the desktop (checked) |
| `server.idleExit` | a transient daemon exits after 15 min of `sup.Idle()` and no request | a daemon with listeners open must not idle-exit (T6) |

### `internal/cli/interfaces.go`

`//go:generate go tool moq -out moq_test.go . configLoader repoCache workspaceStore lifetimeManager shellRunner claudeClient ghClient runnerDaemon`.
A task that adds a consumer interface in `internal/cli` appends its name
here and regenerates `internal/cli/moq_test.go` (`go generate ./internal/cli/`).
Never hand-edit a `moq_test.go`.

### Lint rules (`.golangci.yml`, `Makefile`)

| Rule | Effect on this PR |
| --- | --- |
| `depguard` `main`: `math/rand$` denied | backoff jitter uses `math/rand/v2` |
| `depguard` `cobra`: only `internal/cli`, `cmd/ws` | `internal/environment`, `internal/envagent`, `internal/envproto` never import cobra |
| `forbidigo`: `fmt.Print*`, `print`, `log.*`, `os.Stdout/Stderr/Stdin`, `os.Exit` | write through injected writers and `*slog.Logger` |
| `gochecknoglobals`, `gochecknoinits` | no package vars except `Err*`/`err*` sentinels and `regexp.MustCompile` patterns |
| `sloglint`: no global logger, static messages, `snake_case` keys, no mixed args | `log.InfoContext(ctx, "environment online", "id", id)` |
| `tagliatelle`: JSON and YAML tags `snake_case` | all wire and output structs |
| `gosec` (not in tests) | G402 for `InsecureSkipVerify` in the enroll pin check needs `//nolint:gosec // G402: VerifyConnection checks the chain against the pinned CA fingerprint (ADR 0075).`; files holding keys are written 0600 (G306); G304 file reads need a path built from a trusted dir |
| `noctx` | `net.ListenConfig{}.Listen(ctx, …)`, `http.NewRequestWithContext`, `tls.Dialer.DialContext` |
| `funlen` 60 lines / 40 statements, `cyclop` 15, `gocognit` 20, `nestif` 4 | split long handlers; tests are exempt |
| `errname` | sentinels `ErrX`/`errX`, error types `xError` |
| `godot`, `revive` `exported` | every comment ends with a period; every exported identifier has a doc comment |
| `testifylint`, `thelper`, `usetesting` | `require` for preconditions, `assert` for checks, `t.Context()`, `t.TempDir()` |
| `exhaustive` (default-signifies-exhaustive) | `switch` on an enum lists every case or has `default` |
| `Makefile` `lint` grep: `exec.Command…"claude"` only in `internal/claude/` | the agent's hello asks `internal/claude` for the version (T9 adds `(*claude.Client).Version`) |
| `Makefile` `lint` grep (new, T1): `internal/environment/envtest` imported only from `_test.go` files | added by T1 |
| `depguard` (new, T10): `github.com/eclipse/paho.golang` only in `internal/mqttclient` | added by T10 |
| `depguard` (new, T10): the broker module only in the adapter package | added by T10 |
| Owner rules not enforced by a linter | no blank lines inside function bodies; interfaces at the consumer, all in one `interfaces.go` per package with one `go:generate` moq line; mocks only in `moq_test.go`; unexported by default; constructor injection with `*slog.Logger` passed in; tool versions only in `go.mod`'s `tool` directive; `t.Context()` in tests; `require` for preconditions, `assert` for verifications |

### Docs with lists

| File | What to add |
| --- | --- |
| `docs/tools/README.md` command table | a `ws env` row after `ws daemon` |
| `docs/state.md` `## Layout` table | rows for `environments/ca/`, `environments/<id>.toml`, `environments/revoked`, `environments/.lock`, `state/.environments/<name>/snapshot.json`, and the agent's `environment/` |
| `docs/config.md` `### Layout` block | `environments/` and, on an agent, `environment/` |
| `docs/adrs/README.md` index rows 0073, 0074, 0075 | status `Proposed` → `Accepted` |
| `docs/adrs/0073-…`, `0074-…`, `0075-…` `Status:` line | `Accepted (2026-…)`, with the amendments T8 lists |
| `README.md` `## Roadmap` | an entry for PR 2 (ADR 0076: "README roadmap gets an entry for each PR") |
| `docs/tools/ws-daemon.md`, `internal/cli/guides/ws-daemon-status.md` | `environments` in the JSON example |

## What the pod can and can't run

Measured in this pod: `uname -m` = `x86_64`, 48 CPUs, 128 GB RAM;
`go version` = `go1.27.3 linux/amd64` (`go.mod` says `go 1.27.0`; CI uses
`1.27.x`); `node --version` = `v20.19.2`; `make` present;
`golangci-lint` only through `go tool golangci-lint` (the `make lint`
target); the module proxy is reachable (`go list -m -versions
github.com/eclipse/paho.golang` lists up to `v0.23.0`); loopback TCP
listen and connect work (`127.0.0.1:0`).

| Can't | Consequence for tasks |
| --- | --- |
| Windows | no `GOOS=windows` build or test; Windows is out of scope (ADR 0076). Don't add `_windows.go` files |
| macOS | CI's macOS legs check darwin; use no Linux-only syscalls in the new packages (they need none) |
| A local Docker daemon (`DOCKER_HOST` is a remote `tcp://docker-host…:2375`) | no testcontainers, no published ports, no bind mounts; no external MQTT broker. Every test runs in-process: `envtest` in memory, or the real embedded broker on `127.0.0.1:0` (T10) |
| Node 22.12+ | the desktop app can't be tested here; this PR doesn't touch `desktop/` |
| Inbound connections from another machine | a real remote agent is never tested here; controller and agent run in one test process on loopback |
| `make test` cheaply | run `go test -p 2` on the task's packages; `make lint` lints everything (about 2-4 min) and is run once per task, last |

Run heavy checks one at a time: `go test -p 2 ./internal/<pkg>/...`, then
`make lint`. Never wait on a permission prompt: list a check you couldn't
run as unverified in the report.

## Not verified by this design

- `make lint` and `make test` on `feat/env-core` before T1 (the
  orchestrator's baseline verifier covers it).
- That the broker module the spike picks exposes, per connection, the
  verified TLS state, the client id, and hooks for connect, publish and
  subscribe decisions; T10 must stop and report if it doesn't.
- How Claude Code reports "signed out"; the hello reports only `missing` or
  `present` in this PR (T9), and `signed_out` is never sent yet.
- Will Delay in the in-memory broker: `envtest` publishes a will at once;
  the delay is checked only against the real broker in T10.
