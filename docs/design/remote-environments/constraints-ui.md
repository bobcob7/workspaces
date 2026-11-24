# Constraints for PR 4, "Terminals and desktop"

What a task in [tasks-ui.md](tasks-ui.md) breaks if it forgets something,
and what the build pod can't run. Measured 2026-11-23 on `feat/env-ui` at
`320b683` (PR 3 merged). Every task names the rows here that it touches; it
must update each one in the same commit as the code that changes it. The PR
2 and PR 3 sheets, [constraints.md](constraints.md) and
[constraints-ops.md](constraints-ops.md), still hold for what they list.

## Toolchains in this pod

| Tool | Here | Needed |
| --- | --- | --- |
| Go | `go1.27.3 linux/amd64` (`go.mod`: `go 1.27.0`) | as is |
| golangci-lint | only `go tool golangci-lint` through `make lint` | as is |
| Rust | `rustc 1.99.1`, `cargo 1.99.1` | `rust-version = "1.90"` (`desktop/src-tauri/Cargo.toml`) |
| Node | `v20.19.2`, npm `9.2.0` | **22.12+**: `vitest` declares `^22.12.0 \|\| ^24.0.0 \|\| >=26.0.0` and `@testing-library/jest-dom` `>=22` (`desktop/package-lock.json`). Install v22.23.5 from the official tarball into `$CLAUDE_JOB_DIR/tmp/node`, SHA-256 checked (commands in tasks-ui.md › Rules); never into the repo |
| Memory, CPUs | 125 GiB, 48 (this pod; children may get 8 GB) | one heavy child at a time on 8 GB |

`desktop/README.md` still says "Node.js 20+"; T18 corrects it to 22.12+.

## The desktop commands (`desktop/README.md`, `Makefile`)

```
make desktop-test
  = cd desktop && npm ci && npm run typecheck && npm test
  + cd desktop/src-tauri && cargo test && cargo clippy --all-targets -- -D warnings
  + cd desktop/src-tauri && cargo test --no-default-features --features online && cargo clippy --no-default-features --features online --all-targets -- -D warnings
```

Tasks run the pieces one at a time with `--maxWorkers=2 --testTimeout=60000`
(vitest) and `-j 2` (cargo). `npm ci` needs network access to the npm
registry (the lock file is pinned; no package changes).

## What the pod can't run

| Can't | Consequence |
| --- | --- |
| The Tauri GUI (no display, likely no `webkit2gtk`/`libsoup` system libraries) | the `desktop` feature may not build here: run the `--no-default-features --features online` cargo commands, which compile `commands.rs`, `pty.rs`, `model.rs`, `online/dispatch.rs`, `environments.rs` and the tray logic's pure parts; list the `desktop` feature build as unverified and leave it to CI |
| A browser other than Playwright's | screenshots only through the orchestrator's child with `desktop/test/tauri-mock.js` and `web-mock.js` |
| Docker with published ports (the daemon is remote) | `make online-smoke` and the e2e image can't run; Workspace Online is checked with vitest and the `online` cargo tests |
| A real remote machine | every terminal test runs controller, broker and agent in one process: `envtest` (T4, T6) and the real embedded broker on loopback (T1, T11) |
| A real WAN | T1 simulates one with an in-process throttling proxy (RTT and bandwidth only, no loss) |
| A signed-in `claude` | attach is tested with a fake `claude` script that `exec cat` (T11) or a `ptyStarter` mock (T4) |
| Windows | no `_windows.go` files; `ptyproc_other.go` and `rawterm_other.go` refuse |
| macOS | `GOOS=darwin go vet` for `ptyproc` and `rawterm_darwin.go`; CI's macOS legs run them |
| `make test` cheaply | run `go test -p 2` on the task's packages; `internal/cli` alone takes minutes |

## Pinned values in the code

### Error codes (`internal/cli`) — T8

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/cli/errors.go`, the `const` block | every code with its ADR comment | `terminal_limit` isn't declared under `// From PR 4 (ADR 0074).` |
| `errors.go`, `errorCode.exitCode()` (`exhaustive`) | every code under its exit | `codeTerminalLimit` isn't in the `exitRefused` case |
| `errors.go`, `errorCode.known()` | every code ws knows; unknown remote codes become `internal` (PR 3's D9) | `codeTerminalLimit` isn't listed: an agent's `terminal_limit` would show as `internal` |
| `internal/cli/errors_test.go`, `TestErrorCode_exitCode` | samples per exit | not added to the exit 4 subtest |
| `internal/cli/llm_test.go`, `TestGuideTopics` "the overview lists every error code under its exit code" | `internal/cli/guides/ws.md` line 177 (the `\| 4 ` row) | `terminal_limit` isn't in the row |
| `TestGuideTopics` "exit code sections name only real error codes" | codes in each guide's `## Exit codes` are declared | `ws-shell.md`/`ws-attach.md` name `terminal_limit` before `errors.go` declares it (same commit) |
| `unused` linter | a declared code is used | declare it only in T8, where `runRemoteTerminal` maps the agent's reply |
| `docs/adrs/0016-output-contract.md` `Status:` | lists ADRs that added codes | T18 adds `terminal_limit` |

### Commands and guides (`internal/cli`) — T8, T10

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/cli/app_test.go:342`, `TestApp_newRootCmd` "has every command" | `ElementsMatch` of 19 names | `attach` (T8) and `stop` (T10) aren't added: 21 names |
| `internal/cli/llm.go`, `guideTopicsBeforeReview()` | `ws llm --all`'s order | `"attach"` (after `"shell"`) and `"stop"` (after `"rm"`) missing: `TestGuideTopics` "every command has a guide" fails |
| `internal/cli/guides/ws-attach.md`, `ws-stop.md` | one guide per command | missing |
| `TestGuideTopics` "every local flag appears in its guide" | each command's flags in its guide | `--env` missing from `ws-shell.md`, `ws-rm.md`, `ws-rename.md`, `ws-hide.md`, `ws-unhide.md`, `ws-attach.md`, `ws-stop.md` |
| `ws shell`, `ws attach`: `PreRunE: a.noJSON` | their guides have no `## JSON output` | a JSON section is added |
| `internal/cli/guides/ws.md` commands list (around line 235) | `ws llm <topic>` lines | `attach`, `stop` missing (not a test failure, but `ws llm` lies) |
| `app.go` people group | `ws --help` "Commands for people and the desktop app" | `attach`, `stop` must be in `peopleGroup`, not `agentsGroup` |
| `docs/tools/README.md`, `README.md` command tables | one row per command | T10/T18 rows |

### `internal/cli/interfaces.go` — T8

`//go:generate go tool moq -out moq_test.go . configLoader repoCache workspaceStore lifetimeManager shellRunner claudeClient ghClient runnerDaemon envBroker envClient environmentCaller`
(line 22). T8 appends `terminalOpener` and `remoteTerminal`, then
`go generate ./internal/cli/`. Never hand-edit `moq_test.go`.

### `ws list` JSON and text — T9

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/cli/list_test.go:98-106`, "--json" golden | each entry's exact keys | every entry (the unknown directory too) gains `"agent_state":null` |
| `internal/cli/list_test.go` empty listings (`{"workspaces":[],"hidden_count":N,"warnings":[...]}`) | | unchanged |
| `listWorkspaceJSON` (embedded in `machineChildJSON`, `machine_result.go:50`) | `ws machine children` JSON and its guide | `agent_state` must go on `listEntryJSON`, not `listWorkspaceJSON` |
| `internal/cli/list.go:46`, `--env` help "default: local" | the flag's text in `ws-list.md` | must say "default: every environment" |
| `internal/cli/list.go:150` "`--parent` takes one `--env`" | `list_env_test.go` | with no `--env`, `--parent` now finds its environment (D9); the error stays for two explicit `--env`s |
| `list_env_test.go:48` snapshot fixture `"protocol":1` | | unchanged (a protocol-1 snapshot is still read) |
| `list_test.go:503` "the ENV column appears only with a remote entry" | text header | unchanged |
| `desktop/src-tauri/src/commands.rs:64` runs `ws list --all` | the desktop's listing | now includes remote entries: T12 must key them (`env:<name>:<id>`) in the same PR, which is why T12 depends on T9 |
| `desktop/src-tauri/src/model.rs:14-50` `WsList`, `WsWorkspace` | serde without `deny_unknown_fields` | nothing breaks; T12 reads the new keys |
| Scripts using `ws list -q` | ids only | stays local by default (D9) |

### Wire payloads (`internal/envproto`) — T2

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/envproto/payloads.go:9`, `const Protocol = 1` | the hello's `protocol` | becomes 2 |
| `internal/envproto/payloads_test.go:92`, `:124` | `"protocol":1` literals | must become 2 (or use `Protocol`) |
| `internal/cli/env_daemon_test.go:234`, `internal/daemon/server_test.go:415`, `internal/environment/snapshot_test.go:22`, `list_env_test.go:48` | protocol 1 in fixtures of a hello or snapshot | unchanged: they describe a protocol-1 agent, still valid |
| `envproto.LookupOp` | the allow-list; `ops_test.go` covers each op | two new ops; `CallerTerminal` is a new `Caller` value: keep the existing values (1, 2, 3) |
| `envproto.MaxRequestBytes` 256 KiB; `internal/mqttbroker/broker.go:95` `MaximumPacketSize = MaxRequestBytes + 16 KiB` | the biggest packet | stream frames are ≤ 16 KiB + 10 bytes: far under it |
| `envproto.validateKind` refuses `shell` with "a shell on an environment needs terminal streams" (`ops.go:351`) | `workspace.create` | keep: a shell is `terminal.open`; T18 may reword the message to name `ws shell --env` |

### `envagent` — T4

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/envagent/interfaces.go:10` `//go:generate go tool moq -out moq_test.go . mqttClient snapshotSource claudeProbe operationRunner` | the moq list | T4 appends `terminalCommands ptyStarter ptyProcess`; regenerate |
| `envagent.NewAgent(...)` callers: `internal/cli/env_agent.go`, `internal/envagent/*_test.go`, `internal/mqttbroker/conformance_test.go` | signature | unchanged by design (terminals come through the `ops` value) |
| `envagent.NoOperations{}` | used by the conformance test | doesn't implement `terminalCommands`: `terminal.open` answers `usage` there until T11 passes the cli operations |
| the session loop (`Agent.serve`) | operations run on goroutines (PR 3) | terminal frames must not run on the loop either; the `stream/+/in` handler only enqueues |
| `internal/environment/policy.go:70-107` agent ACL | `stream/<x>/out` publish, `stream/<x>/in` subscribe (with `+`) | already allows terminals; no change. A second stream connection (D1 fallback) would change it |

### Controller (`internal/environment`) — T6

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/environment/interfaces.go:10` `//go:generate … . broker` | moq list | unchanged |
| `c.mu` never held across a broker call (comment in `controller.go`) | the broker's handlers take `c.mu` | `OpenTerminal` and `onStreamOut` must follow it |
| The broker's in-process `Subscribe` callback | may run on the publishing client's goroutine (T1 measures) | `onStreamOut` blocking would stall the agent's connection: it must only enqueue |
| `Controller.subscribe` filters | `status`, `snapshot`, `up`, `res/+` | gains `ws/v1/env/+/stream/+/out` |
| `Controller.Call` | refuses ops whose caller isn't `CallerController` (`ErrNotForwarded`) | `CallerTerminal` must be refused there too |

### Daemon (`internal/daemon`) — T7

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/daemon/interfaces.go:13` `//go:generate … RunnerSupervisor processReader processStarter RunnerRecords EnvironmentHost` | moq list | unchanged list; regenerate after `EnvironmentHost` gains `OpenTerminal` (and `TerminalStream` if mocked: append it) |
| `EnvironmentHost` implementations: `NopEnvironments` (`environments.go`), `envHost` (`internal/cli/env_daemon.go`), `EnvironmentHostMock`, and `reloadRecorder` (`internal/cli/env_daemon_test.go`, hand-written) | method set | each gains `OpenTerminal`, or the cli tests stop compiling |
| `server.routes()` | seven routes | the terminal route is the eighth |
| `http.Server{ReadHeaderTimeout: shutdownWait}`, no `WriteTimeout` | long requests | don't add one; a hijacked connection has none anyway |
| `server.touching` and `idleExit` | the transient daemon exits when idle | an open terminal isn't a request in flight: count it, or the daemon exits under `ws attach` |
| `peerListener{uid: os.Getuid()}` (`server.go:150`) | same-user check on every connection | unchanged; the upgrade is on the same listener |
| `daemon.Status` JSON and `ws-daemon-status.md` | | unchanged (terminals aren't reported in PR 4) |

### Broker and streams — T1, T1a, T4, T11

| Value | Where | Note |
| --- | --- | --- |
| `MaximumClientWritesPending = 256` | `internal/mqttbroker/broker.go:96` | a slow subscriber loses QoS 0 frames beyond it: the gap check is required |
| `MaximumPacketSize = 256 KiB + 16 KiB` | `broker.go:95` | stream frames are far below |
| keep-alive 30 s, will delay 10 s | `envagent` | offline after ~11 s (killed) or ~56 s (silent): a terminal ends then (D10) |
| mochi v2.7.9, paho v0.23.0 | `go.mod` | no change |
| `TCP_NOTSENT_LOWAT` 16 KiB (`envproto.AgentNotSentLowWater`) | the agent's one connection, set in `mqttclient`'s dial (T1a) | bounds the not-yet-sent queue so bulk output can't delay control requests (D1's Result); 0 = OS default |

### Lint and test rules (all tasks)

| Rule | Effect |
| --- | --- |
| `internal/blanklines` (`make test`, last line of `make lint`) | no blank line inside any function body in new files |
| `Makefile` `lint` grep: `exec.Command…"claude"` only in `internal/claude/` | T5 resolves `claude` with `exec.LookPath("claude")` and `ptyproc` runs a path, never `exec.Command("claude", …)` |
| `Makefile` grep: `(syscall\|unix).Exec` only in `internal/claude/` | local `ws attach` calls `a.claude.Attach` (which execs); nothing else execs |
| `Makefile` grep: `"/bin/sh"` in `exec.Command` only in `_test.go` or `internal/gate/` | `ptyproc` tests may use it; code may not |
| `Makefile` grep: `envtest` only from `_test.go` | T4, T6 tests only |
| `depguard`: `cobra` only in `internal/cli`, `cmd/ws` | `envproto`, `envagent`, `environment`, `daemon`, `ptyproc` never import cobra or `internal/cli` |
| `gochecknoglobals` | frame kinds and limits are `const`; `ErrFrame`, `ErrUnsupported` are error `var`s (allowed: `errname` sentinels as elsewhere) |
| `funlen` 60 / 40, `cyclop` 15, `gocognit` 20 | the agent's frame switch: one method per kind |
| `gosec` G204 (subprocess with a variable) | `ptyproc.Start` takes a resolved path from the agent's own builder: add a `//nolint:gosec // the path and args come from TerminalCommand, built from validated fields` with that reason |
| `forbidigo`, `sloglint`, `tagliatelle` (`snake_case` JSON), `errname`, `godot`, `noctx` | as in constraints.md |
| Build tags | T1's files are `//go:build spike`; `make lint` doesn't build them, so T1 runs `go vet -tags spike` itself |

## Desktop command lists — T12, T13

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `desktop/src-tauri/src/lib.rs` `generate_handler![…]` | **81** commands, in order | T13 appends six after `stats::stats_get`: **87** |
| `desktop/src-tauri/src/online/dispatch.rs` `COMMANDS` | the same names in the same order | `commands_match_generate_handler` (parses `lib.rs`) fails on any difference |
| `dispatch.rs` `dispatch` match | one arm per command | `every_command_is_handled` fails on a missing arm (an arm returning `UnknownCommand`) |
| `dispatch.rs` `args!` with `#[serde(rename_all = "camelCase")]` | camelCase args | new args: `environment`, `workspaceId`; `rename_workspace_rejects_snake_case_args` shows the pattern |
| `dispatch.rs` tests `terminal_open_needs_a_socket`, `terminal_open_streams_to_the_socket` | `TermSpec` JSON | the new optional fields must default (`#[serde(default)]`) so these keep passing |
| `commands.rs` tests `rm_args`, `set_hidden_args`, `new_agent_args`, `runner_stop_args` | exact argv | builders gain an environment parameter; old cases keep their argv for `None`/`local` |
| `model.rs` tests (`to_value(&entries[0])`, line 624) | serialized `Entry` keys | gain `environment`, `environmentOnline`, `snapshotStale` |
| `tray/watcher.rs` tests (`observe`, `wanted`) | events by key | remote keys `env:<name>:<id>` |
| `desktop/test/tauri-mock.js` | sample data for every startup command; logs `tauri-mock: unhandled command` | the six new commands and remote entries (D7) |
| `desktop/test/web-mock.js` | serves `/api` from the Tauri mock's handlers | nothing, once the Tauri mock has the handlers |

## Frontend — T14-T17

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `src/api.ts` `Entry` | every fixture and test building one | `src/test/fixtures.ts` `entry()` gains the three fields with local defaults; tests that spread a full literal `Entry` must too (`npm run typecheck` finds them) |
| `src/api.ts` `ConfigType` | `"string" \| "strings" \| "duration" \| "bool" \| "hosts"` | `environments.renew_before_days` is `"int"` (`internal/config/edit.go:422`): add `"int"` |
| `src/api.ts` `TermSpec`, `NewAgent` | terminal and create callers | optional fields; existing calls unchanged |
| `src/api.ts` `removeEntry`, `renameWorkspace`, `setWorkspaceHidden`, `stopAgent` | positional parameters | the new `environment` parameter is last with a default |
| `src/store.ts` `rootReducer` | 12 slices | gains `environments`; `RootState` changes (tests that build a preloaded state use `Partial`) |
| `settingsSlice.ts` `SettingsGroup` | union of 8 | gains `"environments"` |
| `SettingsList.tsx` `groups` and `SettingsPane.test.tsx:239` | `["Repos", "Reviews", "Forges", "Models", "Agent roles", "Notifications", "Workspaces", "About"]` | the test's list gains `"Environments"` after `"Workspaces"` |
| `agentsSlice.ts` `filtersCleared` and its tests | which filters reset | also clears `environmentFilter` |
| `src/features/agents/tree.ts` `parentOf` | `workspaceId === parentId` | must also compare `environment` |
| `App.tsx` `pollInterval = 4000` (exported) | the listing poll | unchanged; environments poll separately every 10 s |
| `localStorage` keys | existing ones | new key `ws.environment.last`; never store a code |

## `[environments]` settings (ADR 0054's mechanism) — T15

| Key | Type (`ws config list`) | Default | Listed by `ws config list` |
| --- | --- | --- | --- |
| `environments.public_name` | string | `""` (no listener until set) | only when `[environments]` exists in `config.toml` (`internal/config/edit.go:80`) |
| `environments.listen` | string | `0.0.0.0:8883` | same |
| `environments.enroll_listen` | string | `0.0.0.0:8884` | same |
| `environments.renew_before_days` | int | 30 | same |

`ws config set` accepts them whether or not the table exists
(`parseKeyWith` includes `environmentRules()`), so the UI shows defaults
for unlisted keys and saves with `config_set`.

## Docs with lists — T18

| File | What to add |
| --- | --- |
| `docs/adrs/0072`, `0073`, `0074`, `0076` | Amendments (D8) |
| `docs/adrs/0014`, `0016` `Status:` | `ws attach`, `ws stop`; `terminal_limit` |
| `docs/adrs/README.md`, row 0076 | `Proposed` → `Accepted` |
| `docs/tools/README.md`, `README.md` | rows for `ws attach`, `ws stop`; roadmap entry for PR 4 (`README.md:250-257` has PR 2 and 3) |
| `docs/tools/ws-env.md` | Terminals; protocol 2 (the protocol paragraph at line 221) |
| `docs/tools/ws-list.md`, `ws-shell.md`, `ws-rm.md`, `ws-rename.md`, `ws-hide.md`, new `ws-attach.md`, `ws-stop.md` | flags, JSON, exit codes |
| `desktop/README.md` | Environments bullet, Node 22.12+, the D7 screenshots |
| `docs/online.md` | the Environments screenshot and D5's note |

## Not verified by this design

- Any terminal number: that is T1's job. The design's limits (16 KiB, 1 MiB,
  64 KiB/s, 8 terminals, 15 s/60 s) are ADR 0074's or chosen, not measured.
- Whether mochi's in-process `Subscribe` callback runs on the publisher's
  goroutine (T1 checks it; T6 assumes it might).
- That `http.Client` keeps the hijacked 101 body usable over a unix-socket
  transport with `daemon.Client`'s `DialContext` (it does for TCP since Go
  1.12; T7's test proves it on the socket).
- That `claude attach` repaints fully on a one-row resize jiggle (true for
  Ink-based TUIs in general; T11 uses `cat`, so only a real session shows it).
- That the `desktop` cargo feature builds in this pod (no GUI libraries were
  checked); the `online` feature is what tasks can rely on.
- That `ws env agent run` passes the enrolled name to `ws shell` through
  `WS_ENVIRONMENT` without leaking it into a person's own shell on that
  machine (it is set only in the pty's environment).
- `make lint` and `make test` on `feat/env-ui` before T1; the orchestrator's
  baseline verifier covers it.
