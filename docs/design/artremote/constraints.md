# artremote: environment constraints

Every task in [tasks.md](tasks.md) links this page. It says what the pod
that runs the implementers can and can't do, the repo rules the build
enforces, and the pinned values the tasks touch. Measured on 2026-11-27 in
the ws-online pod at `8adb0d4` (main): linux/amd64 (x86_64), 48 CPUs,
128 GiB, Go 1.27.3, Node 20.19.2, installed `ws` v0.6.0
(`/usr/local/bin/ws`, built 2026-11-27 16:22).

## Proven here

| Command | Result |
| ------- | ------ |
| `go build ./...` | exit 0, 9 s |
| `go test -p 2 -count=1 ./internal/envproto/ ./internal/environment/ ./internal/envagent/ ./internal/artifact/ ./internal/config/ ./internal/daemon/` | all `ok`, 22 s wall (`environment` 15.8 s, `artifact` 9.9 s) |
| `go test -p 2 -count=1 ./internal/cli/` | `ok`, 36 s |
| `go tool golangci-lint run ./internal/envproto/... ./internal/environment/... ./internal/envagent/... ./internal/artifact/...` | `0 issues.` |
| `go test -count=1 -run '^TestRepository$' ./internal/blanklines/` | `ok` |
| A loopback mTLS probe (a throwaway Go module in `$CLAUDE_JOB_DIR`, not in the repo): `httptest.NewUnstartedServer` with TLS 1.3 and `RequireAndVerifyClientCert` | a 10 MiB `POST` with a valid client certificate: 200 in 15-20 ms. Another CA's certificate: `remote error: tls: certificate required` (Go's client sends no certificate whose issuer isn't in the server's list). Expired: `remote error: tls: expired certificate`. None: `certificate required`. Each is a `*net.OpError` with `Op == "remote error"`, not a `tls.AlertError`, and surfaces on the first request, not at dial. A closed server: `*net.OpError` with `Op == "dial"`, `connection refused` |

So **everything this feature needs runs here**: the controller's listener,
its handlers, the agent's client and the end-to-end test are all
in-process Go over loopback (`127.0.0.1:0`).

## What the pod can't do

- **No real remote environment.** There is no second machine or enrolled
  agent. Remote behaviour is tested only in-process: the environment
  package's tests, `internal/environment/envtest` (an in-process MQTT
  broker), and `internal/cli`'s helpers `startEnvController`,
  `enrollAgentTestMachine` (`internal/cli/env_agent_test.go`) and
  `enrollTestEnvironment` (`internal/cli/env_test.go`), which run a real
  controller with real certificates on loopback.
- **No vega cluster change.** `kubectl` is read-only and vega-infra PRs go
  through Forgejo by a person. The deployment follow-up (port 8885 on the
  LoadBalancer Service and the NetworkPolicy) is written down in ADR 0087
  and the report, not done. No task touches `deploy/` or vega-infra.
- **No desktop tests.** `make desktop-test` needs Node 22.12+ or 24.12+; the
  pod has 20.19.2. **No task touches `desktop/`**: the desktop reads
  artifacts through `ws artifact` on the controller, which doesn't change.
- **The installed `ws` is older than the branch.** Anything a task adds is
  exercised only by `go test` or a fresh `./bin/ws` (`make build`). Never
  run `ws` other than `ws machine ...` (workspace rule), and never
  `./bin/ws` against the real `WS_CONFIG_DIR`: use
  `WS_CONFIG_DIR="$(mktemp -d)" ./bin/ws ...`.
- **Tests must never start a real Claude Code session** (the session has no
  sign-in). Use the `claudeClient` fake in `internal/cli`.
- **Ports:** tests bind `127.0.0.1:0` only; never a fixed port, never
  `0.0.0.0`.

## Repo rules the build enforces

- **No blank lines inside function bodies**:
  `go test -count=1 -run '^TestRepository$' ./internal/blanklines/`.
- **`go tool golangci-lint run ./...`** (`.golangci.yml`: `contextcheck`,
  `errorlint`, `exhaustive`, `gosec`, `nilnil`, `noctx`,
  `forcetypeassert`, ...; `bodyclose` is not enabled, close bodies anyway). HTTP code: every `http.NewRequestWithContext`,
  every response body closed, `http.Server` with timeouts (`gosec` G112).
- **Imports:** `internal/environment/envtest` only from `_test.go`;
  `claude` only from `internal/claude/`, `git` only from `internal/git/`.
  `internal/environment` must not import `internal/cli`; the controller's
  handlers are injected (`SetArtifactAPI`), like `SetLocalScopes`.
- **Go testing standard** (Agent Handbook, Go: Testing): one test file per
  source file, `Test<Receiver>_<Method>` names, `t.Run` per behaviour,
  testify (`require` for preconditions, `assert` for checks),
  `t.Context()`, `t.Parallel()` only for tests that do I/O; moq mocks only
  from `interfaces.go` (`go generate` in that package).
- **Failing-first:** a test that shows the bug (here: the end-to-end test in
  T4, which gets `artifact_unreachable` on `main`) is committed first
  (`test: ...`), run, and recorded as failing; then the fix.
- **Commits:** imperative, conventional prefix, **no `Co-Authored-By` or any
  AI attribution** (user rule; it overrides the implementer role's default).
- **ADRs:** this feature's is [0087](../../adrs/0087-artifacts-from-remote-environments.md),
  already written; tasks don't add ADRs.

## Pinned values the tasks touch

| Value | Where it's pinned | Task |
| ----- | ----------------- | ---- |
| `envproto.Protocol = 4` | `internal/envproto/payloads.go:9`; `TestProtocol_scopes` asserts `4, Protocol` (`internal/envproto/scope_test.go:33`) | T1 makes it 5 and changes that assertion to `5, Protocol` and `4, ProtocolScopes`; adds `TestProtocol_artifacts` (`5, ProtocolArtifacts`). Tests that use `envproto.Protocol` symbolically (`envagent/agent_test.go:352`, `requests_test.go:177`, `environment/call_test.go`) need no change |
| The operation table | `TestLookupOp` (`internal/envproto/ops_test.go:13`) lists each op's `OpSpec` | T1 adds `OpAPIUpdate: {Name, Caller: CallerInternal, Since: 5, Timeout: 30 s}` |
| `envagent` `remembered()` and `admit()` | `internal/envagent/ops.go` | T3 handles `api.update` before `admit`, beside `ca.update`, so neither list changes |
| `[environments]` keys in `ws config` | `TestEnvironmentKeys` (`internal/config/edit_test.go:540`): `require.Len(t, settings, 11)` and indexes 5-10 | T1 appends `environments.api_listen` **last** in `environmentRules()`, so indexes 5-10 stay and the length becomes 12, with `settings[11]` = `api_listen`, default `"0.0.0.0:8885"` |
| `runner.roles.implementer.runner` is `keys[len(keys)-9]` | `internal/config/runner_test.go:511` | unaffected: its config has no `[environments]` table (check it still passes) |
| Daemon status | `internal/daemon/server_test.go:273` and `internal/cli/daemon_test.go:218` build `EnvironmentsStatus` with `Listen`, `EnrollListen`; `cli/daemon.go:203` prints `listening on %s, enroll on %s` | T1 adds `APIListen` (`json:"api_listen"`) and the text `, api on %s`; update those two tests' expectations |
| `EnrollResponse`, enrolment code `ws-enroll-v1`, `agent.toml` `version = 1` | `internal/envproto/payloads.go`, `envproto/code.go`, `envagent/files.go` | **unchanged**: discovery is `api.update` into a new `api.toml` |
| `artifact.json` `version = 1`, `Actor` JSON | `internal/artifact/*_test.go` and `internal/cli/artifact*_test.go` pin actors as `{"kind","workspace","name"}` | T2 adds `Environment *string json:"environment,omitempty"`: omitted for every existing actor, so those pins hold |
| Error codes | `internal/cli/errors.go`, `errors_test.go`, ADR 0016's status line | **no new code**; `artifact_unreachable` gains `details.reason` |
| `artifactHints` behaviour | `TestApp_artifactHints` (`internal/cli/claude_test.go:97`): "on a remote environment the prompt is unchanged, with a warning" and "an enrolled agent passes references with a warning"; `TestApp_runMachineSpawn_artifactHints` (`machine_test.go:305`) | T4 rewrites the two remote subtests to the new behaviour (checked, hint appended); the others stay |
| `artifact_unreachable` on an agent | `TestApp_machineArtifactOutside` (`internal/cli/machine_artifact_test.go:186`) writes `agent.toml` = `"x"` and expects `artifact_unreachable` for every command | still true after T4 (an unreadable identity is `artifact_unreachable`, reason `identity`); T4 keeps that subtest and renames it |
| Size limits | `artifact.MaxImageSize = 10 << 20`, `MaxTextSize = 1 << 20`, `MaxStoreBytes = 2 << 30` (`internal/artifact/artifact.go:24-28`) | used as they are; no new limit constant except the handler's in-flight cap (4) |
