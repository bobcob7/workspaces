# wsenv: tasks

Two goals, built as six tasks:

- **Goal 1, per-workspace Claude Code config** ([ADR 0085](../../adrs/0085-per-workspace-claude-config.md)): T1, T2, T3, T6.
- **Goal 2, agent services on remote environments** ([audit.md](audit.md), bugs 1-11): T4, T5, and bug 11 in T3.

**Every task:** read [constraints.md](constraints.md) first (pod limits,
lint rules, pinned values, no blank lines in function bodies). Run heavy
checks one at a time with `-p 2`. Commit and push after each file. For a
bug fix, commit the failing test first (`test: ...`), run it and record
that it fails, then commit the fix (`fix: ...`). No `Co-Authored-By` or AI
attribution in commits. Never start a real Claude Code session in a test.
List anything you couldn't run under `## Not run here` in your report.

The designer wrote the ADR, the audit and config.md; they are done and
not tasks.

## Order

```
T1 ∥ T2 ∥ T4 ∥ T5      (disjoint files; run at most one heavy one at a time)
        ↓
       T3              (needs T1, T2, T5: shares internal/cli/claude.go and machine.go with T5)
        ↓
       T6  small       (docs)
```

The common verify block, `VERIFY(pkgs)`, is:

```bash
go build ./... && go vet ./... \
&& go test -p 2 -count=1 <pkgs> \
&& go tool golangci-lint run ./... \
&& go test -count=1 -run '^TestRepository$' ./internal/blanklines/
```

---

## T1: Overlay seeding and the `claude.config_dir` key

- **Depends on:** none. **Parallel with:** T2, T4, T5.
- **Files:** `internal/claude/overlay.go` (new), `internal/claude/overlay_test.go` (new), `internal/config/config.go`, `internal/config/config_test.go`, `internal/config/edit.go`, `internal/config/edit_test.go`.
- **Constraints:** [constraints.md](constraints.md), especially the `ws config list` pins.

### internal/claude/overlay.go

```go
// OverlayMarker is the file that marks an overlay; its one line is the base's absolute path.
const OverlayMarker = ".ws-overlay"

// BaseConfigDir is the user's Claude Code config directory: the marker's path
// when $CLAUDE_CONFIG_DIR is an overlay, else $CLAUDE_CONFIG_DIR, else
// $HOME/.claude, else "".
func BaseConfigDir(environ []string) string

// BaseEnviron is environ with CLAUDE_CONFIG_DIR set to BaseConfigDir when
// $CLAUDE_CONFIG_DIR is an overlay; otherwise environ unchanged (same slice contents).
func BaseEnviron(environ []string) []string

// GlobalConfigFile is Claude Code's global config for environ:
// configFile(environ) exported.
func GlobalConfigFile(environ []string) string

// SeedOverlay makes dir (0700) an overlay of base for a workspace under
// workspacesRoot, as ADR 0085's table says, with a cut-down copy of
// globalConfig (the base's .claude.json). It never writes under base,
// except to create empty projects/ and jobs/. An existing dir is removed
// and made again.
func SeedOverlay(dir, base, globalConfig, workspacesRoot string) error
```

Use the package's existing `lookup` and `userHome` helpers. `SeedOverlay`:

1. `os.RemoveAll(dir)`, `os.MkdirAll(dir, 0o700)`, write `dir/.ws-overlay` = `base + "\n"` (0600).
2. **Copy** (regular files, 0600, skip if missing in base): `settings.json`, `CLAUDE.md`, `keybindings.json`.
3. **Symlink** `dir/<name> -> base/<name>` (skip if missing in base): `.credentials.json`, `hooks`, `agents`, `commands`, `skills`, `plugins`, `output-styles`, `projects`, `jobs`. For `projects` and `jobs`, create them in base first (`MkdirAll` 0700) if missing, so the link always resolves — the only write ADR 0085 allows under base, and only of an empty directory.
4. **`.claude.json`**: read `globalConfig` (the caller passes `GlobalConfigFile(BaseEnviron(environ))`: `$CLAUDE_CONFIG_DIR/.claude.json`, else `$HOME/.claude.json`). Keep only the top-level keys `userID`, `oauthAccount`, `hasCompletedOnboarding`, `lastOnboardingVersion`, `installMethod`, `autoUpdates`, `mcpServers`, `theme`, and under `projects` only the entries whose key is `workspacesRoot` or one of its parents (compare with symlinks resolved, as `trust.go`'s `resolved` does) and whose `hasTrustDialogAccepted` is `true`. Write `dir/.claude.json` 0600 with `json.MarshalIndent`. A missing global file writes `{}`. Unknown values are copied as `json.RawMessage`, unchanged.

### internal/config

- `type Claude struct { ConfigDir string \`toml:"config_dir" json:"config_dir"\` }`, field `Claude Claude \`toml:"claude"\`` on `Config`.
- Constants `ClaudeConfigAuto = "auto"`, `ClaudeConfigWorkspace = "workspace"`, `ClaudeConfigShared = "shared"`; `func (c Claude) Mode() string` returns the value, or `auto` when empty; `func ValidClaudeConfigMode(s string) bool`.
- Loading: a value other than the three (or empty) is `config_invalid` with the message `claude.config_dir must be auto, workspace or shared, not "<v>"`. Follow the pattern of `(*Environments).check` (`config.go:220`).
- `edit.go`: add `claudeRules()` = `{key: "claude.config_dir", typ: TypeString, table: "claude", leaf: "config_dir"}`; in `Settings`, append it **after** `environmentRules()` only `if meta.IsDefined("claude")`; add it to the rule list at `edit.go:447` so `ws config set/unset/get claude.config_dir` work; validate the value on `set` with `ValidClaudeConfigMode` (`config_value_invalid`).

### Acceptance (tests to write)

- `TestBaseConfigDir`: subtests "CLAUDE_CONFIG_DIR wins over HOME", "HOME/.claude when unset", "empty CLAUDE_CONFIG_DIR counts as unset", "an overlay's marker names the base", "neither set is empty".
- `TestBaseEnviron`: an overlay's environ gets the base; a non-overlay environ is unchanged.
- `TestSeedOverlay` (`t.Parallel()`, temp dirs): "copies settings, CLAUDE.md and keybindings" (edit the copy, base unchanged); "links credentials, add-on dirs, projects and jobs" (`os.Readlink`); "skips what base lacks"; "creates projects and jobs in base when missing"; "keeps only the allowed .claude.json keys and the workspace root's trust" (input has `numStartups`, `projects` with the root, a parent of it, and an unrelated dir: only root and parent survive, and only when trusted); "writes {} without a global config"; "re-seeding replaces an existing overlay"; "never changes base" (hash every base file before and after); "the marker holds the base path"; modes 0700/0600.
- `TestConfig_Claude` (config_test.go): default `auto`; each valid value; an invalid value is `config_invalid`.
- `TestSettings` (edit_test.go): no `[claude]` table → key not listed (existing expectations unchanged); with `[claude]` → `claude.config_dir` listed last; `set claude.config_dir bogus` → `config_value_invalid`.

### Verify

`VERIFY(./internal/claude/ ./internal/config/ ./internal/cli/)` (the cli package pins `ws config list` output; it must still pass unchanged).

---

## T2: The Claude Code client pool

- **Depends on:** none. **Parallel with:** T1, T4, T5.
- **Files:** `internal/claude/pool.go` (new), `internal/claude/pool_test.go` (new), `internal/claude/start.go`, `internal/claude/start_test.go`, `internal/claude/client.go`, `internal/claude/client_test.go`, `internal/claude/agents.go`, `internal/claude/agents_test.go`.
- **Constraints:** [constraints.md](constraints.md) (claude runs only from `internal/claude`; tests use `fakeClaude` from `client_test.go`).

### Changes

- `StartOptions` gains `ConfigDir string` (the overlay; "" for the base) and `Env []string` (extra `KEY=value` entries, later wins).
- `Session` gains `ConfigDir string` ("" when listed by the base).
- `client.go`:

  ```go
  // WithConfigDir is a copy of c that runs claude with CLAUDE_CONFIG_DIR=dir
  // and extra appended to its environment. jobsDir, projectsDir and
  // configFile follow the new environment.
  func (c *Client) WithConfigDir(dir string, extra []string) *Client

  // SignedIn runs `claude auth status` with CLAUDE_CONFIG_DIR=dir and extra,
  // and reports its JSON "loggedIn". A run that fails or prints no JSON is
  // (false, reason, nil); reason is one short line for the warning.
  func (c *Client) SignedIn(ctx context.Context, dir string, extra []string) (bool, string, error)
  ```

  `execWith` must build the command environment as `c.environ` then `extra` (one copy; no shared slice mutation).
- `start.go`: `Start` appends `opts.Env` to the command's environment (through a client from `WithConfigDir` when `opts.ConfigDir != ""`).
- `pool.go`:

  ```go
  // Overlay is a workspace's own Claude Code config directory (ADR 0085).
  type Overlay struct {
      ConfigDir    string // CONFIG_DIR/state/<id>/claude
      WorkspaceDir string // CONFIG_DIR/workspaces/<id>
  }

  // Pool is a Client for the base config directory plus one per overlay,
  // with the same methods as Client, routing each call as ADR 0085 says.
  type Pool struct { /* base *Client; overlays func() ([]Overlay, error); mu; routes map[string]string */ }

  func NewPool(base *Client, overlays func() ([]Overlay, error)) *Pool
  ```

  Methods with the exact signatures of `Client`'s for every method in the `claudeClient` interface (`internal/cli/interfaces.go:137-158`) and `lifetime`'s interface (`internal/lifetime/interfaces.go`), plus `SignedIn` (delegates to base). Routing:
  - `Agents`: base plus each overlay, at most 8 `claude agents` at once (a buffered-channel semaphore and `sync.WaitGroup`; no new module). Sessions from an overlay get `ConfigDir` set; remember `routes[s.ID] = dir`. A base error is returned; an overlay's error is logged at debug and skipped. Order: base first, then overlays in `ConfigDir` order, each in its listing's order.
  - `Start`: with `opts.ConfigDir` use `base.WithConfigDir(opts.ConfigDir, nil)`, record the route of the new id.
  - `Attach`, `Stop`, `Remove`, `Respawn`, `Logs` (by id), `Send` (by `s.ID`, or `s.ConfigDir` when set): the route of the id; on a miss call `Agents` once and look again; else the base.
  - `Trust`, `Untrust`: the overlay whose `WorkspaceDir` contains the directory (path prefix with a separator, symlinks resolved), else the base.
  - `StopDaemon`: base, then each overlay; return the first error after trying all. `RunDaemon`, `DaemonStatus`, `Check`, `JobExists`, `UsageLimit`, `Model`, `Recorded`: base.
- Add `var _ = (*Pool)(nil)` compile checks in the test file against a local interface listing the methods (internal/claude can't import internal/cli).

### Acceptance (tests to write)

Use `fakeClaude` with a body that logs `$CLAUDE_CONFIG_DIR $*` to `$CALLS` and prints a canned `agents --json` per directory (key the output on `$CLAUDE_CONFIG_DIR`).

- `TestClient_WithConfigDir`: env carries `CLAUDE_CONFIG_DIR` and extras; the original client is unchanged; `configFile` is `<dir>/.claude.json`.
- `TestClient_SignedIn`: `{"loggedIn":true}` → true; `{"loggedIn":false}` → false with reason; non-JSON → false with reason, no error.
- `TestClient_Start`: `Env` entries reach the command (`start_test.go`).
- `TestPool_Agents`: merges base and two overlays with `ConfigDir` set; a failing overlay is skipped; a failing base is an error; never more than 8 concurrent (count with a lock file or skip the bound check if flaky: then assert the bound by a unit test of the semaphore helper).
- `TestPool_Stop` (and `Remove`, `Logs`, `Attach` via the `replace` hook, `Respawn`, `Send`): routed to the overlay that listed the id; an unknown id lists once, then goes to the base.
- `TestPool_Start`: runs with the overlay's `CLAUDE_CONFIG_DIR`; the new id is routed there without a listing.
- `TestPool_Trust`: a directory under an overlay's workspace writes that overlay's `.claude.json` (seed one with a trusted parent entry); others write the base's.
- `TestPool_StopDaemon`: stops all; reports the first error.

### Verify

`VERIFY(./internal/claude/)`

---

## T3: Wire the pool, the mode and `WS_CONFIG_DIR` into `ws` (ADR 0085, audit bug 11)

- **Depends on:** T1, T2, T5 (merged).
- **Files:** `internal/cli/app.go`, `internal/cli/interfaces.go`, `internal/cli/claude.go`, `internal/cli/claude_config.go` (new), `internal/cli/claude_config_test.go` (new), `internal/cli/machine.go`, `internal/cli/loop_tick.go`, `internal/cli/review_start.go`, `internal/cli/errors.go`, the `claudeClient` fake in `internal/cli` tests (find it with `grep -rn "func (.*) SignedIn\|Respawn(ctx" internal/cli/*_test.go`), `internal/lifetime/interfaces.go` (only if `SignedIn` is needed there: it isn't), `internal/cli/guides/ws-claude.md`, `internal/cli/guides/ws-machine-spawn.md`, `docs/tools/ws-claude.md`, `docs/tools/ws-machine.md`.
- **Constraints:** [constraints.md](constraints.md).

### Changes

- `app.go:159`: `a.claude = claude.NewPool(claude.New(log, "claude", claude.BaseEnviron(environ)), a.claudeOverlays)`. `claudeOverlays` (in `claude_config.go`) lists `a.paths.State/*/claude/` that hold `claude.OverlayMarker`, as `claude.Overlay{ConfigDir, WorkspaceDir: filepath.Join(a.paths.Workspaces, id)}`; before `a.paths` is set it returns none. Check every other `claude.New` (`env_agent.go:151` keeps the base client for the hello probe: wrap its environ with `claude.BaseEnviron`).
- `interfaces.go`: add `SignedIn(ctx context.Context, dir string, extra []string) (bool, string, error)` to `claudeClient`; update the test fake.
- `errors.go`: `codeClaudeSignedOut errorCode = "claude_signed_out"`, exit 1 (add it to the list at line 165 and the exit switch at 194, as `codeClaudeUntrusted`).
- `claude_config.go`:

  ```go
  // claudeConfigMode is the flag, else $WS_CLAUDE_CONFIG_DIR, else
  // [claude] config_dir, else auto. A bad flag or variable is usage.
  func (a *App) claudeConfigMode(flag string) (string, error)

  // claudeStart is the config directory and extra environment of the session
  // ws starts in ws (ADR 0085). Extra always holds WS_CONFIG_DIR=<CONFIG_DIR>.
  // shared: ("", extra). Otherwise it seeds CONFIG_DIR/state/<id>/claude with
  // claude.SeedOverlay(dir, claude.BaseConfigDir(a.environ),
  // claude.GlobalConfigFile(claude.BaseEnviron(a.environ)), a.paths.Workspaces), adds CLAUDE_CODE_OAUTH_TOKEN and
  // ANTHROPIC_API_KEY from secrets.env when set there and not in a.environ,
  // and checks SignedIn. Not signed in: removes the overlay; workspace mode
  // fails claude_signed_out; auto appends the ADR's warning to a.warned and
  // returns ("", ["WS_CONFIG_DIR=..."]). A seed error in auto mode is the
  // same fallback with the error as the reason.
  func (a *App) claudeStart(ctx context.Context, ws *workspace.Workspace, mode string) (configDir string, extra []string, err error)
  ```

  Warning text exactly: `Claude Code isn't signed in for a separate config directory (<reason>); the agent shares <base>`.
- `claude.go`: `startAgent(ctx, ws, args, mode string)` calls `claudeStart` **before** `agentDir` (so `Trust` finds the overlay), then `a.claude.Start(ctx, claude.StartOptions{Dir, Name, Args, ConfigDir, Env: extra})`. On `claudeStart` error: `a.removeWorkspace(ws.ID)` and return it. `handOff` passes the mode through. `runClaude` resolves the mode from `opts.claudeConfigDir` (new `claudeOptions` field) **before** creating the workspace, so a bad flag creates nothing.
- Flags: `ws claude --claude-config-dir <auto|workspace|shared>` and the same on `ws machine spawn` (`machine.go`, next to `--hidden`). With `--env` (remote) or `--runner ws`: `usage` ("--claude-config-dir can't be used with --env; the environment's own setting applies" / "... with --runner ws").
- `loop_tick.go:387` and `review_start.go:387`: pass `""` (the variable and the key decide).

### Acceptance (tests to write; all with the fake `claudeClient`)

- `TestApp_claudeConfigMode`: flag beats variable beats key beats `auto`; a bad flag or variable is `usage`.
- `TestApp_claudeStart`: subtests "shared starts in the base with WS_CONFIG_DIR", "auto signed in uses the overlay", "auto signed out falls back with the warning and removes the overlay", "workspace signed out is claude_signed_out and removes the overlay", "secrets.env token is passed when the environment lacks it", "the environment's token is not duplicated".
- `TestApp_runClaude` (existing test, new subtests): the fake records `StartOptions`: `ConfigDir` is `<state>/<id>/claude` and `Env` contains `WS_CONFIG_DIR=<root>`; `--claude-config-dir shared` gives `ConfigDir == ""`; `--claude-config-dir bogus` exits 2 and creates no workspace; with `--env` it is `usage`.
- **Bug 11, failing first:** before any other change, add the subtest "the session gets WS_CONFIG_DIR" asserting `Env` contains `WS_CONFIG_DIR=<a.paths.Root>` for a shared-mode start (it fails on the branch because `StartOptions.Env` is never set; after T2 the field exists).
- `TestApp_runMachineSpawn`: the flag reaches `StartOptions`; with `--runner ws` it is `usage`.
- Removal: a test that `ws rm` of a workspace with an overlay deletes `state/<id>/claude` and leaves the overlay's symlink targets (use a temp base dir).

### Verify

`VERIFY(./internal/cli/ ./internal/claude/ ./internal/lifetime/)`, then a smoke with a temp config dir and a fake `claude` first on `PATH` (no real session):

```bash
make build
T=$(mktemp -d); mkdir -p $T/bin $T/home/.claude
printf '#!/bin/sh\ncase "$1" in --version) echo "2.1.332 (Claude Code)";; auth) echo "{\\"loggedIn\\":false}";; agents) echo "[]";; --bg) echo "backgrounded · abcd1234 · $3";; esac\n' > $T/bin/claude; chmod +x $T/bin/claude
HOME=$T/home PATH=$T/bin:$PATH WS_CONFIG_DIR=$T/cfg ./bin/ws --json claude --offline 2>&1 | grep -o "isn't signed in for a separate config directory"
```

(The fake's `--bg` line must match what `parseBackgrounded` expects; adjust it, not the code, if it doesn't.)

---

## T4: Remote children: results, wait, progress and rm (audit bugs 2, 3, 4, 6, 8)

- **Depends on:** none. **Parallel with:** T1, T2, T5.
- **Files:** `internal/machine/remote.go`, `internal/machine/remote_test.go`, `internal/cli/machine_remote.go`, `internal/cli/machine_remote_test.go`, `internal/cli/machine_wait.go`, `internal/cli/machine_wait_test.go`, `internal/cli/machine_progress.go`, `internal/cli/machine_progress_test.go`.
- **Constraints:** [constraints.md](constraints.md). Test helpers already in `machine_remote_test.go`: `childHost`, `serveChildHost`, `helperAnswer`, `remoteHelperChild`, `remoteHelperRecord`, `writeCopy`, `childOpsOf`, `removeHelper`.

For each bug: commit the failing test, run it (`go test -p 2 -count=1 -run '<name>' ./internal/...`) and note the failure in the report, then fix.

1. **Bug 2** (`machine/remote.go:39-40`). Test `TestStore_WriteRemote` subtest "a later result replaces the copy": write a copy with verdict `FAIL` at `t0`, then `PASS` at `t0+1m` → `ReadRemote` gives `PASS` and the new report; "an older or equal result keeps the copy": `PASS` at `t0`, then `FAIL` at `t0` → `PASS`. Fix: read the existing `result.toml`; keep it only when its `RecordedAt` is not before `r.RecordedAt`; otherwise rewrite spawn (when given), report, then result last. CLI test `TestApp_runMachineWait_remoteChild` subtest "a later verdict replaces the copied one": `writeCopy` FAIL, host answers PASS with a later `recorded_at` → item `outcome: pass`.
2. **Bug 3** (`machine_remote.go:460-463`). Test subtest of `TestApp_runMachineWait_remoteChild`: "an unreachable environment with a copied result finishes": `writeCopy` PASS, host's record answers `environment_offline` → `wait --timeout 5s` exits 0 with `outcome: pass` well before the timeout (assert elapsed < 3 s with the test's short poll). Fix: `finished: rec.Result != nil, quiet: rec.Result != nil`.
3. **Bug 4** (`machine_wait.go:404`). Add `progress *progressJSON` and `rollup *rollupJSON` to `childState`; `askRemote` fills them from `a.remoteProgress(res)` (needs `res` from `remoteRecord`); `copiedChildState` leaves them nil. When building items, a remote target uses `s.progress, s.rollup` instead of `childProgress`. Test: in "a finished remote child is copied back" (`machine_remote_test.go` near line 246), assert the item's `progress` equals the host's and `rollup` is `{"total":N,"done":N}` as `remoteProgress` computes.
4. **Bug 6** (`machine_progress.go:192-214`). `callerProgress` takes `ctx` and calls `a.nestRemoteProgress(ctx, &node)` as `show` does (a failure there is a warning, as in `show`). Test `TestApp_runMachineProgress` subtest "step counts remote children in the rollup", mirroring `TestApp_nestRemoteProgress`'s setup: after `progress step`, the printed `rollup` includes the remote child's steps.
5. **Bug 8** (`machine_remote.go:376-387`). Before `remoteChildOp(..., "rm", ...)`, call `a.remoteRecord(ctx, root, rc)` and ignore its error (an unreachable environment keeps the child anyway). `removeRemote` gains the `root string` parameter; update its caller. Test `TestApp_runMachineRm_remoteChild` subtest "a finished child's verdict stays readable after rm": host answers a PASS record, then the rm; afterwards `machine.NewStore(state).ReadRemote(root, env, child)` has the result, and `wait <child>` returns `pass`.

### Verify

`VERIFY(./internal/machine/ ./internal/cli/)`

---

## T5: Remote spawn, agent ops and review wait (audit bugs 1, 5, 7, 9, 10)

- **Depends on:** none. **Parallel with:** T1, T2, T4.
- **Files:** `internal/cli/claude.go` (only `artifactHints`, lines 140-160), `internal/cli/claude_test.go`, `internal/cli/scope.go`, `internal/cli/scope_test.go`, `internal/cli/machine.go` (only `runMachineSpawn`, lines 240-262), `internal/cli/machine_test.go`, `internal/cli/env_agent_sessions.go`, `internal/cli/env_agent_sessions_test.go`, `internal/cli/review_env.go`, `internal/cli/review_env_test.go`, `docs/tools/ws-machine.md`.
- **Constraints:** [constraints.md](constraints.md). Helpers: `serveScopeHost`, `writeCallerScope` (`scope_test.go`); `agentTestOps`, `agentTestOpsList`, `listingAgents`, `notAgentCases` (`env_agent_sessions_test.go`); `opsFor`, `claudeOps` (`env_agent_ops_test.go`); `TestApp_waitRemoteReview` (`review_env_test.go:324`).

Failing test first for each, as in T4.

1. **Bug 1.** `artifactHints(args, remote)`: when `remote == ""` and `a.enrolledAgent()`, append the warning `artifacts aren't reachable on an environment's agent; @artifact references were sent as plain text` and return `args` unchanged (no existence check). `runRemoteSpawn` (`scope.go:240`): before building `create`, when the prompt (first non-flag argument after `--`) has refs, call `a.artifactHints(opts.claudeArgs, opts.target)` for its warning and use the returned args. Tests: `TestApp_artifactHints` "an enrolled agent passes references with a warning" (write `<root>/environment/agent.toml`; before the fix: `artifact_unknown`); `TestApp_runRemoteSpawn` (or the remote spawn cases in `machine_test.go` ~446-510) "an @artifact prompt warns": `warnings` contains `artifacts aren't reachable from <env>`.
2. **Bug 9.** In `runMachineSpawn`, set `opts.runner = which` before `a.runRemoteSpawn(...)`. Test in `machine_test.go` remote spawn cases: a role whose config has `[runner.roles.<role>] runner = "ws"` (and a `runner.model`) spawned with `--env <target>` sends `create.kind == "runner"` to `serveScopeHost` (capture the request). Before the fix: `claude`.
3. **Bug 5.** `agentSession` accepts a session with `Runner == lifetime.RunnerWS` (shells and missing sessions stay `usage`). `agentStatus`: for a runner, `Agent = &envproto.SnapshotAgent{ID: s.ID, Name: ws.ID, State: string(s.State), Status: ""}` with `PID` set when `s.PID > 0`; Claude sessions unchanged. `agentLogs`: for a runner, read `runner.FilesFor(o.app.paths.State, ws.ID)` with `agent.Tail(files.Transcript(), n)` (`n` = `args.Tail`, or `envproto.DefaultTail` for 0), each event `json.Marshal`ed as one line; `Truncated` = `more`; then `fitLogs`. Update `notAgentCases` so a runner is no longer in it. Tests: `TestAgentOperations_agentStatus` "a ws runner reports its lifetime state" (`stopped`); `TestAgentOperations_agentLogs` "a ws runner returns its transcript events". Before the fix: `usage`.
4. **Bug 7.** `tailLines` returns `(lines []string, cut bool)`; `agentLogs` sets `result.Truncated = cut || <fitLogs's own truncation>`. Test `TestAgentOperations_agentLogs` "tail 0 keeps 200 lines and says truncated" with 250 lines → 200 lines, `truncated: true`.
5. **Bug 10.** In the review wait loop (`review_env.go:372-395`), when `callRemoteObject` fails with `environment_offline` (`hasErrorCode(err, codeEnvironmentOffline)`, `errors.go:105`), write one line to stderr `ws: <env> unreachable, still waiting: <err>` and continue polling until the deadline; other errors still return. Test `TestApp_waitRemoteReview` "an offline blip doesn't end the wait": the fake answers `environment_offline` once, then `finished: true` → the wait succeeds.
6. **Docs** (`docs/tools/ws-machine.md`): under `logs`, a remote child's `--tail` is at most 2000 lines and 0 means 200 (with `truncated`); under `memory`, a child on another environment is its own root, so it can't use its parent loop run's memory (audit follow-up F1).

### Verify

`VERIFY(./internal/cli/)`

---

## T6 (small): Docs for ADR 0085

- **Depends on:** T3. Docs only; may skip the task review (orchestration E8).
- **Files:** `docs/state.md`, `docs/tools/ws-config.md`, `internal/cli/guides/ws-config-list.md`, `docs/adrs/0012-workspace-layout.md` is **not** edited (accepted ADRs are immutable); `docs/adrs/README.md` row for 0012 gets "`state/<id>/claude/` added by [0085](0085-per-workspace-claude-config.md)".
- **Content:**
  - `docs/state.md`: a row `state/<id>/claude/` — "the agent's own Claude Code config directory, an overlay of the user's (ADR 0085): `.ws-overlay`, a private `.claude.json`, copies of `settings.json`, `CLAUDE.md`, `keybindings.json`, symlinks to the rest" — written "by `ws claude`, `ws machine spawn`, loop ticks and `ws review start` when the mode isn't `shared`; deleted with `state/<id>/`".
  - `docs/tools/ws-config.md` and `guides/ws-config-list.md`: `claude.config_dir` (string; `auto`, `workspace`, `shared`; listed only with a `[claude]` table).
- **Verify:** `go test -p 2 -count=1 ./internal/cli/ -run 'Guide|Llm|Help'` (guides are embedded and some tests pin them) and `git diff --stat` shows only the listed files.

---

## Follow-ups

- **F1** (audit): loop memory from a remote child of a loop run needs a `memory.*` scope operation and protocol 5. Documented as a limitation by T5.
- **F2** (audit): `children` asks the agent socket even when the scope is self only (`machine_remote.go:54`); one wasted local call, no wrong result.
- **F3** (ADR 0085, unverified here): check on a machine with a signed-in Claude Code (and on macOS) that an overlay session signs in, that a token refresh doesn't break the base, and how long `ws list` takes with 20 overlay agents. This needs a real signed-in session, which tests must not start.
- **F4** (ADR 0085): the desktop's Settings could show `claude.config_dir`; it needs a desktop task with Node 22.12+, which the pod lacks.
