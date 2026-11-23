# Constraints for PR 4, part RL, "Reviews and loops on remote environments"

What a task in [tasks-rl.md](tasks-rl.md) breaks if it forgets something.
These values were measured on 2026-11-23 on `feat/env-ui` at `a1c215b`.

This sheet lists only what [constraints-ui.md](constraints-ui.md) doesn't.
Everything there still holds: toolchains, the desktop commands, what the pod
can't run, and the lint and test rules. Every task names the rows here that
it touches, and must update each one in the same commit as the code that
changes it.

The RL tasks build on T-tasks of tasks-ui.md. Line numbers below are from
`a1c215b`; a T-task may have moved them.

## Wire payloads (`internal/envproto`) — RL1, RL2

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `payloads.go`, `const Protocol` | 1 today; T2 makes it 2 | RL1 makes it 3. Every `"protocol":2` literal T2 wrote in `payloads_test.go` becomes `Protocol`. Fixtures that describe an older agent (`env_daemon_test.go`, `daemon/server_test.go`, `environment/snapshot_test.go`, `list_env_test.go`) keep their number |
| `ProtocolReviewsLoops` | new, equal to `Protocol` at merge | the orchestrator renumbers it if the terminals' number changes (R6) |
| `LookupOp` | the allow-list, one `case` per op | 7 `review.*` and 16 `loop.*` rows; `ops_test.go` covers each |
| `newArgs`, `Validate` | one `case` per args type | a new type that `Validate` doesn't know is `usage` "unknown operation arguments": add every type |
| `DecodeArgs` | calls `Validate` | RL2 adds `validateOp(op, args)` after it (confirm, `[redacted]`) |
| `Snapshot` | `{at, protocol, workspaces, agents}` | gains `loops`, `omitempty` on the struct so a protocol-1/2 snapshot still decodes. Agents always set `[]` |
| `MaxRequestBytes` 256 KiB | every message | paged results stay ≤ `MaxPageBytes` 192 KiB, and `finishResult` refuses over 252 KiB |
| `envproto` imports | standard library only | `internal/redact` must not be imported here. Redaction is the agent's job (RL6) |
| `CodeOperationLimited` | new string const (RL5) | must equal `internal/cli`'s `codeOperationLimited` (RL8) |
| `ErrNeedsConfirm` | new sentinel (RL7) | `envagent.refusalReply` maps it to `details {"needs":"confirm"}` |

## `envagent` — RL5, RL7

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `interfaces.go` moq line | `mqttClient snapshotSource claudeProbe operationRunner`, plus T4's additions | RL5 appends `loopTicker`; `snapshotSource` gains `Loops`. Run `go generate ./internal/envagent/` |
| `snapshotSource` implementations | `agentSnapshots` (`internal/cli/env_agent.go`), `snapshotSourceMock`, and any hand fakes in `internal/mqttbroker/conformance_test.go` | each gains `Loops`, or it stops compiling |
| `remembered(op)` | read-only ops answer `false` (`agent.logs`, `agent.status`, `workspace.list`) | `review.status`, `review.list`, `review.report`, `review.profiles`, `loop.list`, `loop.show`, `loop.history`, `loop.history_run`, `loop.memory_list` and `loop.memory_get` must be `false`. Their replies can hold secrets and should be fresh |
| `workspaceRefs(args)` | `CheckWorkspaceRef` on workspace arguments | review ids and loop names are **not** workspace paths. Don't add them here; their checks are typed (RL1, RL2) |
| `NewAgent` signature | unchanged since PR 3 | RL5 adds `SetLoopTicker`, as T1c adds `SetStreamClient` |
| `Options` | `DefaultOptions` | gains `LoopTick` 60 s. Existing tests that build `Options` by hand get 0, meaning no ticking |
| the session loop | never blocks | the ticker and passes run on their own goroutines |

## Agent operations (`internal/cli`) — RL6, RL7

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `agentOperations.Run` | `agent.` prefix goes to `runAgent`, else `runWorkspace`; T5 adds terminals | `review.` and `loop.` prefixes must be matched before the `runWorkspace` fallthrough |
| `newApp` | stdin is always empty | `loop.memory_set` needs stdin: add `newAppWithStdin`, and keep `newApp` for the rest |
| `cliPrefix` | `--json --config-dir <root>` | every review and loop argv starts with it |
| `review start` | finds a requester from the cwd | the agent always passes `--requester none` |
| `review report --repo --ref` | an alternative lookup | never built from wire arguments |
| `commandFailure`, `stderrError` | error mapping | unchanged; review and loop codes pass through |

## Error codes (`internal/cli`) — RL8

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `errors.go` `const` block | codes under ADR comments | `codeOperationLimited` goes under `// From ADR 0078.` |
| `exitCode()` (`exhaustive`) | | `operation_limited` must be in the exit-1 case |
| `known()` | | must be listed, or a remote `operation_limited` prints as `internal` |
| `errors_test.go` `TestErrorCode_exitCode` | | exit 1 sample |
| `guides/ws.md`, the `\| 1 ` exit-code row | `TestGuideTopics` "the overview lists every error code under its exit code" | add `operation_limited` |
| `ws-review-start.md` `## Exit codes` | `TestGuideTopics` "exit code sections name only real error codes" | name it only in the commit that declares it |
| `unused` linter | | declare it in RL8, where `ws review start --env` maps it |

## Commands and guides (`internal/cli`) — RL4, RL8, RL9

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `TestGuideTopics` "every local flag appears in its guide" | each flag in its guide | `--env` missing from `ws-review-{start,wait,report,list,decide,acted,profiles}.md` or any `ws-loop-*.md` except `ws-loop-tick.md`. `--cached` must be in `ws-loop-list.md`; `--yes` in `ws-loop-rm.md`, `ws-loop-history.md`, `ws-loop-memory-clear.md`, `ws-loop-add.md`, `ws-loop-edit.md`; `--max-starts` in `ws-loop-tick.md` |
| `TestApp_newRootCmd` "has every command" | the command count | unchanged: no new command in this part |
| `ws llm` for agents | `ws review` is an agent command (ADR 0039) | `--env` on `ws review` must say "for people and the desktop app; refused inside a workspace" (R5), so an agent doesn't try it |
| `ws loop tick --watch` with `--json` | `usage` | `--max-starts` works in all modes |

## Config key — RL7

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/config/config.go` `Environments` | four fields | gains `AgentLoopScripts *bool` (`agent_loop_scripts`) |
| `internal/config/edit.go:417` `environmentRules()` | four keys | gains `environments.agent_loop_scripts`, `TypeBool` |
| `ws config list` | lists `[environments]` keys only when the table exists (`edit.go:80`) | unchanged behaviour |
| constraints-ui.md's `[environments]` table, T15's settings card | four keys on the **controller** | the new key is set on the **remote**, so T15 doesn't show it. ws-env.md documents it (RL16) |

## Desktop — RL11, RL12

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `lib.rs` `generate_handler!`, `dispatch.rs` `COMMANDS` | 87 after T13 | **unchanged**: this part adds arguments, not commands |
| `dispatch.rs` `args!` (`camelCase`, `#[serde(default)]`) | old frontends' calls decode | new arguments `environment`, `reviewId`, `confirmed` are optional with defaults |
| `reviews/store.rs` `RepoWatch` | `{watch, autoReview}` in `desktop.json` | `environment` is `skip_serializing_if = "Option::is_none"`, so an old file round-trips byte for byte |
| `reviews/plan.rs` `start_args` tests | exact argv | local cases keep their argv |
| `reviews/report.rs` `Cache` key | `repo@ref` | remote keys are `env:<name>:<repo>@<ref>` |
| `reviews/runs.rs` | T12: local entries only | RL11 joins remote runs only on their own environment; T12's `remote_review_workspace_is_not_joined` is rewritten as `remote_runs_join_only_their_environment` |
| `loops.rs` `*_args` tests | exact argv | local cases keep their argv; `tick_forever` stays local |
| `desktop/test/tauri-mock.js` | sample data | remote review runs, remote loops (with one script), and `ws review profiles --env` data |

## Frontend — RL13-RL15

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `src/api.ts` `Loop`, `ReviewRun` | fixtures and tests | `fixtures.ts` gains `environment: "local"`, `stale: false`, `summary: false` |
| `api.ts` wrappers' positional parameters | callers | new parameters go last, with defaults |
| `loopsSlice` state shape | tests, `LoopsList` | becomes `byEnvironment`. Every test that preloads `loops` changes |
| `reportsSlice` `reportKey` | `"${repo}@${ref}"` | remote keys get the `env:` prefix; local keys are unchanged |
| `localStorage` | `ws.environment.last` (T16) | New loop reuses it; no new key |

## Docs with lists — RL16

| File | What to add |
| --- | --- |
| `docs/adrs/README.md` | row 0078 |
| `docs/adrs/0073`, `0072`, `0076`, `0016`, `0048`, `0060` | R7's amendments |
| `docs/tools/ws-env.md` | Reviews and Loops on an environment; protocol 3 |
| `docs/tools/ws-review.md`, `ws-loop.md` | `--env`, `--cached`, `--yes`, `--max-starts` |
| `desktop/README.md`, `docs/online.md` | the seven RL16 screenshots |

## Not verified by this design

- That `internal/review/render` can render from `reviewReportJSON`, as
  opposed to store types. RL8 checks this first and adds a conversion if
  needed.
- That `gh` on a remote reads `GH_TOKEN` from the service environment the
  same way it does locally. `internal/gh` passes `os.Environ()`; the
  service files are the user's.
- That `secrets.env` values reach `claude` or `gh` child processes on the
  agent. R1 tells the user to use `gh auth login` or the service
  environment, not `secrets.env`, for `GH_TOKEN`.
- How large a real review report is. Only design documents of 43 and 103 KB
  were measured. Paging handles any size.
- The permission-mode set. RL1 copies it from `internal/loop`'s check; the
  design doesn't list the values.
- Whether `ws loop tick`'s settle step is cheap enough every 60 s on a
  remote with many runs. It is the same as the desktop's tick today.
- The redaction patterns' false positives on review text, for example a
  40-character example token in a finding. They are replaced, which is the
  safe side.
- Anything on a real remote machine or a real WAN, as in constraints-ui.md.
