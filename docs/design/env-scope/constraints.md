# Constraints for `env-scope`: environment scopes for children

What this pod can and can't check, how long each check takes, the values
tests pin, and the repo rules every task follows. Measured 2026-11-24 on
`main` at `0763608`, Go 1.27.3 linux/amd64, in a ws-online pod (8 GB limit;
`free -m` showed the host's 128 GB, don't trust it). The decisions are in
[ADR 0082](../../adrs/0082-environment-scopes-for-children.md), the formats
in [design.md](design.md), the order in [tasks.md](tasks.md).

## 1. Environment proof (run on `main` before any change)

| Command | Result | Wall time |
| --- | --- | --- |
| `go build ./...` | ok | 28 s (cold module cache warm-ish) |
| `go test -p 2 -count=1 ./internal/envproto ./internal/environment/... ./internal/envagent ./internal/machine/... ./internal/workspace ./internal/config` | all `ok` (environment 15 s, envagent 3 s) | 29 s |
| `go test -p 2 -count=1 ./internal/cli ./internal/daemon` | `ok` (cli 47 s, daemon 2 s) | 68 s |
| `make lint` (golangci-lint on `./...`, on `e2e` with `-tags e2e`, the grep rules, `TestRepository` blank lines) | `0 issues.` twice, blanklines `ok` | 60 s |
| `test -z "$(go tool golangci-lint fmt --diff ./...)"` | clean (gofumpt isn't on `PATH`; use this) | 32 s |
| `GOOS=windows go build ./... && GOOS=darwin go build ./...` | ok | 62 s |
| `go tool -n moq` | present (mocks: `go generate ./internal/<pkg>`) | — |

`/usr/bin/time` doesn't exist in the pod; use bash's `time`.

The designer re-ran `go build ./...` after committing the docs (see the
commit); docs-only, so nothing else changed.

## 2. What can't run here

| Thing | Why | What tasks do instead |
| --- | --- | --- |
| A real remote environment (second machine, real `ws env agent run` over the network) | one pod, no second host | controller + agent tests over `internal/environment/envtest` (in-memory broker, real TLS handshake); CLI tests with `daemon.EnvironmentHostMock` / `serveDaemonWith` and the conformance harness in `internal/cli/env_ops_conformance_test.go` |
| The motivating demo (agent on A spawns on B, compare private IPs) | needs two enrolled machines; pods have no `ping` | out of scope; list it under "Not run here" in the PR test plan |
| Windows tests | no Windows host; CI `windows-latest` runs `make test-windows` | `GOOS=windows go build ./...` and `GOOS=windows go vet ./internal/daemon ./internal/envagent` for files you touch |
| Docker / testcontainers | remote daemon, no published ports | not needed: nothing in this feature uses them |
| The desktop app (`make desktop-test`, Node) | not touched by this feature | don't change `desktop/`; if a task thinks it must, stop and report |

Heavy suites: run **one at a time**, `-p 2`. Never run `go test ./...`
without `-p 2` in this pod. `internal/cli` alone takes ~50 s; run it with
`-run` for your tests while iterating, and once in full at the end.

## 3. Pinned values a task can break

| Pin | Where | What to do |
| --- | --- | --- |
| `envproto.Protocol = 3` | `internal/envproto/payloads.go:9` | T2 sets it to **4** and adds `ProtocolScopes = 4`. Tests that use the constant follow it. One literal: `internal/envproto/payloads_test.go:74` decodes `{"protocol":3}` — leave it (it tests decoding, not the current value) |
| `LookupOp` table tests | `internal/envproto/ops_test.go` (`TestLookupOp`, every op's caller/timeout/since) | add rows for the 5 new ops |
| Agent refuses non-controller ops | `internal/envagent/ops.go` `admit` | the `scope.*` ops are `CallerAgent`, so the agent already refuses them from the controller; add a test row |
| `remembered()` list | `internal/envagent/ops.go:74` | `machine.record` is read-only: add it to the not-remembered list (reports can be secret, like `agent.logs`) |
| Controller `answer` only knows `cert.renew` | `internal/environment/controller.go:578` | T3 switches on the op; keep `cert.renew`'s exact replies (tests pin `enrollment_failed`) |
| Error codes: overview row per exit code | `internal/cli/llm_test.go` "the overview lists every error code under its exit code" | `environment_out_of_scope` goes in `guides/ws.md`'s exit-4 row in the **same task** that declares the constant (T5) |
| Error codes: `exitCode()` and `known()` | `internal/cli/errors.go:161`, `:191` | add the new code to both (exitRefused) |
| "exit code sections name only real error codes" | `llm_test.go` | a guide may only name the new code once the constant exists (T5) |
| Every command has a guide; every guide a command; topics listed once | `llm_test.go` `TestGuideTopics` | `ws machine envs` needs `guides/ws-machine-envs.md` **and** `"machine envs"` in `guideTopicsFromReview()` (after `"machine children"`) in the same task (T5) |
| Every flag is in its command's guide | `llm_test.go` | `--env`, `--env-scope` on `machine spawn` and `--env-scope` on `claude` must appear in `ws-machine-spawn.md` / `ws-claude.md` in the task that adds the flag (T5) |
| Commands with a result document their JSON | `llm_test.go` | `ws-machine-envs.md` needs `## JSON output` with a ```` ```json ```` block, and must mention `--json` ("`--json` is accepted and changes nothing") |
| The overview points to every guide | `llm_test.go` | add `` `ws llm machine envs` `` to `guides/ws.md` (T5) |
| "the facts agents get wrong" | `llm_test.go:201` | keep `ws machine result PASS --file -` and `ws machine progress` in `ws-machine-spawn.md` |
| Exact JSON of spawn / claude / list | `JSONEq` in `machine_test.go` (6), `claude_test.go` (2), `list_test.go` (12), `machine_result_test.go` (2) | **local output must not change**: new fields only on cross-environment results. Wait items gain `"environment"` (no `JSONEq` in `machine_wait_test.go`) |
| Config keys | `internal/config/edit.go` `environmentRules()`; `config_test.go`/`edit_test.go` list keys | add `environments.default_scope` (`TypeStrings`) last in `environmentRules()`; update any test listing environment keys; `docs/config.md` `[environments]` table row |
| moq mocks | `//go:generate` in `internal/{cli,daemon,envagent,environment}/interfaces.go` | after changing an interface listed there, run `go generate ./internal/<pkg>` and commit `moq_test.go` |
| `workspace.toml` version (4) | `internal/workspace/record.go` | **don't touch**: scopes live in `scope.json`, not the record |
| ADR index | `docs/adrs/README.md` | row for 0082 already added (Proposed) |

## 4. Repo rules (Makefile lint and the owner's rules)

- **No blank lines inside function bodies** (`go test -count=1 -run '^TestRepository$' ./internal/blanklines/`).
- Grep rules in `make lint`: `git` only via `internal/git`; `claude` only via
  `internal/claude`; `gh` only via `internal/gh`; `/bin/sh` only via
  `internal/gate`; no `syscall.Exec` outside `internal/claude`;
  `internal/environment/envtest` imported **only from `_test.go`**; the
  OpenTelemetry SDK only in `internal/telemetry`; `"runner", "run"` only in
  `internal/daemon` and `internal/cli`.
- Go standards (handbook): one test file per source file,
  `Test<Receiver>_<Method>` with `t.Run` per behaviour, testify
  (`require` for preconditions, `assert` for checks), `t.Context()`,
  `t.Parallel()` only for tests that block on I/O, moq mocks only from
  `interfaces.go`, early returns, small functions, errors wrapped with
  `fmt.Errorf("doing x: %w", err)`.
- Commits: imperative, concise, **no `Co-Authored-By` trailer** (repo rule;
  overrides the implementer role's prompt). Commit and push after each file
  group and before the slowest suite.
- Never wait on a permission prompt: list refused or unrunnable commands
  under `## Not run here` in the report. Never wait with a loop on
  `pgrep -f <pattern>`.
- New files' package docs name the ADR: `// Package envscope ... (ADR 0082).`
- JSON fields are `snake_case`, times RFC 3339 UTC, a field with no value
  is `null`, never missing (`ws llm` "Output and exit codes").

## 5. Security tests are written failing first

Each task that implements a security property writes the test **before**
the code, runs it, and records the failure in its report (test name + the
failing assertion), then makes it pass. The properties and their tasks:

| Property (design §5) | Task | Test name (required) |
| --- | --- | --- |
| Narrow never widens; missing/invalid file = self (#1, #6, #7) | T1 | `TestScope_Narrow`, `TestRead` |
| Forged up claim bounded by the ledger ceiling (#8, #9) | T3 | `TestController_Scope/spawn refuses a claim wider than the ledger ceiling`, `.../ignores ledger entries on other environments` |
| A can't act on a child whose ledger parent isn't on A (#10) | T3 | `TestController_Scope/child refuses a ledger parent on another environment` |
| A child can't widen by flag, config edit, env var, or `ws claude` inside a workspace (#1-#5) | T5 | `TestApp_runMachineSpawn/scope ...` subtests and `TestApp_runClaude/refuses --env-scope inside a workspace` |
| Default-local unchanged | T5 | `TestApp_runMachineSpawn/scope without a grant spawns locally and writes no scope.json` |
