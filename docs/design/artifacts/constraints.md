# Constraints for artifacts

What a task in [tasks.md](tasks.md) breaks if it forgets something, and
what the build pod can and can't run. Measured 2026-11-23 on `main` at
`320b683` in the ws-online pod. Every task names the rows here that it
touches (by their **id**, `C1`…); it must update each one in the same
commit as the code that changes it.

## Pinned values in the Go code

### Error codes (`internal/cli`) — C1

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/cli/errors.go`, the `const` block of `errorCode` (70 codes today) | every code, grouped with a comment naming its ADR | a new code isn't declared here |
| `errors.go`, `errorCode.exitCode()` | a `switch` listing every code under its exit; the `exhaustive` linter and `TestErrorCode_known` / "every code in exitCode is known" (which parses `errors.go`) | a new code isn't in its `case` |
| `errors.go`, `errorCode.known()` | a second `switch` listing every code | a new code isn't added: `TestErrorCode_known` fails |
| `internal/cli/errors_test.go`, `TestErrorCode_exitCode` | sample codes per exit | add the new codes to their subtests |
| `internal/cli/llm_test.go`, `TestGuideTopics` / "the overview lists every error code under its exit code" | each declared code must be in the row `\| <exit> ` of the table under `## Output and exit codes` in `internal/cli/guides/ws.md` | a new code isn't in that row |
| `llm_test.go`, "exit code sections name only real error codes" | every `` `snake_code` `` in a guide's `## Exit codes` must be declared | a guide names a code before its constant exists; declare codes in the same task as the first guide that names them |
| `internal/cli/errors.go`: unused constants | `unused` linter | a code declared in one task and used only in a later one; declare each code in the task that first returns it |
| `docs/adrs/0016-output-contract.md`, the `Status:` line | lists each ADR that added codes | append ``; `artifact_invalid`, `artifact_unknown`, `artifact_conflict`, `artifact_full`, `artifact_forbidden` and `artifact_unreachable` added by [0078](0078-artifacts.md) (T3), `anchor_not_found` by [0079](0079-artifact-comments-and-anchors.md) (T4)`` |

Which task declares which code: **T3** declares `artifact_invalid` (2),
`artifact_unknown` (3), `artifact_conflict` (4), `artifact_full` (4),
`artifact_forbidden` (4), `artifact_unreachable` (1). **T4** declares
`anchor_not_found` (3). Exit constants: `exitFailed` 1, `exitUsage` 2,
`exitNotFound` 3, `exitRefused` 4.

### Command tree and `ws llm` guides (`internal/cli`) — C2

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/cli/app.go`, `newRootCmd` | the slice of people commands given `peopleGroup` | `a.newArtifactCmd()` isn't added to it (after `a.newEnvCmd()`) |
| `internal/cli/app_test.go:342`, `TestApp_newRootCmd` / "has every command" | `assert.ElementsMatch` over exactly `claude, shell, restore, restart, continue, loop, list, rm, rename, hide, unhide, cache, config, daemon, env, review, llm, machine, runner` (hidden commands included) | `artifact` isn't added |
| `app_test.go`, "keeps runner hidden from help" | pattern to copy | add a subtest "keeps artifact hidden from help" |
| `internal/cli/machine.go`, `newMachineCmd` | `cmd.AddCommand(…)` list | `a.newMachineArtifactCmd()` isn't added (last) |
| `internal/cli/llm.go`, `guideTopicsFromReview()` | ordered topics; `TestGuideTopics` requires a guide per topic and a topic per guide, no duplicates (`assert.Len(slices.Compact(topics), len(guideTopics()))`) | the 11 `machine artifact…` topics aren't inserted after `"machine progress show"` and before `"llm"`, in the order of the guides list in README.md |
| `llm_test.go`, `TestGuideTopics` | every **non-hidden** command has `guides/ws-<path>.md` (the walk skips `Hidden` subtrees, `help`, `completion`); every local flag appears in its guide; every guide starts `# ws <path>\n`, has the synopsis line `\nws <path>`, a non-empty `## Exit codes` and `## Examples`; a command that prints JSON has `## JSON output` with a ```` ```json ```` block; `guides/ws.md` contains `` `ws llm <path>` `` for every topic | any is missing |
| `internal/cli/guides/*.md` | embedded with `//go:embed guides/*.md` (91 files today) | a stray guide with no topic |
| `internal/cli/guides/ws.md` `## Rules for agents` | prose | T4 adds the `@artifact:` bullet |
| `internal/cli/guides/ws-machine.md` | the synopsis block and "When to use it" list | T3 adds `ws machine artifact …` lines |

`ws artifact` (people) is **hidden**, so it needs no guide and isn't a
topic; `ws artifact --help` still works. The visible topics are exactly the
11 in README.md › Guides. **T3** adds 7 (`machine artifact`, `machine
artifact add`, `list`, `show`, `get`, `put`, `rm`); **T4** adds the
`comment` command and its 4 topics (`machine artifact comment`, `comment
list`, `comment add`, `comment resolve`). Each guide is added in the same
task as its command, since `TestGuideTopics` fails on either alone.

### Config paths (`internal/config`) — C3

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/config/paths.go`, `Paths` and `NewPaths` | one field per location | add `Artifacts string` (doc comment "Artifacts holds artifacts and their comments (ADR 0078).") = `filepath.Join(root, "artifacts")`, after `Environment` |
| `internal/config/paths_test.go`, `TestNewPaths` | `assert.Equal` on the whole struct | `Artifacts: "/cfg/artifacts"` isn't added |
| `docs/config.md` `### Layout` | the `CONFIG_DIR/` block | add `  artifacts/           files people and agents share, and their comments (0078)` after `machine/roles/` |

No `config.toml` key is added: limits are constants in `internal/artifact`.

### Runner guard (`internal/agent`) — C4

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `internal/agent/tools/guard.go`, `isAllowedWS` | allows `ws machine result` and `ws machine progress set|step|show` | add `ws machine artifact <add|list|show|get|put|rm>` and `ws machine artifact comment <list|add|resolve>` |
| `guard.go`, the refusal message in `checkWS` | `ws may only run "ws machine result" or "ws machine progress set|step|show"` | update to name `ws machine artifact` too; `guard_test.go` matches `"command refused"` only |
| `internal/agent/tools/guard_test.go` table near line 87 | allow/refuse cases | add "allows ws machine artifact get", "refuses bare ws machine artifact", "refuses ws machine artifact comment" (bare) |
| `internal/agent/preamble.md` line 9 | "Do not run ws (except `ws machine result` and `ws machine progress`)" | add `ws machine artifact`; check for a golden of the preamble: `grep -rn "except \`ws machine result\`" internal/` |
| `docs/adrs/0066-runner-security-boundaries.md` | lists the allowed `ws` commands (0077 amended it) | add a line ``` `ws machine artifact` allowed by [0078](0078-artifacts.md) ``` |

### Lint rules (`.golangci.yml`, `Makefile`) — C5

| Rule | Effect on these tasks |
| --- | --- |
| Owner rule, enforced: **no blank lines inside function bodies** (`internal/blanklines`, run by `make lint`) | write each body as one block; split functions instead |
| `Makefile` greps: `exec.Command…"git"` only in `internal/git/`, `"claude"` only in `internal/claude/`, `"gh"` only in `internal/gh/` | `internal/artifact` runs no programs at all |
| `depguard` `cobra`: only `internal/cli`, `cmd/ws` | `internal/artifact` never imports cobra |
| `forbidigo`: `fmt.Print*`, `print`, `log.*`, `os.Stdout/Stderr/Stdin`, `os.Exit` | `cat` writes to `a.stdout`; `-` reads `a.stdin` (fields of `App`) |
| `gochecknoglobals`, `gochecknoinits` | only `err*` sentinels and `regexp.MustCompile` patterns at package level; the allowed-kind table is a function or a `switch` |
| `funlen` 60 lines / 40 statements, `cyclop` 15, `gocognit` 20, `nestif` 4 | anchor matching and type sniffing split into small functions |
| `errname` | sentinels `ErrX`/`errX`; error types `xError` |
| `godot`, `revive` `exported` | every comment ends with a period; every exported name has a doc comment |
| `tagliatelle` | JSON tags `snake_case` |
| `gosec` | files 0600, dirs 0700 (G301/G306); reads of paths built from the store dir carry `//nolint:gosec // G304: path built from the store's own dir and a checked id.` only where the linter flags them |
| `testifylint`, `thelper`, `usetesting` | `require` for preconditions, `assert` for checks, `t.Context()`, `t.TempDir()` |
| `exhaustive` | a `switch` on a kind lists every kind or has `default` |
| Owner rules not linted | interfaces at the consumer, all of a package's in its one `interfaces.go` with one `//go:generate go tool moq` line; mocks only from moq (`moq_test.go`), never by hand; unexported by default; `*slog.Logger` passed in; `t.Parallel()` only on tests that block on I/O (the store's tests touch the filesystem: they may) |

`internal/cli/interfaces.go`'s moq line (`configLoader repoCache
workspaceStore lifetimeManager shellRunner claudeClient ghClient
runnerDaemon`) needs no change: the CLI uses `*artifact.Store` directly
over `t.TempDir()`, as it uses `*machine.Store`.

### Docs with lists — C6

| File | What to add (T5) |
| --- | --- |
| `docs/tools/README.md` command table | a row ``[`ws artifact`](ws-artifact.md)`` after `ws env`: "Share files with agents and comment on them; used by the desktop app; hidden from `ws --help`" |
| `docs/tools/ws-artifact.md` | new page: the people commands, JSON, errors, layout |
| `docs/tools/ws-machine.md` | `## ws machine artifact` section before `## Errors`, the synopsis lines in `## Synopsis`, the new codes in `## Errors` |
| `docs/state.md` `## Layout` | rows for `artifacts/<id>/artifact.json`, `r<n>`, `comments.json`, `.lock`, `artifacts/.tmp/`, and `<workspace>/.ws-artifacts/` (written by `get` on an agent's request); and the "Workspace contents" bullet amended: "except `.ws-artifacts/`, which `ws machine artifact get` writes when an agent asks" |
| `README.md` `## Commands` and `## Roadmap` | one line for artifacts in each |
| `docs/adrs/0016-output-contract.md` `Status:` | see C1 |
| `docs/adrs/README.md` | rows 0078 and 0079 already added by the design (status Accepted); don't add again |

## Pinned values in the desktop (`desktop/`)

### Command lists — C7

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `desktop/src-tauri/src/lib.rs`, `tauri::generate_handler![…]` | 81 commands, ending `stats::stats_get` | the 8 `artifacts::…` commands aren't appended after `stats::stats_get`, in the order of README.md's table |
| `desktop/src-tauri/src/online/dispatch.rs`, `COMMANDS` | the same 81 names in the same order; `commands_match_generate_handler` compares them | the names aren't appended in the same order |
| `dispatch.rs`, `every_command_is_handled` | dispatching each name with `Value::Null` mustn't be `UnknownCommand` | a name has no `match` arm (a `BadArgs` for missing args is fine) |
| `dispatch.rs`, the `use crate::{…}` line | module list | add `artifacts` |
| `desktop/src-tauri/src/lib.rs` `mod` list | modules | add `pub mod artifacts;` (or `mod`, as the others are) |
| `desktop/src-tauri/capabilities/default.json` | `["core:default"]`; app commands need no permission (no `AppManifest` in `build.rs`) | nothing to change; don't add one |
| `desktop/src-tauri/tauri.conf.json` CSP and `online/server.rs` `CONTENT_SECURITY_POLICY` | `img-src 'self' data:` | nothing to change; **don't** widen either |
| `desktop/src-tauri/Cargo.toml` | `base64ct` is `optional = true` and in the `online` feature list | make it non-optional (`base64ct = { version = "1.8", features = ["alloc"] }`) and remove `"dep:base64ct"` from `online`; `Cargo.lock` is unchanged (same crate) |
| `online/server.rs`, `router` | `/api/invoke/{command}` with axum's default 2 MB body limit | add `.layer(DefaultBodyLimit::max(16 * 1024 * 1024))` to that route only, and a test that a 3 MiB body reaches the handler |

### Frontend sections — C8

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `desktop/src/features/ui/uiSlice.ts:13`, `Section` | `"agents" \| "repos" \| "reviews" \| "loops" \| "stats" \| "settings"` | add `"artifacts"` |
| `desktop/src/features/rail/Rail.tsx:81,84`, `railOrder`, `shownSections` | both `["agents","repos","reviews","loops","stats","settings"]` | insert `"artifacts"` after `"loops"` in both |
| `Rail.tsx`, `icons` and the labels `Record<Section, …>` | one entry per section (TypeScript fails otherwise) | add an icon (a document with a folded corner, same stroke style) and the label "Artifacts" |
| `desktop/src/features/rail/Rail.test.tsx:24` | `toEqual(["Agents","Repos","Reviews","Loops","Stats","Settings"])` | add `"Artifacts"` after `"Loops"` |
| `desktop/src/App.tsx:120`, `sectionViews`; `Warnings`'s exhaustive `switch` at line ~133 | one view per section; the `switch` covers every section | add `artifacts: { actions: ArtifactsActions, list: ArtifactsList, pane: ArtifactsPane }` and a `case "artifacts":` returning the slice's warnings |
| `desktop/src/store.ts` | the reducers | add `artifacts: artifactsReducer` |
| `desktop/src/api.ts` `api` object | one wrapper per command | add the 8 wrappers |
| `desktop/test/tauri-mock.js` | answers every command the app calls; logs unhandled ones | add the 8 `artifact_*` commands with sample data (T6) |
| `desktop/test/web-mock.js` | turns the Tauri mock into `/api` for web mode | nothing: it forwards every command to the Tauri mock |

## What the pod can and can't run — C9

Measured in this pod on 2026-11-23: `uname -m` = `x86_64`, 48 CPUs,
125 GiB RAM (104 available); `go version` = `go1.27.3 linux/amd64`
(`go.mod`: `go 1.27.0`); `node --version` = `v20.19.2`, `npm` 9.2.0;
`cargo 1.99.1`, `rustc 1.99.1`; `pkg-config` finds `webkit2gtk-4.1` and
`gtk+-3.0`; `make` at `/usr/bin/make`; `DOCKER_HOST` is a remote
`tcp://docker-host.docker-host.svc.cluster.local:2375`; Playwright's
Chromium is cached in `~/.cache/ms-playwright` (`chromium-1248`), but no
`playwright` package is installed in `desktop/` (`desktop/node_modules`
doesn't exist until `npm ci`); `nodejs.org` is reachable
(`SHASUMS256.txt` for v24.12.0 lists
`bdebee276e58d0ef5448f3d5ac12c67daa963dd5e0a9bb621a53d1cefbc852fd
node-v24.12.0-linux-x64.tar.xz`).

`cargo check --all-targets` in `desktop/src-tauri` (default `desktop`
feature, with Tauri) **passes here** in 1m36s with `-j 8` and a fresh
target directory, so Rust code for the desktop can be checked and tested
in the pod.

| Can't | Consequence for tasks |
| --- | --- |
| `npm test` / `npm run typecheck` with Node 20.19.2 | `vitest` 5.1.0 requires Node `^22.12.0 \|\| ^24.0.0 \|\| >=26.0.0` (`package-lock.json`). The desktop implementer (T6) installs Node 24.12.0 itself into `$CLAUDE_JOB_DIR/tmp/node` from the official tarball, checking the SHA-256 above, and puts its `bin` first on `PATH` for `npm ci`, `npm run typecheck` and `npm test -- --maxWorkers=2 --testTimeout=60000`. If that is refused or fails, the task lists the frontend checks as unverified and the whole-branch verifier (which the orchestrator gives Node 24) runs them |
| A Tauri window (no display) | nothing runs the desktop app; the screenshot child uses Vite + Playwright with `desktop/test/tauri-mock.js` (handbook: "screenshots of the desktop app"), which needs `npm i -D playwright` per run and Node 22+ |
| A local Docker daemon | no `make online-image` / `make online-smoke` checks here; Workspace Online is tested by `cargo test --no-default-features --features online` |
| Windows, macOS | CI's legs only; use nothing Linux-only in `internal/artifact` (`syscall.Flock` is fine: `internal/lockfile` already wraps locking, use it) |
| A remote environment | `artifact_unreachable` is tested by writing any `environment/agent.toml` file in a temp `CONFIG_DIR`, not with a real agent |
| `make test` cheaply | run `go test -p 2` on the task's packages; `make lint` lints the whole repo (2-4 min), once per task, last |

Run heavy checks one at a time: `go test -p 2 ./internal/<pkg>/...`, then
`make lint`; in the desktop `cargo test -j 2`, then `cargo clippy
--all-targets -- -D warnings`, then the `online` feature pair. Never wait
on a permission prompt: list a check you couldn't run as unverified.

## Not verified by this design

- `make lint` and `make test` on `main` (the orchestrator's baseline
  verifier covers it).
- That `node.position` reaches `react-markdown` components with source
  offsets in this repo's versions (T6 asserts it in its first test).
- Whether Claude Code in default permission mode reads
  `<workspace>/.ws-artifacts/…` from a one-repo session without asking
  (ADR 0078, Consequences).
- `npm test` on any branch in this pod (Node 20).
