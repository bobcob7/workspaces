# Constraints for PR 3, "Operations"

What a task in [tasks-ops.md](tasks-ops.md) breaks if it forgets something,
and what the build pod can't run. Measured 2026-11-22 on `feat/env-ops` at
`f65808d` (PR 2 merged). Every task names the rows here that it touches; it
must update each one in the same commit as the code that changes it. The
PR 2 sheet, [constraints.md](constraints.md), still holds for everything it
lists (lint rules, config keys, the command tree); this sheet adds what PR 3
moves.

## Pinned values in the code

### Error codes (`internal/cli`) — T9

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/cli/errors.go`, `const` block of `errorCode` | every code, with a comment naming its ADR | `environment_offline`, `environment_outdated`, `environment_mismatch` aren't declared, under a `// From ADR 0072 (PR 3).` comment |
| `internal/cli/errors.go`, `errorCode.exitCode()` | a `switch` listing every code under its exit; `exhaustive` fails on a missing one | offline and outdated aren't in the `exitFailed` case, mismatch in `exitRefused` |
| `internal/cli/errors_test.go`, `TestErrorCode_exitCode` | sample codes per exit code | the three new codes aren't added to their subtests |
| `internal/cli/llm_test.go`, `TestGuideTopics` / "the overview lists every error code under its exit code" | every declared code appears in the row `\| <exit> ` of `## Output and exit codes` in `internal/cli/guides/ws.md` | a new code isn't in its row |
| `internal/cli/llm_test.go`, "exit code sections name only real error codes" | every `` `snake_code` `` in a guide's `## Exit codes` is declared | `ws-list.md` (T8) names a code T9 declares; `ws-claude.md` names one before T9's `errors.go` change |
| `unused` linter | a declared code must be used | declare the three codes only in T9, where `callEnvironment` and `remoteParent` use them |
| `docs/adrs/0016-output-contract.md`, `Status:` line | lists each ADR that added codes | add "`environment_offline`, `environment_outdated` and `environment_mismatch` added by [0072](0072-remote-environments.md) (PR 3)" |

### Command flags and guides (`internal/cli`) — T8, T9

No command is added, so `TestApp_newRootCmd` "has every command"
(`internal/cli/app_test.go`) and `guideTopicsBeforeReview()`
(`internal/cli/llm.go`) don't change. New flags:

| Command | Flag | Guide that must name it (`TestGuideTopics`: every local flag appears in its guide) |
| --- | --- | --- |
| `ws list` | `--env` (repeatable) | `internal/cli/guides/ws-list.md` |
| `ws claude` | `--env` | `internal/cli/guides/ws-claude.md` |
| `ws shell` | `--env` | `internal/cli/guides/ws-shell.md` |

`ws shell` is `PreRunE: a.noJSON`: its guide has no `## JSON output`
section and must not get one. `ws list` and `ws claude` guides keep their
```` ```json ```` blocks, updated with the new fields.

### Workspace records (`internal/workspace`) — T2

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/workspace/record.go`, `readRecord` | `checkVersion(ws.Version, parentVersion)`: newest readable version 3 | not raised to `environmentVersion` (4) |
| `internal/workspace/record_test.go:83-91`, "a newer version" and "a newer version with keys this ws doesn't know" | use `version = 4` and expect `this ws reads up to 3` | must become `version = 5` / `reads up to 4`, or they fail once 4 is readable |
| `internal/workspace/record_test.go:121-160`, the `writeRecord` subtests | visible 1, hidden 2, parent 3 | keep passing; add the version 4 case |
| `internal/workspace/create_test.go:113,123`, `rename_test.go:95,157` | `hiddenVersion`, `parentVersion` after create, hide and set-parent | an `environment` that leaks into those fixtures changes their version |
| `docs/state.md:67-71` | "`workspace.toml` is version 1; version 2 when … hidden; version 3 when … parent" | version 4 with `environment` isn't added (T11) |
| `desktop/` | doesn't read `workspace.toml` (checked: no match for `workspace.toml` in `desktop/src-tauri/src`) | nothing |

### Wire payloads (`internal/envproto`) — T1, T3, T5, T6

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/envproto/payloads_test.go`, `TestHello_MarshalJSON` | checks named keys, not the exact object | nothing; add a `protocol` assertion |
| `internal/envproto/payloads_test.go:57`, `Request` decode of `{"op","args"}` | a request without `request_id` and `deadline` decodes | keep: `RequestID` "" and `Deadline` nil |
| `internal/envagent/agent_test.go:335-345`, the snapshot subtest | decodes `envproto.Snapshot` and checks `workspaces` | gains `protocol` 1 and `agents` `[]` (T3), then agents (T5) |
| `internal/environment/controller.go`, `snapshotRecord` (`snapshotVersion = 1`) | the file `state/.environments/<name>/snapshot.json`: `version, id, name, received_at, snapshot` | T6 exports it as `SnapshotFile` with the same JSON; the file's `version` stays 1 (the payload inside gains fields, the wrapper doesn't) |
| `internal/environment/controller_test.go:554` | its own struct with the same JSON tags | nothing, or switch it to `SnapshotFile` |
| `internal/environment/controller.go`, `sendCAUpdate` | `ca.update`'s payload `{op, args}` | gains `"request_id"` and `"deadline":null`; no test pins the exact bytes (checked) |
| `envproto.MaxRequestBytes` = 256 KiB; `internal/mqttbroker/broker.go:95` `MaximumPacketSize = MaxRequestBytes + 16 KiB` | the biggest packet the broker accepts; a bigger one closes the connection with no DISCONNECT (broker spike) | a reply or snapshot over 256 KiB: T3 caps replies, T3 skips oversized snapshots, T5 caps logs at `MaxLogBytes` (192 KiB) |

### `envagent` signatures and their callers — T3, T5

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `envagent.NewAgent(log, files, client, snapshots, claude, random, opts)` | callers: `internal/cli/env_agent.go:131`; `internal/envagent/agent_test.go:132,242,313,333`; `internal/envagent/requests_test.go:135,173`; `internal/mqttbroker/conformance_test.go:132` | T3 adds `ops operationRunner` after `claude`: every caller passes one (`envagent.NoOperations{}` outside envagent's own tests) |
| `envagent` `snapshotSource` | implementations: `agentSnapshots` (`internal/cli/env_agent.go`), `testSnapshots` (`internal/envagent/agent_test.go`), `emptySnapshots` (`internal/mqttbroker/conformance_test.go:110`), and the moq mock | T5 adds `Agents`: each implementation gains it |
| `internal/envagent/interfaces.go` `//go:generate go tool moq -out moq_test.go . mqttClient snapshotSource claudeProbe` | the moq list | T3 appends `operationRunner`; run `go generate ./internal/envagent/` |
| `envagent.DefaultOptions(version, renewBeforeDays)` | returns `Options` | T3's new fields `WorkspacesRoot` (set by the caller) and `MaxOps` (default 4) |
| the session loop (`Agent.serve`) | requests are handled synchronously on the loop today (`handle` → `answer`) | an operation run on the loop blocks snapshots and renewal for up to 10 min: T3 runs operations on goroutines |

### Controller (`internal/environment`) — T6

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `Controller.onRes` | today only logs `ca.update` acknowledgements | T6 must deliver call replies first, by correlation data, and still log ca.update |
| `Controller.answer` (the `up` handler) | refuses any op but `cert.renew` with `usage` "unknown operation" | T6 refuses by `LookupOp(op).Caller != CallerAgent`; the existing test of an unknown op keeps its code `usage` |
| `internal/environment/interfaces.go` `//go:generate … . broker` | the moq list | unchanged |
| `c.mu` is never held across a broker call (comment in `controller.go`) | the broker's handlers take `c.mu` | `Call` must publish and wait without holding `c.mu` |

### Daemon (`internal/daemon`) — T7

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/daemon/interfaces.go` `EnvironmentHost` | implemented by `NopEnvironments` (`environments.go`), `envHost` (`internal/cli/env_daemon.go`), and the moq `EnvironmentHostMock` | `Call` isn't added to each; run `go generate ./internal/daemon/` |
| `//go:generate go tool moq -out moq_test.go . RunnerSupervisor processReader processStarter RunnerRecords EnvironmentHost` | the moq list | unchanged list, regenerated mock |
| `server.routes()` | six routes; `server_test.go` drives them through `serve(...)` | the new route isn't registered |
| `http.Server{ReadHeaderTimeout: shutdownWait}` (no write timeout) | long requests aren't cut | don't add a `WriteTimeout`: a create takes up to 10 min |
| `daemon.Status` JSON and its guide `ws-daemon-status.md` | unchanged in PR 3 | the hello's `protocol` isn't added to `EnvironmentHello` (keep it out; the CLI doesn't need it) |
| `internal/cli/env_daemon_test.go:94-97`, `reloadRecorder`, a hand-written `EnvironmentHost` passed to `serveDaemonWith` | its method set | it doesn't gain `Call` (T7): the cli tests stop compiling |

### `ws list` JSON and text (`internal/cli/list.go`) — T8

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/cli/list_test.go:87-100`, "--json" golden | each entry's exact keys | gains `"environment":"local","environment_status":"online","stale":false` on all three entries, the unknown directory too |
| `internal/cli/list_test.go:105,152,170,229,623` | `{"workspaces":[],"hidden_count":N,"warnings":[...]}` | unchanged (no entries) |
| `internal/cli/list_test.go:429-475`, `TestApp_listWorkspaces`, `listWorkspace` | `listWorkspaceJSON` values | unchanged: the new fields live in `listEntryJSON`, used by `ws list` only |
| `internal/cli/machine_result.go:50`, `machineChildJSON` embeds `listWorkspaceJSON`; `machine.go:413` | `ws machine children`'s JSON (`guides/ws-machine-children.md`, `docs/tools/ws-machine.md`) | adding fields to `listWorkspaceJSON` would change `ws machine children`; don't |
| `internal/cli/env_agent.go:236-249`, `agentSnapshots.Workspaces` | `visibleEntries(..., false, ...)` and `listing.Unknown` | T4: `true` and `nil` (D3) |
| `printList` header `ID\tNAME\tCREATED\tSESSION\tREPOS` | text goldens in `list_test.go` | the `ENV` column appears without a remote entry |
| `desktop/src-tauri/src/commands.rs:64` runs `ws list --all`; `desktop/src-tauri/src/model.rs:14-34` `WsList`/`WsWorkspace` | serde without `deny_unknown_fields` (checked): new keys are ignored | remote entries in the default listing would be shown and acted on as local: D3 keeps the default local-only in PR 3 |
| `desktop/src/test/fixtures.ts`, the Workspace Online dispatcher (`desktop/src-tauri/src/online/dispatch.rs`) | `ws list` fixtures | unaffected (local output only gains keys) |

### `ws claude` JSON — T9

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/cli/claude_test.go:764` | `ws claude --json` golden without `environment` | unchanged: `environment` is only on `--env` results (D9) |
| `internal/cli/machine_test.go:360,406,906` | `ws machine spawn` JSON substrings | unchanged: spawn doesn't use the remote path |
| `workspace.CreateOptions{…}` callers: `claude.go:146`, `runner_start.go:77`, `shell.go:43`, `loop_tick.go:341`, `review_start.go:371` | the options passed | T4 sets `Environment: a.environment` in `claude.go` and `runner_start.go` only; the others leave it empty (a review or loop run on a remote inherits through its parent, D2) |

### `internal/cli/interfaces.go` — T9

`//go:generate go tool moq -out moq_test.go . configLoader repoCache workspaceStore lifetimeManager shellRunner claudeClient ghClient runnerDaemon envBroker envClient`
(`internal/cli/interfaces.go:22`). T9 appends `environmentCaller` and
runs `go generate ./internal/cli/`. Never hand-edit `moq_test.go`.
`claudeClient` already has `Agents`, `Logs`, `Send`, `Stop` (T5 needs no
change to it).

### Lint and test rules

| Rule | Effect on this PR |
| --- | --- |
| New, T0: `internal/blanklines` `TestRepository` in `make test`, and `go test -count=1 -run '^TestRepository$' ./internal/blanklines/` as the last line of `make lint` | a blank line inside any function body outside the 24 exempt files fails both |
| `funlen` 60 lines / 40 statements, `cyclop` 15, `gocognit` 20 | `LookupOp`'s switch and `DecodeArgs` may approach `cyclop`: one `case` per op returning a value, and a helper per argument type |
| `gochecknoglobals` (not in `_test.go`) | the op table is a function with a `switch`, not a map variable; T0's `exempt` map lives in the test file |
| `depguard` `cobra`: only `internal/cli`, `cmd/ws` | `internal/envagent`, `internal/environment`, `internal/envproto` never import cobra or `internal/cli` (cli imports envagent: a cycle) |
| `Makefile` grep: `exec.Command…"claude"` only in `internal/claude/` | `agent.*` call `claudeClient` methods; nothing in `envagent` runs `claude` |
| `Makefile` grep: `envtest` only from `_test.go` | T10's conformance test may import it; non-test code may not |
| `sloglint`, `forbidigo`, `tagliatelle`, `noctx`, `errname`, `godot` | as in [constraints.md](constraints.md) › Lint rules; `OutdatedError`, `OfflineError`, `HostError`, `ArgsError` follow `errname` (`…Error` types) |
| `gosec` G304 | the snapshot reader opens a path built from the store's directory and a validated name |
| Owner rules not enforced by a linter (besides T0's) | interfaces at the consumer, one `interfaces.go` and one `go:generate` line per package; mocks only in `moq_test.go`; unexported by default; `t.Context()`; `require` for preconditions, `assert` for verifications |

### Docs with lists — T9, T11

| File | What to add |
| --- | --- |
| `docs/adrs/README.md:242`, row 0072 | status `Proposed` → `Accepted` |
| `docs/adrs/0072-remote-environments.md` `Status:` | `Accepted (<date>)`, and an Amendments section with D2-D5, D7, D9 |
| `docs/adrs/0016-output-contract.md` `Status:` | the three codes (T9) |
| `docs/adrs/0012`, `0014`, `0044` | "Amended by [0072](0072-remote-environments.md)" in each `Status:` line, as ADR 0072's Consequences list |
| `docs/state.md` `## Layout` row of `workspace.toml` and the version paragraph (lines 63-71) | `environment`, version 4; the snapshot file's new payload fields |
| `docs/tools/ws-list.md`, `ws-claude.md`, `ws-shell.md`, `ws-env.md` | as in T11 |
| `docs/tools/README.md` command table | unchanged (no new command) |
| `README.md` `## Roadmap` | an entry for PR 3 (ADR 0076) |
| `internal/cli/guides/ws.md` | the error rows (T9) and the agents' paragraph (T11) |

## What the pod can and can't run

Measured in this pod on 2026-11-22: `go version` = `go1.27.3 linux/amd64`
(`go.mod`: `go 1.27.0`); `make` present; `golangci-lint` only through
`go tool golangci-lint` (`make lint`); loopback TCP works; the module proxy
is reachable but no task needs it (no new dependency). The PR 2 sheet's
measurements (Node `v20.19.2`, a remote Docker daemon, 128 GB, 48 CPUs on
that pod) were not re-measured; children may land on a smaller pod.

| Can't | Consequence for tasks |
| --- | --- |
| A real remote machine, or inbound connections from one | every operation test runs controller, broker (`envtest`) and agent in one process (T10); the real broker on loopback is PR 2's `internal/mqttbroker` conformance test, which T3 and T5 only keep compiling |
| A real `claude` signed in | `claude` is always `claudeClientMock`; nothing starts a real session; `agent.send`'s delivery check (`claude.Send` over a pty) is covered only by `internal/claude`'s own tests |
| Network git clones | conformance creates empty workspaces (no specs); spec handling is unit-tested with the stub cache (`stubCache` in `app_test.go`) |
| Windows | no `GOOS=windows` build; no `_windows.go` files; paths in tests are Unix paths |
| macOS | CI's macOS legs check darwin; the new code uses no OS-specific calls |
| Node 22.12+ (the PR 2 pod had 20.19.2) | the desktop can't be tested here; this PR doesn't change `desktop/` |
| `make e2e-runner` cheaply | not needed: PR 3 doesn't touch the runner's scenarios, but `e2e/runner/*_test.go` are scanned by T0 (five of them are exempt) |
| `make test` cheaply | run `go test -p 2` on the task's packages; `internal/cli` alone takes minutes; `make lint` once per task, last |

## Not verified by this design

- That every test in `internal/cli` passes with `App.environment` and the
  `--env` flags (no code was run; the tasks say which goldens change).
- That `ws claude`, run in-process by the agent while the agent's own
  `signal.NotifyContext` is active, behaves the same as from a terminal
  (the `SIGHUP` catch in `handOff` is per-channel and should not interfere;
  T10 runs it in-process but without signals).
- `make lint` and `make test` on `feat/env-ops` before T0; the
  orchestrator's baseline verifier covers it. T0's exempt list was measured
  with a scratch scanner using T0's rules, not with T0's code.
- The real broker's behavior when an operation's reply arrives after the
  controller gave up (it's dropped by `onRes`; T6 tests it with `envtest`
  only).
- How long `workspace.create` takes on a real remote for a large repo; the
  10-minute timeout is ADR 0074's and unmeasured.
