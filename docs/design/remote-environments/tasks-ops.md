# Tasks: PR 3, "Operations"

PR 3 of [ADR 0076](../../adrs/0076-environment-platforms-and-phasing.md)'s
stack, row 3: the agent's operations (`workspace.create` for the `claude` and
`runner` kinds, `workspace.list`, `workspace.rm`, `workspace.rename`,
`workspace.hide`, `agent.status`, `agent.logs`, `agent.send`, `agent.stop`),
run by the agent with the same code a local `ws` runs; the controller's side,
which sends them over the existing `env/<id>/req` and `env/<id>/res/<req>`
topics; `ws claude --env`, `ws shell --env`, `ws list --env`; the
per-workspace `environment`, `environment_status` and `stale` fields of
`ws list --json`; `environment` on workspace records (version 4) and its
inheritance by children; the error codes `environment_offline`,
`environment_outdated` and `environment_mismatch`; and the docs. It accepts
ADR [0072](../../adrs/0072-remote-environments.md).

**Not in this PR:** terminal and log streams (`terminal.*`, PR 4), so no
`workspace.create` of the `shell` kind (decision D7); the desktop app and
Workspace Online (PR 4); `files.*`, `config.*`, `cache.*`, `review.*`,
`loop.*`; Windows.

**Read first:** [constraints-ops.md](constraints-ops.md) (pinned counts,
lists and golden values this PR moves, lint rules, and what the pod can't
run). Every task below names the rows of it that it touches. The PR 2 files
[tasks.md](tasks.md) and [constraints.md](constraints.md) still describe the
code this PR builds on.

## Rules for every task

- Go conventions: **no blank lines inside function bodies** (T0 makes
  `make test` and `make lint` fail on one); interfaces at the consumer, all
  of a package's interfaces in its one `interfaces.go` with one
  `//go:generate go tool moq -out moq_test.go . <names>` line; mocks only
  from moq (`go generate ./internal/<pkg>/`), never hand-written; unexported
  by default; constructor injection with `*slog.Logger` passed in; tool
  versions only in `go.mod`'s `tool` directive; tests use `t.Context()`,
  `t.TempDir()`, `require` for preconditions and `assert` for verifications,
  `slog.New(slog.NewJSONHandler(io.Discard, nil))`; one test file per source
  file, `Test<Receiver>_<Method>` with `t.Run` subtests; `t.Parallel()` only
  on tests that block on I/O.
- Comments end with a period; every exported name has a doc comment.
- Time in files and JSON is RFC 3339 UTC. JSON fields are `snake_case`; a
  field with no value is `null`, never missing; lists are `[]`, never
  `null`.
- No new module dependency in any task. `go.mod` and `go.sum` don't change.
- Verify with exactly the commands listed, one at a time, `go test` with
  `-p 2`. `make lint` lints the whole repo and runs last. Commit and push
  after each file. Never wait on a permission prompt: list a check you
  couldn't run as unverified. Don't add a `Co-Authored-By` trailer to
  commits in this repo.
- Write a failing test first for every behavior a task adds, then the code.
- Every reply the agent publishes and every snapshot is at most
  `envproto.MaxRequestBytes` (256 KiB): the broker closes a connection whose
  packet is bigger (`internal/mqttbroker/broker.go`, `MaximumPacketSize`).

## Decisions this design adds to the ADRs

Each is marked **Owner review**: the owner should confirm or change it
before T1 starts. T11 writes the accepted ones into ADR 0072 and the docs.

### D1. The agent runs `ws`'s commands in-process, through a fresh `cli.App` per operation (ADR 0073's open question). **Owner review.**

`internal/cli` builds its whole command tree in `App.Run(ctx, args)`, writes
results only to the `stdout` and `stderr` it was given (`cli.New`), keeps no
package state (`gochecknoglobals`), and reports `--json` errors as one
`{"error": {...}}` line on stderr. So the agent can run
`--json --config-dir <its CONFIG_DIR> claude …` in-process: a new `App` per
operation, `stdin` empty (not a terminal, so `ws claude` never attaches),
`stdout` and `stderr` in buffers, and the exit code from `Run`. The result
is byte-for-byte the CLI's `--json` output, which is ADR 0073's conformance
test, with no subprocess, no `PATH` lookup and no version skew between the
agent and the `ws` it would run.

`internal/envagent` can't import `internal/cli` (cli imports envagent), so
envagent owns the dispatcher (allow-list, typed arguments, path checks,
deduplication, concurrency, deadlines) and calls an `operationRunner`
interface that `internal/cli` implements (T4, T5). A subprocess would need
the same dispatcher and gain nothing; it stays the fallback if a command
turns out to need its own process (none does today: `ws claude` catches
`SIGHUP` with `signal.Notify`, which is per-channel and safe beside the
agent's own handlers).

`agent.status|logs|send|stop` have no CLI command for people; they call the
same `internal/claude` client methods that `ws machine logs`, `ws continue`
and `ws rm` call (`Agents`, `Logs`, `Send`, `Stop`).

### D2. Records, `environment`, and what `parent` means on each side. **Owner review.**

- **On the remote**, a workspace is an ordinary record in the agent's own
  `CONFIG_DIR`. The agent stamps `environment = "<its enrolled name>"` on the
  workspaces its operations create, so the record is version 4 (ADR 0072)
  and an older `ws` on that machine leaves it alone. A person running `ws`
  directly on the remote makes ordinary local records (no `environment`).
- **Inheritance by construction.** `workspace.Create` copies the parent
  record's `environment` to the child whenever the parent has one, and uses
  `CreateOptions.Environment` only for a workspace without a parent or whose
  parent has none. `ws machine spawn` and `ws review start`, run by a Claude
  session on the remote, run the remote's own `ws`, whose enclosing
  workspace is the parent: the child is on the same machine and carries the
  same `environment`, with no flag and no network (ADR 0073).
- **On the controller**, a remote workspace is never a `workspace.toml`; it
  is an entry of `state/.environments/<name>/snapshot.json` (PR 2). Its
  `environment` in `ws list` is always the environment's name, whatever the
  remote's record says (the record is informational on the remote; the
  machine is the truth).
- **`parent` is an id within one environment.** A remote entry's `parent`
  is the id of a workspace on the same environment. Ids are unique per
  `CONFIG_DIR` only, so the controller keys trees and lookups by
  `(environment, id)`.
- **`ws claude --env <name> --parent <P>`** looks `P` up among `<name>`'s
  snapshot entries. Found: the agent creates the child with `--parent P`
  (the agent checks again and is the authority). Found only locally or on
  another environment: `environment_mismatch` (exit 4), nothing sent.
  Nowhere: `workspace_not_found` (exit 3).
- **`ws claude --parent <P>` without `--env`**: a local `P` works as today.
  Otherwise, `P` found in exactly one environment's snapshot runs the child
  there (children inherit); in more than one, `usage` ("pass --env").
- **The default parent** (the local workspace `ws` runs in, ADR 0044) is
  dropped when `--env` names a remote environment, with the warning
  `not a child of <id>: it runs on <name>`: only an explicit `--parent` can
  be a mismatch.

### D3. What the snapshot carries, and how stale entries are marked. **Owner review.**

- `envproto.Snapshot` becomes `{"at", "protocol", "workspaces", "agents"}`.
  `workspaces` is the remote's `ws list --json --all` array (hidden ones
  included, so `ws list -a` and `hidden_count` work on the controller),
  without the unknown directories (entries whose `id` is `null`). `agents`
  is the remote's Claude Code sessions (`claude agents --all`: id, session
  id, name, state, status, pid, started_at), so the desktop in PR 4 can tell
  a blocked agent from a done one, as it does locally with
  `claude agents --json`. A PR 2 agent sends neither `protocol` nor `agents`;
  the controller reads `protocol` as 0 and `agents` as `[]`.
- The agent republishes the snapshot at once after an operation that
  changed a workspace, besides its 10 s check and 30 s refresh.
- A snapshot larger than 256 KiB isn't published; the agent logs
  `snapshot too large` with the size, and the controller's entries go stale.
- **Presence comes from the daemon, not a new file.** `ws list` asks the
  running daemon for `GET /v1/status` (`daemon.Dial`, no autostart, 2 s
  timeout). No daemon running, or no answer: every environment is
  `offline`. An entry is `"stale": true` when its environment isn't
  `online`, or its snapshot's `received_at` is more than 90 s old (three
  refresh periods). `environment_status` is `"online"` or `"offline"`;
  local entries are `"environment": "local"`, `"environment_status":
  "online"`, `"stale": false`.
- **Scope in this PR.** `ws list` without `--env` lists local workspaces
  only, exactly as today; `--env <name>` (repeatable; `local` is a name)
  lists those environments. Reason: the desktop app runs `ws list --all`
  and acts on ids with local commands (`ws rm <id>`, `claude attach <id>`);
  a remote id equal to a local one would make it act on the wrong
  workspace. PR 4 makes "every environment" the default together with the
  desktop's environment badges. This defers part of ADR 0072's "ws list
  shows every environment" by one PR.

### D4. Timeouts and idempotency: a request id the agent deduplicates on. **Owner review.**

- Every request carries `request_id` (16 lowercase hex characters, made by
  the CLI with `crypto/rand`) and `deadline` (RFC 3339 UTC) in its envelope.
  The correlation data is the request id and the response topic is
  `env/<id>/res/<request_id>`. The MQTT message expiry is the operation's
  timeout (ADR 0074).
- Timeouts, per operation (in `envproto.LookupOp`, T1):
  `workspace.create` 10 min; `workspace.rm` 2 min; `agent.send` 90 s;
  every other operation 30 s.
- **The agent finishes before the controller gives up.** It runs each
  operation under `context.WithDeadline(deadline)`; `ws claude` removes a
  workspace whose context ends before `claude --bg` starts (existing
  `startAgent`). The controller waits until `deadline + 15 s`. So the agent
  always answers in time unless its connection drops.
- **Deduplication.** The agent keeps, in memory, each request id it has
  seen: running, or finished with its reply, for 15 min after it finished,
  at most 512 ids (the oldest finished one is dropped first). A duplicate
  of a running request waits for it and gets the same reply on its own
  response topic; a duplicate of a finished one gets the stored reply. The
  operation runs once.
- **Where duplicates come from.** If the environment goes `offline` and
  back `online` while a call waits, the controller republishes the same
  request (same id, the remaining expiry) once. A reply lost in the
  reconnect is then answered from the agent's store, so no second workspace
  is created.
- **What's left.** If the agent's process restarts mid-create, the store is
  lost with the operation; `ws`'s removal pass on the remote removes the
  half-built workspace (ADR 0015). If the controller still times out, the
  CLI fails `environment_offline` with details
  `{"environment", "request_id", "op", "reason": "timeout"}` and the message
  says the operation may still have run and `ws list --env <name>` shows the
  result. The workspace, if made, is an ordinary listed workspace that
  `ws rm` can remove; there's no hidden orphan.

### D5. The version check uses a protocol number, defined next to the operations. **Owner review.**

ws versions are `git describe` strings (`v0.2.0-14-gabc1234`, `dev-…`) that
don't compare reliably. So the hello gains `"protocol"`, an integer the
agent sends (`envproto.Protocol`, 1 in this PR; a PR 2 agent sends none,
read as 0), and each operation has `Since`, the protocol it needs. Both
live in `internal/envproto/ops.go` (T1), the one table of operations. The
controller refuses an operation with `Since` above the agent's protocol
before sending it: `environment_outdated` (exit 1), details
`{"environment", "op", "ws_version", "protocol", "need_protocol"}`, message
`linux-server runs ws <ws_version>, too old for <op>; update ws there to
this controller's version (<version>).` PR 4 bumps `Protocol` to 2 for
`terminal.*`.

### D6. Security: an allow-list of typed operations, checked three times. **Owner review.**

- **Allow-list.** `envproto.LookupOp(name)` knows every operation with its
  caller (`CallerController`: sent on `env/<id>/req`; `CallerAgent`: sent on
  `env/<id>/up`), `Since` and timeout. Anything else is `usage`
  ("unknown operation").
- **Typed arguments.** Each operation's `args` decode into its struct with
  unknown keys refused (`json.Decoder.DisallowUnknownFields`) and are
  validated before anything runs (T1 `Validate`): workspace references,
  names, specs (each must parse with `repo.ParseSpec` on the agent, which
  already refuses local paths and `file://`; and must not start with `-`),
  the model (`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`), the prompt (1 to
  65536 bytes), a message (1 to 16384 bytes), `tail` (0 to 2000). The agent
  builds the command line itself from the typed fields; no string from the
  controller becomes a flag. There are no free-form Claude arguments: only
  `prompt` and `model`.
- **Paths stay under `workspaces/`.** A workspace reference is an id
  (`^[a-z0-9][a-z0-9._-]{0,127}$`), or an absolute path that lies under the
  agent's `workspaces/` after `filepath.Clean` (no `..` element); anything
  else is `outside_workspace` (exit 2, ADR 0046) and nothing runs.
- **The caller is checked at each hop.** The agent refuses an operation
  whose caller isn't `CallerController` (for example `cert.renew` on
  `req`); the controller refuses one on `up` whose caller isn't
  `CallerAgent` (for example `workspace.create` on `up`); the daemon's
  `POST /v1/environments/{name}/call` refuses any operation whose caller
  isn't `CallerController` or that is internal (`ca.update`). The broker's
  ACL (PR 2) already keeps an agent from publishing on another's `req`.
- **The tests that prove it** (each a `t.Run` subtest):
  - `internal/envagent/ops_test.go` `TestAgent_dispatch`: "an unknown
    operation is refused", "an operation the controller may not send is
    refused", "unknown argument keys are refused", "a workspace path outside
    workspaces/ is refused", "a spec that starts with - is refused"; each
    asserts the reply's code and that the runner mock has no calls.
  - `internal/envagent/ops_test.go` `TestCheckWorkspaceRef`: the table of
    references in T3.
  - `internal/environment/call_test.go` `TestController_answer`: "a
    workspace operation on up is refused".
  - `internal/daemon/server_test.go` `TestServer_handleCallEnvironment`:
    "an operation the daemon doesn't forward is refused".

### D7. `ws shell --env` refuses until PR 4. **Owner review.**

A shell is an interactive terminal; without PR 4's `terminal.*` streams
there's nothing to connect it to, and running `ws shell` on the agent
in-process needs a pty the agent would own (that is `terminal.open`). So in
this PR `ws shell --env <name>` checks the name (`environment_unknown` for
an unknown one) and then fails `usage` with `a shell on an environment
needs terminal streams, which arrive with ws's next release; use ws claude
--env <name>`, creating nothing; there is no `shell` kind on the wire.
**Recommended alternative:** drop `--env` from `ws shell` in this PR and add
it in PR 4; then T9 loses its `shell.go` part.

### D8. The CLI reaches the controller through the daemon. **Owner review.**

The controller runs in the daemon (PR 2). The CLI asks it with
`POST /v1/environments/{name}/call` on the daemon's socket, body
`{"op", "args", "request_id", "deadline"}`, answer
`{"reply": {"ok", "result", "error"}}`, through `dialDaemon` (which starts
a transient daemon when none runs, ADR 0068). A daemon that has just
started has no agent connected yet, so the call fails
`environment_offline`. The daemon's HTTP server has no write timeout, so a
10-minute create isn't cut.

### D9. Results on the controller are the agent's results, plus `environment`. **Owner review.**

`ws claude --env <name> --json` prints exactly the object the remote's
`ws claude --json` printed (paths are paths on the remote), with
`"environment": "<name>"` added. A local `ws claude` prints no
`environment`, so `ws claude`'s and `ws machine spawn`'s JSON don't change.
An error from the agent keeps its code and exit code (for example
`repo_not_found`, 3), with `details.environment` added; a code this `ws`
doesn't know becomes `internal` with `details.remote_code`.

## Order

```
T0                      (small; independent of every task)
T1 ─┬─ T3 ─┐
    │      ├─ T4 ─┬─ T5 ──────────────┐
T2 ─┼──────┘      │                   ├─ T10
    │             ├─────────┐         │
    └─ T6 ─┬─ T7 ─┼─────────┼─ T9 ────┤
           └─ T8 ─┘ (T8 also needs T2)└─ T11
```

Edges: T3←T1; T4←T2,T3; T5←T4; T6←T1; T7←T6; T8←T2,T6; T9←T4,T7,T8;
T10←T5,T9; T11←T9.

| Task | Depends on | Parallel with | Size |
| --- | --- | --- | --- |
| T0 Blank lines in function bodies fail `make test` | – | everything | `small` |
| T1 Protocol: operations, typed arguments, request id, protocol number | – | T0, T2 | |
| T2 Records: `environment`, version 4, inheritance | – | T0, T1 | `small` |
| T3 Agent: dispatcher, allow-list, path checks, dedupe, concurrency | T1 | T6 | |
| T4 Agent side in `internal/cli`: `workspace.*` in-process, the snapshot | T2, T3 | T6, T8 | |
| T5 Agent side: `agent.*` and the snapshot's `agents` | T4 | T7, T8 | |
| T6 Controller: `Call`, replies, republish, outdated and offline, snapshot reader | T1 | T3, T4 | |
| T7 Daemon: `POST /v1/environments/{name}/call`, client, host | T6 | T5, T8 | |
| T8 `ws list --env`, `environment`, `environment_status`, `stale` | T2, T6 | T4, T5, T7 | |
| T9 Error codes, `ws claude --env`, `ws shell --env`, parents | T4, T7, T8 | – | |
| T10 Conformance: controller, broker and agent in one process | T5, T9 | T11 | |
| T11 Docs: tools, state, `ws llm`, ADR 0072 accepted | T9 | T10 | |

Heavy tasks (full `internal/cli` tests): T4, T5, T8, T9, T10. Run at most
one of them at a time on an 8 GB pod.

---

## T0. Blank lines in function bodies fail `make test` and `make lint` (`small`)

- **Depends on:** nothing. **Parallel:** with every task.
- **Pinned values touched:** constraints-ops.md › Lint and test rules
  (`Makefile` `lint`).

The owner's rule "no blank lines inside function bodies" isn't checked by
golangci-lint; in PR 2 it cost three review rounds. A Go test with
`go/ast` checks every function body in the module.

### Files

Create:

- `internal/blanklines/blanklines.go`
- `internal/blanklines/blanklines_test.go`

Change: `Makefile` (one line in `lint`).

### `internal/blanklines` (standard library only)

```go
// Package blanklines finds blank lines inside function bodies, which the
// repository's Go conventions forbid. Its test checks the whole module.
package blanklines

// finding is one blank line inside a function body.
type finding struct {
	Line int    // 1-based line of the blank line
	Func string // the function's name; "Recv.Name" for a method
}

// check returns the blank lines inside the bodies of f's function
// declarations, in line order. A line is blank when it is empty after
// strings.TrimSpace. Lines inside a string literal (a raw string spanning
// lines) and lines inside a block comment don't count. Function literals
// are covered by the declaration that holds them. src is the file's source.
func check(fset *token.FileSet, f *ast.File, src []byte) []finding

// checkModule walks root (skipping .git, node_modules, desktop, testdata
// and any directory starting with "."), parses every .go file with
// parser.ParseComments, skips files for which ast.IsGenerated is true, and
// returns the findings keyed by the slash-separated path relative to root.
// A file that doesn't parse is an error naming it.
func checkModule(root string) (map[string][]finding, error)
```

`blanklines_test.go`:

- `TestCheck` with subtests, each on a source string parsed with
  `parser.ParseFile(fset, "x.go", src, parser.ParseComments)`:
  "a blank line in a function body is found" (line and name asserted),
  "a blank line in a method is named Recv.Name", "a blank line in a function
  literal is found", "a blank line between functions isn't", "a blank line
  in a raw string literal isn't", "a blank line inside a block comment
  isn't", "a body without blank lines has no findings".
- `TestCheckModule` with subtests "finds a blank line in a file under a
  temp root" (writes two files with `os.WriteFile` into `t.TempDir()`),
  "skips generated files" (a file starting
  `// Code generated by moq; DO NOT EDIT.`), "skips node_modules and
  testdata".
- `TestRepository`: `checkModule("../..")`; fails listing
  `path:line in Func` for every finding in a file not in `exempt`, and fails
  for every file in `exempt` that has no findings any more ("remove it from
  exempt"), so the list only shrinks.

```go
// exempt are files that had blank lines in function bodies when this test
// was added (2026-11-22, at f65808d). Remove a file once it is fixed.
var exempt = map[string]bool{ ... }
```

`exempt` is a `map` inside the test file; `gochecknoglobals` doesn't run on
tests. Measured on `f65808d` with the same rules: 191 blank lines in these
24 files, every other file clean. Put exactly these in `exempt` (or fix a
file and leave it out):

```
e2e/runner/daemon_test.go          e2e/runner/mcp_test.go
e2e/runner/mcpsmoke_test.go        e2e/runner/realmodel_test.go
e2e/runner/scenarios_test.go       internal/cli/config_test.go
internal/cli/loop_edit_test.go     internal/cli/loop_history_test.go
internal/cli/loop_runner_test.go   internal/cli/loop_test.go
internal/cli/machine_roles_test.go internal/config/edit_test.go
internal/gate/gate.go              internal/gate/gate_test.go
internal/llm/preflight_test.go     internal/loop/edit_test.go
internal/loop/store_test.go        internal/machine/result_test.go
internal/machine/role/set_test.go  internal/machine/store_test.go
internal/review/advisory/advisory_test.go
internal/review/prompt.go          internal/runner/exec_test.go
internal/runner/reresolve_test.go
```

If the implementer's count differs (a file merged since `f65808d`), use what
`checkModule` finds and say so in the report.

`Makefile` `lint`, as the last line of the recipe:

```make
	@# The owner's rule: no blank lines inside function bodies (internal/blanklines).
	go test -count=1 -run '^TestRepository$$' ./internal/blanklines/
```

### Acceptance

- First commit: the tests with a `check` that returns nil; `TestCheck`
  fails. Then the code; then the whole package passes.
- `make test` and `make lint` fail when a blank line is added inside any
  non-exempt function body (check by hand once, then revert).
- No new dependency; `go.mod` unchanged.

### Verify

```
go test -p 2 -count=1 ./internal/blanklines/
make lint
```

---

## T1. Protocol: operations, typed arguments, request id, protocol number

- **Depends on:** nothing. **Parallel:** T0, T2.
- **Pinned values touched:** constraints-ops.md › Wire payloads
  (`Hello`, `Snapshot`, `Request`).

### Files

Create: `internal/envproto/ops.go`, `internal/envproto/ops_test.go`.
Change: `internal/envproto/payloads.go`, `internal/envproto/payloads_test.go`.

`internal/envproto` imports only the standard library (as in PR 2).

### `payloads.go` changes

```go
// Protocol is the newest operation protocol this ws speaks (D5). PR 3 is 1.
const Protocol = 1

// Hello gains, after CAFingerprints:
	Protocol int `json:"protocol"` // 0 from an agent older than PR 3

// Snapshot is what an agent publishes on env/<id>/snapshot (D3).
type Snapshot struct {
	At         time.Time       `json:"at"`
	Protocol   int             `json:"protocol"`
	Workspaces json.RawMessage `json:"workspaces"` // ws list --json --all's array, no unknown dirs
	Agents     []SnapshotAgent `json:"agents"`     // [] until T5
}

// SnapshotAgent is one Claude Code session on the agent's machine.
type SnapshotAgent struct {
	ID        string     `json:"id"`
	SessionID string     `json:"session_id"`
	Name      string     `json:"name"`
	State     string     `json:"state"`
	Status    string     `json:"status"`
	PID       *int       `json:"pid"`
	StartedAt *time.Time `json:"started_at"`
}

// Request gains:
type Request struct {
	Op        string          `json:"op"`
	Args      json.RawMessage `json:"args"`
	RequestID string          `json:"request_id"` // ValidRequestID; "" only from a PR 2 controller
	Deadline  *time.Time      `json:"deadline"`   // null for ca.update
}
```

### `ops.go`

```go
// Operation names (ADR 0073's table, the rows this protocol serves).
const (
	OpWorkspaceCreate = "workspace.create"
	OpWorkspaceList   = "workspace.list"
	OpWorkspaceRm     = "workspace.rm"
	OpWorkspaceRename = "workspace.rename"
	OpWorkspaceHide   = "workspace.hide"
	OpAgentStatus     = "agent.status"
	OpAgentLogs       = "agent.logs"
	OpAgentSend       = "agent.send"
	OpAgentStop       = "agent.stop"
	KindClaude = "claude"
	KindRunner = "runner"
	// Grace is how much longer than an operation's deadline the controller waits.
	Grace = 15 * time.Second
)

// Caller is who may send an operation.
type Caller int

const (
	CallerController Caller = iota + 1 // on env/<id>/req, through the daemon's call endpoint
	CallerAgent                        // on env/<id>/up
	CallerInternal                     // on env/<id>/req, sent only by the controller itself (ca.update)
)

// OpSpec is one operation's rules.
type OpSpec struct {
	Name    string
	Caller  Caller
	Since   int           // the protocol it needs (D5)
	Timeout time.Duration // the message expiry and the deadline's distance
	Changes bool          // the agent republishes its snapshot after it
}

// LookupOp returns name's spec; false for an operation that isn't allowed.
// A switch, not a map, so the table has no package variable.
func LookupOp(name string) (OpSpec, bool)
```

The table `LookupOp` encodes:

| Op | Caller | Since | Timeout | Changes |
| --- | --- | --- | --- | --- |
| `cert.renew` | Agent | 0 | 30 s | no |
| `ca.update` | Internal | 0 | 30 s | no |
| `workspace.create` | Controller | 1 | 10 min | yes |
| `workspace.list` | Controller | 1 | 30 s | no |
| `workspace.rm` | Controller | 1 | 2 min | yes |
| `workspace.rename` | Controller | 1 | 30 s | yes |
| `workspace.hide` | Controller | 1 | 30 s | yes |
| `agent.status` | Controller | 1 | 30 s | no |
| `agent.logs` | Controller | 1 | 30 s | no |
| `agent.send` | Controller | 1 | 90 s | no |
| `agent.stop` | Controller | 1 | 30 s | yes |

Typed arguments (every field always present in JSON; `null` where a pointer
is nil):

```go
type CreateArgs struct {
	Kind     string   `json:"kind"`     // KindClaude or KindRunner
	Specs    []string `json:"specs"`    // [] for an empty workspace
	Name     *string  `json:"name"`
	Parent   *string  `json:"parent"`   // a WorkspaceRef on the environment, or null for none
	Hidden   bool     `json:"hidden"`
	Offline  bool     `json:"offline"`
	Prompt   *string  `json:"prompt"`   // required for KindRunner
	Model    *string  `json:"model"`
	Endpoint *string  `json:"endpoint"` // KindRunner only
}
type ListArgs struct{}                                   // {}
type WorkspaceArgs struct{ Workspace string `json:"workspace"` } // agent.status, agent.stop
type RmArgs struct {
	Workspace    string `json:"workspace"`
	KeepChildren bool   `json:"keep_children"`
}
type RenameArgs struct {
	Workspace string `json:"workspace"`
	Name      string `json:"name"`
}
type HideArgs struct {
	Workspace string `json:"workspace"`
	Hidden    bool   `json:"hidden"` // false: unhide
}
type LogsArgs struct {
	Workspace string `json:"workspace"`
	Tail      int    `json:"tail"` // 0: 200 lines; at most 2000
}
type SendArgs struct {
	Workspace string `json:"workspace"`
	Message   string `json:"message"`
}

// Results of agent.* (workspace.* results are the CLI's own --json objects).
type AgentStatusResult struct {
	Workspace string        `json:"workspace"`
	Session   string        `json:"session"`
	Agent     *SnapshotAgent `json:"agent"` // null when Claude Code doesn't list it
}
type AgentLogsResult struct {
	Workspace string   `json:"workspace"`
	Session   string   `json:"session"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"` // lines were dropped to fit MaxLogBytes
}
type AgentSendResult struct {
	Workspace string `json:"workspace"`
	Session   string `json:"session"`
	Sent      bool   `json:"sent"`
}
type AgentStopResult struct {
	Workspace string `json:"workspace"`
	Session   string `json:"session"`
	Stopped   bool   `json:"stopped"` // false when the session had already ended
}

const (
	MaxPromptBytes  = 64 << 10
	MaxMessageBytes = 16 << 10
	MaxLogBytes     = 192 << 10
	MaxTail         = 2000
	DefaultTail     = 200
)

// ErrArgs is wrapped by DecodeArgs for every refusal; ArgsError carries the
// error code the reply uses.
type ArgsError struct {
	Code    string // "usage", "spec_invalid", "name_invalid" or "outside_workspace"
	Message string
}

func (e *ArgsError) Error() string

// DecodeArgs decodes raw into op's argument struct with unknown keys
// refused and returns a pointer to it (*CreateArgs, *ListArgs, …), after
// Validate. A missing or null raw is {} . Unknown op: ArgsError{usage}.
func DecodeArgs(op string, raw json.RawMessage) (any, error)

// Validate checks the fields of a decoded argument struct (D6) that need
// no file system: Kind; Specs not starting with "-" (spec_invalid); Name
// and RenameArgs.Name 1-64 bytes without control characters (name_invalid);
// Model against ^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$; Prompt 1..MaxPromptBytes,
// required for KindRunner and refused with Endpoint for KindClaude; Message
// 1..MaxMessageBytes; Tail 0..MaxTail; every workspace reference and Parent
// non-empty and without a NUL byte. Paths are checked by the agent (T3).
func Validate(args any) error

// ValidRequestID reports whether id is 16 lowercase hex characters.
func ValidRequestID(id string) bool
```

### Acceptance

- `LookupOp` returns the table above for each name and false for
  `"files.read"`, `""`, `"workspace.create "`.
- `DecodeArgs(OpWorkspaceCreate, {"kind":"claude","specs":["buff-bear@main"],"name":null,"parent":null,"hidden":false,"offline":false,"prompt":"hi","model":null,"endpoint":null})`
  returns a `*CreateArgs`; with an extra key `"x":1` it is an `ArgsError`
  with code `usage`; with `"kind":"shell"` code `usage` and message
  `a shell on an environment needs terminal streams`; with a spec `"-rf"`
  code `spec_invalid`; a runner without a prompt `usage`; a model
  `"--dangerously-skip-permissions"` `usage`.
- `Hello` marshals `"protocol":1`; a PR 2 hello without `protocol`
  unmarshals as 0. `Snapshot` marshals `"agents":[]` for an empty slice set
  by the caller (document: callers set `[]`, never nil).
- `Request` round-trips `request_id` and `deadline`; `deadline` is `null`
  when nil.
- `ValidRequestID`: `"0123456789abcdef"` true; 15 or 17 characters, upper
  case, non-hex false.

### Verify

```
go test -p 2 -count=1 ./internal/envproto/
go test -p 2 -count=1 ./internal/environment/... ./internal/envagent/...
make lint
```

---

## T2. Records: `environment`, version 4, inheritance (`small`)

- **Depends on:** nothing. **Parallel:** T0, T1.
- **Pinned values touched:** constraints-ops.md › Workspace records.

### Files

Change: `internal/workspace/record.go`, `record_test.go`, `create.go`,
`create_test.go`.

### Code

```go
const (
	// environmentVersion is the version of a workspace.toml with an
	// environment, so an older ws leaves a remote's workspace alone (ADR 0072).
	environmentVersion = 4
)

// Workspace gains, after Parent:
	// Environment is the environment this workspace was created for, set
	// only by an environment agent and inherited by children (ADR 0072, D2).
	// Empty for a workspace a person made on this machine.
	Environment string `toml:"environment,omitempty"`

// CreateOptions gains:
	// Environment is the agent's environment name; ignored when the parent
	// has an environment, which the child inherits (D2).
	Environment string
```

- `readRecord` checks `checkVersion(ws.Version, environmentVersion)`.
- `writeRecord`: `Environment != ""` → version 4 (first case); then parent
  3, hidden 2, else 1.
- `Create`: when `opts.Parent != ""`, read the parent's record
  (`readRecord(s.recordPath(opts.Parent))`); the child's `Environment` is
  the parent's when non-empty, else `opts.Environment`. A parent that can't
  be read keeps today's behavior (the caller checked it exists) and uses
  `opts.Environment`.
- `Rename`, `SetHidden`, `SetParent` keep `Environment` (they rewrite the
  record they read; check with a test that it survives each).

### Acceptance

- `record_test.go`: the two "a newer version" subtests now use version 5
  and expect `this ws reads up to 4`; new subtests "a record with an
  environment is version 4" (written file contains `version = 4` and
  `environment = "linux-server"`), "version 4 reads back its environment",
  "an environment wins over a parent for the version".
- `create_test.go`: "a child inherits its parent's environment" (parent
  record with `environment`, `opts.Environment` empty or different → the
  parent's); "a root gets opts.Environment"; "a child of a parent without
  one gets opts.Environment"; "no environment, no key" (file has no
  `environment` line and is version 1).
- `TestCheckVersion` unchanged.

### Verify

```
go test -p 2 -count=1 ./internal/workspace/
make lint
```

---

## T3. Agent: dispatcher, allow-list, path checks, dedupe, concurrency

- **Depends on:** T1. **Parallel:** T6.
- **Pinned values touched:** constraints-ops.md › `envagent` signatures,
  Wire payloads (`Hello.Protocol`, snapshot `protocol`/`agents`).

### Files

Create: `internal/envagent/ops.go`, `ops_test.go`, `dedupe.go`,
`dedupe_test.go`.
Change: `internal/envagent/interfaces.go` (+ regenerate `moq_test.go`),
`requests.go`, `requests_test.go`, `agent.go`, `agent_test.go`,
`internal/cli/env_agent.go` (only the `NewAgent` call, passing
`envagent.NoOperations{}` and setting `opts.WorkspacesRoot`),
`internal/mqttbroker/conformance_test.go` (its `NewAgent` call gains
`envagent.NoOperations{}`).

### Signatures

```go
// interfaces.go: append operationRunner to the go:generate line.

// operationRunner runs one allowed, validated operation with ws's own code
// (internal/cli implements it, D1). args is the pointer DecodeArgs
// returned. result is the reply's result; a *envproto.ReplyError is the
// failure to reply with (its code is a ws error code); any other error is
// replied as "internal".
type operationRunner interface {
	Run(ctx context.Context, op string, args any) (result json.RawMessage, err error)
}

// NoOperations answers every operation with usage "this agent runs no
// operations"; ws env agent run uses it until T4.
type NoOperations struct{}

func (NoOperations) Run(context.Context, string, any) (json.RawMessage, error)

// Options gains:
	WorkspacesRoot string // the agent's CONFIG_DIR/workspaces, for CheckWorkspaceRef
	MaxOps         int    // operations run at once; DefaultOptions: 4

// NewAgent gains ops after claude:
func NewAgent(log *slog.Logger, files *Files, client mqttClient, snapshots snapshotSource, claude claudeProbe, ops operationRunner, random io.Reader, opts Options) *Agent

// ops.go

// dispatch handles a request from env/<id>/req that isn't a reply: decodes
// the envelope, looks the op up, checks its caller is CallerController or
// CallerInternal, decodes and validates the arguments, checks workspace
// references with CheckWorkspaceRef, then either replies at once (a
// refusal, or ca.update, which runs on the session loop as today) or hands
// the operation to runOp on its own goroutine. It never blocks the session
// loop on an operation.
func (a *Agent) dispatch(ctx context.Context, id *Identity, msg *envproto.Message)

// runOp waits for one of MaxOps slots (until the request's deadline, else
// replies internal "the agent was busy until the deadline"), runs the
// operation under context.WithDeadline(ctx, deadline) through the dedupe
// store, replies on msg's response topic, and when the op's spec has
// Changes, signals a.changed (buffered 1, non-blocking send).
func (a *Agent) runOp(ctx context.Context, id *Identity, msg *envproto.Message, req *envproto.Request, spec envproto.OpSpec, args any)

// CheckWorkspaceRef returns nil for an id (^[a-z0-9][a-z0-9._-]{0,127}$),
// or for an absolute path under root after filepath.Clean that is not root
// itself; else an *envproto.ReplyError with code outside_workspace naming
// ref. It doesn't touch the file system.
func CheckWorkspaceRef(root, ref string) error

// dedupe.go

// requests remembers operations by request id (D4).
type requests struct {
	mu      sync.Mutex
	entries map[string]*requestEntry
	order   []string // finished ids, oldest first
	keep    time.Duration // 15 min
	max     int           // 512
	now     func() time.Time
}
type requestEntry struct {
	done     chan struct{}
	reply    []byte
	finished time.Time
}

func newRequests(now func() time.Time) *requests

// do runs fn once per id and returns its reply: a duplicate of a running id
// waits for it (or ctx), a finished one returns the stored reply. An empty
// id isn't remembered. Entries finished more than keep ago, and the oldest
// beyond max, are dropped on each call.
func (r *requests) do(ctx context.Context, id string, fn func() []byte) ([]byte, error)
```

`agent.go` changes: `Agent` gains `ops operationRunner`, `requests
*requests`, `slots chan struct{}` (size `MaxOps`), `changed chan struct{}`
(size 1); `serve`'s `select` gains `case <-a.changed:
a.refreshSnapshot(ctx, id, true)`. `refreshSnapshot` sets `Protocol:
envproto.Protocol` and `Agents: []envproto.SnapshotAgent{}` (T5 fills it),
and skips publishing with a warning `snapshot too large` (`"bytes"`) when
the payload exceeds `envproto.MaxRequestBytes`. `hello` sets `Protocol:
envproto.Protocol`. `requests.go`'s `answer` is replaced by `dispatch`
(keep `updateRoots` for `ca.update`); a reply bigger than
`envproto.MaxRequestBytes` is replaced by `ErrorReply("internal", "the
result is too large to send")`. `reply` and the op goroutines must not
touch `a.pending` or `a.workspaces` (session-loop state).

### Acceptance (`ops_test.go`, `dedupe_test.go`, moq `operationRunnerMock`)

- `TestAgent_dispatch` subtests (D6's list, plus): "workspace.list runs
  the runner and replies ok", "a runner ReplyError becomes the reply's
  error", "a plain runner error replies internal", "ca.update still updates
  the roots", "a reply too large is replaced", "an op that changes
  workspaces republishes the snapshot" (envtest broker or the
  `mqttClientMock`: a second snapshot publish), "a slow op doesn't block
  the next request" (runner blocks on a channel; a second request's reply
  arrives first).
- `TestCheckWorkspaceRef` table (root `/cfg/workspaces`): `fix-login` ok;
  `/cfg/workspaces/fix-login` ok; `/cfg/workspaces/a/../fix-login` ok;
  `../etc`, `a/b`, `/etc/passwd`, `/cfg/workspaces`, `/cfg/workspaces/..`,
  `/cfg/workspaces-evil/x`, `Fix Login`, `""` → `outside_workspace`.
- `TestRequests_do`: "runs fn once for two concurrent calls with one id",
  "returns the stored reply after it finished", "forgets ids after keep",
  "drops the oldest finished beyond max", "an empty id isn't remembered",
  "a waiting duplicate returns ctx's error when ctx ends".
- The existing agent tests pass with `NoOperations{}`; the snapshot test
  now expects `"protocol":1,"agents":[]`.

### Verify

```
go generate ./internal/envagent/
go test -p 2 -count=1 -race ./internal/envagent/
go test -p 2 -count=1 -run 'EnvAgent|Agent' ./internal/cli/
go test -p 2 -count=1 ./internal/mqttbroker/
make lint
```

---

## T4. Agent side in `internal/cli`: `workspace.*` in-process, the snapshot

- **Depends on:** T2, T3. **Parallel:** T6, T8.
- **Pinned values touched:** constraints-ops.md › `internal/cli` agent
  wiring.

### Files

Create: `internal/cli/env_agent_ops.go`, `internal/cli/env_agent_ops_test.go`.
Change: `internal/cli/env_agent.go` (wire `agentOperations`, snapshot
`all=true` and no unknown dirs), `internal/cli/env_agent_test.go`,
`internal/cli/app.go` (one field), `internal/cli/claude.go` and
`internal/cli/runner_start.go` (pass `Environment: a.environment` in
`workspace.CreateOptions`).

### Code

```go
// App gains:
	// environment is the enrolled environment's name when this App runs an
	// agent's operation (D2); "" otherwise. ws claude stamps it on records.
	environment string

// agentOperations runs the controller's operations on this machine (D1).
type agentOperations struct {
	app *App // the agent's App: paths, config, environ, version
	// newApp builds the App one CLI operation runs in: cli.New with stdin
	// empty and the given streams, environment set to name.
	newApp func(stdout, stderr io.Writer) *App
	name   string // the enrolled environment's name
}

func newAgentOperations(a *App, name string) *agentOperations

// Run implements envagent's operationRunner.
func (o *agentOperations) Run(ctx context.Context, op string, args any) (json.RawMessage, error)

// runCLI runs argv in a new App and returns its stdout's one JSON object.
// A non-zero exit with a {"error": {...}} line on stderr is that error as
// *envproto.ReplyError; anything else is internal with stderr's first line.
func (o *agentOperations) runCLI(ctx context.Context, argv []string) (json.RawMessage, error)

// createArgv, rmArgv, renameArgv, hideArgv build the command lines below.
func createArgv(configDir string, args *envproto.CreateArgs) []string
```

Command lines (always prefixed `--json --config-dir <a.paths.Root>`):

| Op | argv after the prefix |
| --- | --- |
| `workspace.create`, `claude` | `claude [--name N] [--hidden] [--offline] --parent <P or none> <specs…> -- [<prompt>] [--model M]` |
| `workspace.create`, `runner` | `claude --runner ws [--endpoint E] [--name N] [--hidden] [--offline] --parent <P or none> <specs…> -- <prompt> [--model M]` |
| `workspace.list` | `list --all` |
| `workspace.rm` | `rm [--keep-children] <W>` |
| `workspace.rename` | `rename <W> <name>` |
| `workspace.hide` | `hide <W>` or `unhide <W>` |

`--parent none` is passed whenever `Parent` is null, so the agent's own
working directory never chooses a parent. Before building `workspace.create`,
each spec is parsed with `repo.ParseSpec` (`spec_invalid` on error). With
`claude` kind and no prompt and no model, nothing follows `--`, and `--` is
left out.

`runEnvAgentRun` loads the identity first (`envagent.NewFiles(...).Load()`)
to get its name, then `envagent.NewAgent(log, files, client, agentSnapshots{app: a}, probe, newAgentOperations(a, id.Name), rand.Reader, opts)` with
`opts.WorkspacesRoot = a.paths.Workspaces`. Not enrolled: as today
(`environment_unknown`).

`agentSnapshots.Workspaces` uses `visibleEntries(..., true, raised)` and
passes `nil` for the unknown directories.

### Acceptance (tests build `newApp` from `newMockedApp()` with its stdout and stderr replaced by the buffers `runCLI` passes)

- `TestCreateArgv` table: a claude agent with a prompt and model; with no
  prompt (no `--`); a runner with endpoint; a parent; hidden and offline.
  Exact slices asserted.
- `TestAgentOperations_Run`: "workspace.list returns ws list --json --all's
  object" (mock `lifetime.List`; `JSONEq` to what `runList` prints for the
  same entries with `all`), "workspace.rename returns renameJSON",
  "workspace.hide with hidden false runs unhide", "workspace.rm passes
  --keep-children", "a CLI error comes back with its code" (mock
  `workspaces.Find` → `workspace.ErrNotFound`: `ReplyError{Code:
  "workspace_not_found"}`), "workspace.create stamps the environment" (the
  `CreateOptions` the `workspaceStoreMock` receives has `Environment:
  "linux-server"` and `Parent: ""` for `--parent none`), "a spec that
  doesn't parse is spec_invalid and runs nothing".
- `agentSnapshots` test: a hidden workspace is included; an unknown
  directory isn't.
- `ws claude` locally still passes `Environment: ""`.

### Verify

```
go test -p 2 -count=1 -run 'AgentOperations|CreateArgv|EnvAgent|agentSnapshots|Claude' ./internal/cli/
go test -p 2 -count=1 ./internal/cli/
make lint
```

---

## T5. Agent side: `agent.*` and the snapshot's `agents`

- **Depends on:** T4. **Parallel:** T7, T8.
- **Pinned values touched:** constraints-ops.md › `envagent` signatures
  (`snapshotSource`).

### Files

Create: `internal/cli/env_agent_sessions.go`, `env_agent_sessions_test.go`.
Change: `internal/cli/env_agent_ops.go` (route `agent.*`),
`internal/cli/env_agent.go` (`agentSnapshots.Agents`),
`internal/envagent/interfaces.go` (+ `moq_test.go`), `agent.go`,
`agent_test.go` (its `testSnapshots` gains `Agents`),
`internal/mqttbroker/conformance_test.go` (`emptySnapshots` gains
`Agents` returning `[]`).

### Code

```go
// envagent/interfaces.go: snapshotSource gains
	// Agents is this machine's Claude Code sessions, [] when there are none.
	Agents(ctx context.Context) ([]envproto.SnapshotAgent, error)

// refreshSnapshot: a failing Agents is logged ("snapshot agents not read")
// and published as []; the workspaces still go out.

// cli/env_agent_sessions.go

// agentSession resolves a workspace reference to its workspace and Claude
// Code session: workspaces.Find(ref), then lifetime.List for its entry.
// No session, a shell, or a ws runner session: usage "<ref> has no Claude
// Code agent". Not found: workspace_not_found.
func (o *agentOperations) agentSession(ctx context.Context, ref string) (workspace.Workspace, *lifetime.Session, error)

func (o *agentOperations) agentStatus(ctx context.Context, args *envproto.WorkspaceArgs) (json.RawMessage, error)
// agentLogs keeps the last Tail lines (DefaultTail for 0) of claude.Logs,
// then drops lines from the front until the JSON fits MaxLogBytes, setting
// truncated.
func (o *agentOperations) agentLogs(ctx context.Context, args *envproto.LogsArgs) (json.RawMessage, error)
// agentSend finds the claude.Session in Agents(ctx, ListOptions{All: true})
// and calls claude.Send; ErrNotDelivered is claude_failed.
func (o *agentOperations) agentSend(ctx context.Context, args *envproto.SendArgs) (json.RawMessage, error)
// agentStop calls claude.Stop; claude.ErrNoSession is stopped: false.
func (o *agentOperations) agentStop(ctx context.Context, args *envproto.WorkspaceArgs) (json.RawMessage, error)

// snapshotAgent converts a claude.Session; PID 0 and a zero StartedAt are null.
func snapshotAgent(s *claude.Session) envproto.SnapshotAgent
```

These use the agent App's `claude` (`claudeClient`) and stores, not a
sub-App: they aren't CLI commands (D1).

### Acceptance

- `TestAgentOperations_agentStatus|agentLogs|agentSend|agentStop` with
  `claudeClientMock`: the happy path for each; "a shell workspace has no
  Claude Code agent"; logs: 3000 lines with tail 0 → 200 lines; tail 5 → 5;
  a 300 KiB log → `truncated: true` and the JSON is at most
  `MaxLogBytes`; send: the message reaches `SendFunc` with the session from
  `AgentsFunc`; stop: `ErrNoSession` → `stopped: false`.
- `agentSnapshots.Agents` maps two sessions; a `claude` failure is an error.
- envagent: "the snapshot carries the agents"; "a failing Agents still
  publishes the workspaces with agents []".

### Verify

```
go generate ./internal/envagent/
go test -p 2 -count=1 ./internal/envagent/
go test -p 2 -count=1 -run 'AgentOperations|agentSnapshots|EnvAgent' ./internal/cli/
go test -p 2 -count=1 ./internal/mqttbroker/
make lint
```

---

## T6. Controller: `Call`, replies, republish, outdated and offline, snapshot reader

- **Depends on:** T1. **Parallel:** T3, T4.
- **Pinned values touched:** constraints-ops.md › Controller.

### Files

Create: `internal/environment/call.go`, `call_test.go`,
`internal/environment/snapshot.go`, `snapshot_test.go`.
Change: `internal/environment/controller.go` (`onRes`, `onStatus`,
`answer`, `sendCAUpdate`, the snapshot type), `controller_test.go`,
`internal/environment/errors.go`.

### Signatures

```go
// errors.go gains
var (
	// ErrOffline is returned when an environment isn't online, the controller
	// isn't listening, or a call got no reply in time.
	ErrOffline = errors.New("the environment is offline")
	// ErrNotForwarded is returned for an operation the controller doesn't send for a caller.
	ErrNotForwarded = errors.New("operation not forwarded")
)

// OutdatedError is an environment whose agent speaks too old a protocol for an op.
type OutdatedError struct {
	Environment string
	Op          string
	WSVersion   string
	Protocol    int
	Need        int
}

func (e *OutdatedError) Error() string

// OfflineError wraps ErrOffline with why: "offline", "not_listening" or "timeout".
type OfflineError struct {
	Environment string
	Reason      string
	LastSeen    *time.Time
	RequestID   string
	Op          string
}

func (e *OfflineError) Error() string
func (e *OfflineError) Unwrap() error { return ErrOffline }

// call.go

// CallRequest is one operation the CLI asks the controller to send.
type CallRequest struct {
	Op        string
	Args      json.RawMessage
	RequestID string    // envproto.ValidRequestID
	Deadline  time.Time // now + the op's Timeout, set by the caller
}

// Call sends req to the environment called name and waits for its reply
// until req.Deadline + envproto.Grace or ctx. In order: ErrUnknown for no
// enrolled environment called name; usage-style ErrNotForwarded (wrapped
// with the op) unless LookupOp(op).Caller is CallerController; an
// *OfflineError{Reason: "not_listening"} when the controller isn't
// listening, {Reason: "offline"} when its presence isn't online; an
// *OutdatedError when the hello's Protocol is below the op's Since; then
// it publishes {op, args, request_id, deadline} on env/<id>/req (QoS 1,
// ResponseTopic env/<id>/res/<request_id>, CorrelationData request_id,
// Expiry until the deadline) and waits. If the environment goes offline
// and online again while it waits, it republishes once (D4). No reply in
// time: *OfflineError{Reason: "timeout"}. The returned Reply is the
// agent's, ok or not.
func (c *Controller) Call(ctx context.Context, name string, req CallRequest) (envproto.Reply, error)

// snapshot.go

// SnapshotFile is state/.environments/<name>/snapshot.json (moved from
// controller.go's snapshotRecord and exported).
type SnapshotFile struct {
	Version    int             `json:"version"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	ReceivedAt string          `json:"received_at"`
	Snapshot   json.RawMessage `json:"snapshot"`
}

// Snapshot reads and decodes name's snapshot file and the envproto.Snapshot
// inside it (missing protocol 0, missing agents []). fs.ErrNotExist when
// there is none.
func (s *Store) Snapshot(name string) (SnapshotFile, envproto.Snapshot, error)
```

`Controller` gains `calls map[string]*pendingCall` (by request id, under
`c.mu`), where `pendingCall{env string, msg envproto.Message, reply chan
envproto.Reply, republished bool}`. `onRes` first delivers a reply whose
correlation data is a pending call's id (non-blocking send to a buffer-1
channel), and only otherwise logs a `ca.update` acknowledgement as today.
`onStatus`, on an online hello after an offline status for the same id,
republishes each of that environment's pending calls that hasn't been
republished. `answer` (the `up` handler) refuses with `usage`
`"<op> isn't an operation an agent may send"` any op whose
`LookupOp(op).Caller` isn't `CallerAgent`. `sendCAUpdate` fills
`RequestID` with its existing request id and `Deadline: nil`.

### Acceptance (`envtest.Broker`, a fixed clock, an agent faked with an `envtest.Client` that answers requests)

- `TestController_Call`: "an unknown name is ErrUnknown", "an
  agent-only op is ErrNotForwarded", "not listening is offline/not_listening",
  "an offline environment is offline/offline and nothing is published",
  "a protocol-0 agent is OutdatedError for workspace.list" (`Need` 1,
  `WSVersion` from the hello), "the agent's ok reply is returned", "the
  agent's error reply is returned as a Reply, not an error", "no reply by
  deadline+grace is offline/timeout" (short deadline), "a reconnect
  republishes the same request id once", "a reply for an unknown request
  id is ignored", "ctx ending returns its error and forgets the call".
- `TestController_answer`: "a workspace operation on up is refused";
  `cert.renew` still renews.
- `TestStore_Snapshot`: round-trip of what `onSnapshot` writes; a PR 2
  snapshot without `protocol` and `agents`; a missing file is
  `fs.ErrNotExist`.
- The existing controller tests pass (the ca.update payload now has
  `"request_id"` and `"deadline":null`).

### Verify

```
go test -p 2 -count=1 -race ./internal/environment/...
make lint
```

---

## T7. Daemon: `POST /v1/environments/{name}/call`, client, host

- **Depends on:** T6. **Parallel:** T5, T8.
- **Pinned values touched:** constraints-ops.md › Daemon.

### Files

Change: `internal/daemon/interfaces.go` (+ `moq_test.go`),
`environments.go`, `environments_test.go`, `server.go`, `server_test.go`,
`client.go`, `client_test.go`, `internal/cli/env_daemon.go`,
`internal/cli/env_daemon_test.go`.

### Code

```go
// EnvironmentHost gains
	// Call sends one operation to an environment and waits for its reply (D8).
	Call(ctx context.Context, name string, req CallRequest) (CallReply, error)

// CallRequest is POST /v1/environments/{name}/call's body.
type CallRequest struct {
	Op        string          `json:"op"`
	Args      json.RawMessage `json:"args"`
	RequestID string          `json:"request_id"`
	Deadline  time.Time       `json:"deadline"`
}

// CallReply is its 200 answer: the agent's reply as it sent it.
type CallReply struct {
	Reply json.RawMessage `json:"reply"`
}

// Errors the host returns, mapped by the server (writeFailure):
var (
	ErrEnvironmentUnknown  = errors.New("no such environment")          // 404 environment_unknown
	ErrEnvironmentOffline  = errors.New("the environment is offline")   // 503 environment_offline
	ErrEnvironmentOutdated = errors.New("the environment's ws is too old") // 409 environment_outdated
	ErrNotForwarded        = errors.New("operation not forwarded")      // 400 usage
)

// HostError carries one of those sentinels and the details the error
// envelope reports.
type HostError struct {
	Err     error
	Message string
	Details map[string]any
}

func (e *HostError) Error() string
func (e *HostError) Unwrap() error

// NopEnvironments.Call is HostError{ErrEnvironmentOffline, reason
// "not_listening"}.

// server.go: mux.HandleFunc("POST /v1/environments/{name}/call", s.handleCallEnvironment)
// handleCallEnvironment reads the body (readBody, at most
// envproto.MaxRequestBytes+4 KiB), refuses an invalid request_id or a zero
// deadline with 400 usage, calls envs.Call with r.Context(), and writes
// 200 CallReply or the mapped error with its details.

// client.go
func (c *Client) CallEnvironment(ctx context.Context, name string, req CallRequest) (CallReply, error)
```

`envHost.Call` (cli) converts to `environment.CallRequest`, calls
`ctrl.Call`, and maps `ErrUnknown`, `*OfflineError`, `*OutdatedError` and
`ErrNotForwarded` to `HostError`s with details: offline
`{"environment", "reason", "last_seen", "request_id", "op"}`; outdated
`{"environment", "op", "ws_version", "protocol", "need_protocol"}`.

### Acceptance

- `TestServer_handleCallEnvironment` (with `EnvironmentHostMock`): "a reply
  is passed through unchanged", "an unknown environment is 404
  environment_unknown", "offline is 503 with its details", "outdated is 409
  with its details", "an operation the daemon doesn't forward is refused"
  (host returns `ErrNotForwarded` → 400 usage), "a bad request id is 400
  usage and the host isn't called", "the handler's context is the
  request's".
- `TestClient_CallEnvironment`: 200 decodes; 503 is a `*RemoteError` with
  code `environment_offline` and details.
- `TestNopEnvironments`: `Call` is offline/not_listening.
- cli `envHost.Call` mapping test with a real `environment.Controller` not
  listening → `HostError` offline/not_listening.
- `internal/cli/env_daemon_test.go`'s hand-written `reloadRecorder` gains a
  `Call` that returns `daemon.ErrEnvironmentOffline`, so the cli tests
  compile.

### Verify

```
go generate ./internal/daemon/
go test -p 2 -count=1 ./internal/daemon/
go test -p 2 -count=1 -run 'Env|Daemon' ./internal/cli/
make lint
```

---

## T8. `ws list --env`, `environment`, `environment_status`, `stale`

- **Depends on:** T2, T6. **Parallel:** T4, T5, T7.
- **Pinned values touched:** constraints-ops.md › `ws list` JSON and text,
  Guides (`ws-list.md`).

### Files

Create: `internal/cli/list_env.go`, `internal/cli/list_env_test.go`.
Change: `internal/cli/list.go`, `list_test.go`,
`internal/cli/guides/ws-list.md`.

### Code

```go
// listOptions gains
	envs []string // --env, repeatable

// cmd.Flags().StringArrayVar(&opts.envs, "env", nil,
//   "only these environments (local or a name from ws env list); default: local")

// listEntryJSON is one ws list entry: a workspace and where it runs. Only
// ws list uses it; ws machine children keeps listWorkspaceJSON.
type listEntryJSON struct {
	listWorkspaceJSON
	Environment       string `json:"environment"`        // "local" or a name
	EnvironmentStatus string `json:"environment_status"` // "online" or "offline"
	Stale             bool   `json:"stale"`
}

// listJSON.Workspaces becomes []listEntryJSON.

// remoteListing is one environment's entries as ws list shows them.
type remoteListing struct {
	name    string
	status  string // online, offline
	entries []listEntryJSON
}

// remoteEntries reads name's snapshot (Store.Snapshot), drops entries with a
// null id, sets environment to name, environment_status from presence and
// stale per D3 (not online, or received_at older than 90 s by a.now()). No
// snapshot: no entries, no error.
func (a *App) remoteEntries(name string, presence map[string]string) (remoteListing, error)

// environmentPresence asks the running daemon (daemon.Dial, no autostart,
// 2 s timeout) for each environment's status by name. No daemon, or any
// error: an empty map (every environment offline).
func (a *App) environmentPresence(ctx context.Context) map[string]string

// selectEnvironments resolves --env: [] → ["local"]; each name must be
// "local" or an environment in envStore().List() (environment_unknown,
// exit 3, naming it); duplicates are dropped; order is as given.
func (a *App) selectEnvironments(names []string) ([]string, error)
```

Rules:

- Local entries are `environment: "local"` (or the record's `environment`
  when it has one: this machine is itself an agent; D2),
  `environment_status: "online"`, `stale: false`.
- `--repo` filters remote entries by their `repos[].id` the same way
  (`repoMatches` on the id string); `--parent` with exactly one `--env`
  that isn't local filters by that environment's `parent` field; with
  more than one, `usage` "--parent takes one --env".
- Hidden remote entries are left out without `-a` and counted in
  `hidden_count`, except loop runs with attention (as locally).
- `-q` prints ids as today.
- Text table: when any listed entry isn't local, an `ENV` column follows
  `ID` (`linux-server`, or `linux-server (offline)` when stale); otherwise
  the table is byte-for-byte today's. Trees are ordered within each
  environment (`treeOrder` per environment, local first, then
  environments in `--env` order).

### Acceptance

- `list_test.go`'s `--json` golden gains `"environment":"local",
  "environment_status":"online","stale":false` on every entry, including
  the unknown directory; the other goldens are unchanged.
- `TestApp_runList` new subtests: "--env linux-server lists the snapshot's
  entries" (snapshot file written in a temp `CONFIG_DIR` with
  `environment.SnapshotFile`), "an offline environment's entries are
  stale", "an online environment with an old snapshot is stale", "no daemon
  means offline", "--env unknown is environment_unknown", "--env local
  --env linux-server lists both, local first", "hidden remote entries are
  counted", "a remote entry with a null id is dropped", "the ENV column
  appears only with a remote entry".
- `guides/ws-list.md` documents `--env`, the three fields and the stale
  rule (the guide test requires every local flag and the JSON block). Its
  `## Exit codes` may name `environment_unknown` (declared in PR 2) but not
  the three codes T9 declares: the guide test refuses a code that isn't
  declared yet.

### Verify

```
go test -p 2 -count=1 -run 'List|GuideTopics' ./internal/cli/
go test -p 2 -count=1 ./internal/cli/
make lint
```

---

## T9. Error codes, `ws claude --env`, `ws shell --env`, parents

- **Depends on:** T4, T7, T8. **Parallel:** none.
- **Pinned values touched:** constraints-ops.md › Error codes, Guides
  (`ws-claude.md`, `ws-shell.md`, `ws.md`), `ws claude` JSON.

### Files

Create: `internal/cli/env_call.go`, `internal/cli/env_call_test.go`.
Change: `internal/cli/errors.go`, `errors_test.go`, `claude.go`,
`claude_test.go`, `shell.go`, `shell_test.go`, `interfaces.go` (+
`moq_test.go`), `app.go` (the dial seam), `internal/cli/guides/ws.md`,
`guides/ws-claude.md`, `guides/ws-shell.md`,
`docs/adrs/0016-output-contract.md` (`Status:` line only).

### Error codes

```go
	// From ADR 0072 (PR 3).
	codeEnvironmentOffline  errorCode = "environment_offline"  // exit 1
	codeEnvironmentOutdated errorCode = "environment_outdated" // exit 1
	codeEnvironmentMismatch errorCode = "environment_mismatch" // exit 4
```

Add each to its `case` in `exitCode()`, to `TestErrorCode_exitCode`, and
to the matching row of `guides/ws.md`'s `## Output and exit codes` table.

### Code

```go
// interfaces.go gains, and the go:generate line lists it:
// environmentCaller is the daemon's call endpoint (daemon.Client).
type environmentCaller interface {
	CallEnvironment(ctx context.Context, name string, req daemon.CallRequest) (daemon.CallReply, error)
}

// App gains: dialEnvironments func(ctx context.Context) (environmentCaller, error)
// (default: dialDaemon's client, which starts a transient daemon, ADR 0068).

// claudeOptions gains env string; the flag:
// cmd.Flags().StringVar(&opts.env, "env", "", "run it on this environment (ws env list); default: local, or the parent's")

// callEnvironment sends op with args to name with a new request id and
// deadline (now + LookupOp(op).Timeout), and returns the reply's result.
// daemon errors map to codes: environment_unknown, environment_offline,
// environment_outdated, usage; with their details. An agent's error reply
// keeps its code (an unknown code is internal with details.remote_code)
// and gains details.environment (D9).
func (a *App) callEnvironment(ctx context.Context, name, op string, args any) (json.RawMessage, error)

// newRequestID is 16 lowercase hex characters from crypto/rand.
func newRequestID() (string, error)

// remoteParent applies D2's rules to --env and --parent and returns the
// environment to run on ("" for local) and the parent to pass (an id on
// that environment, or "" for none). It reads snapshots with remoteEntries.
func (a *App) remoteParent(ctx context.Context, env, parent string) (runOn, parentID string, err error)

// runRemoteClaude builds envproto.CreateArgs from opts (kind runner for
// --runner ws, prompt and --model from after "--"; any other argument
// after "--" is usage "only a prompt and --model can be passed to an
// agent on an environment"), calls workspace.create, and prints the
// agent's result with "environment" added (D9): --json the object, else
// "<id>  <session>  <path>  (on <name>)". It never attaches.
func (a *App) runRemoteClaude(ctx context.Context, opts *claudeOptions, env, parent string) error
```

`runClaude` starts with `remoteParent`; a non-empty `runOn` goes to
`runRemoteClaude`, else everything is as today. `--env local` is the same
as no `--env`, except that a remote `--parent` is then
`environment_mismatch`.

`ws shell --env <name>` (D7): `selectEnvironments([]string{name})` for
`environment_unknown`, then `usage` with D7's message. `--env local` runs
the local shell as today.

### Acceptance

- `TestErrorCode_exitCode` and `TestGuideTopics` pass with the three codes.
- `TestApp_runClaude` new subtests (moq `environmentCallerMock`, snapshot
  files in a temp `CONFIG_DIR`): "--env sends workspace.create with the
  typed args" (assert the `CallRequest`'s op, a valid request id, the
  deadline 10 min ahead of the fixed clock, and the args JSON), "--json
  prints the agent's result with environment", "an agent error keeps its
  code and exit code" (`repo_not_found` → exit 3), "an unknown remote code
  is internal", "offline maps to environment_offline with details",
  "outdated maps to environment_outdated", "--env with a local --parent is
  environment_mismatch and sends nothing", "--parent found on one
  environment runs there without --env", "--parent found on two is usage",
  "the default parent is dropped with a warning under --env", "--runner ws
  --env sends kind runner with the prompt", "arguments other than a prompt
  and --model after -- are usage".
- `TestApp_runShell`: "--env unknown is environment_unknown"; "--env name
  is usage and creates nothing".
- Guides: `ws-claude.md` documents `--env`, the `environment` field and the
  three codes in `## Exit codes`; `ws-shell.md` documents `--env`.
- ADR 0016's `Status:` line gains "`environment_offline`,
  `environment_outdated` and `environment_mismatch` added by
  [0072](0072-remote-environments.md) (PR 3)".

### Verify

```
go generate ./internal/cli/
go test -p 2 -count=1 -run 'ErrorCode|GuideTopics|Claude|Shell|EnvCall|RemoteParent' ./internal/cli/
go test -p 2 -count=1 ./internal/cli/
make lint
```

---

## T10. Conformance: controller, broker and agent in one process

- **Depends on:** T5, T9. **Parallel:** T11.
- **Pinned values touched:** constraints-ops.md › What the pod can't run.

ADR 0073's conformance: for each operation, the agent's result equals the
CLI's `--json` for the same input; an unknown operation and a path outside
`workspaces/` are refused; `claude: missing` fails a Claude start with
`claude_missing`. ADR 0072's: a record with `environment` is version 4;
`--env` conflicting with a parent is refused; `ws list` marks an offline
environment's entries stale; no command with an offline environment touches
`local`.

### Files

Create: `internal/cli/env_ops_conformance_test.go` (package `cli`, no build
tag: it runs in `make test`).

### Setup

Two `CONFIG_DIR`s in `t.TempDir()`: the controller's and the agent's. A real
`environment.Store` and `Controller` on an `envtest.Broker` (no network),
the agent enrolled with the store's code through `envagent.Enroller`
against the controller's enrollment listener on `127.0.0.1:0` (as PR 2's
tests do), then a real `envagent.Agent` with an `envtest.Client`, and
`agentOperations` whose `newApp` returns real Apps (`New(...)`, real
config loader, real workspace store and lifetime manager from `newStores`)
on the agent's `CONFIG_DIR`, with only `app.claude` replaced by one shared
`claudeClientMock` (`Check` ok, `Start` returns a fixed `claude.Started`,
`Agents` lists the started sessions, `Logs`, `Stop`). Workspaces are created
with no specs (an empty workspace, which `ws claude` allows), so no git and
no network are needed. The controller-side App's `dialEnvironments`
returns an adapter that calls `controller.Call` directly (no daemon
socket), and its `CONFIG_DIR` is the controller's.

### Acceptance (one `t.Run` each)

- "workspace.list equals ws list --json --all on the agent".
- "workspace.create (claude) equals ws claude --json, plus environment",
  and the agent's record is version 4 with `environment`.
- "a ws machine spawn child on the agent inherits environment" (create
  through the agent, then run `machine spawn` with the agent's App from
  inside the new workspace's directory, via the `getwd` seam).
- "workspace.rename", "workspace.hide", "workspace.rm" each equal the CLI.
- "agent.status, agent.logs, agent.stop" against the fake claude.
- "claude missing fails workspace.create with claude_missing" (`Check`
  returns `claude.ErrNotInstalled`).
- "an unknown operation and a path outside workspaces/ are refused".
- "ws list --env shows the new workspace after the operation" (the snapshot
  republished at once).
- "an offline environment: ws claude --env fails environment_offline and
  the local workspaces/ and state/ are unchanged" (compare directory
  listings before and after).
- "ws list --env marks the entries stale once the agent is gone".

### Verify

```
go test -p 2 -count=1 -run 'Conformance' ./internal/cli/
go test -p 2 -count=1 ./internal/cli/ ./internal/envagent/ ./internal/environment/...
make lint
```

Helpers this test needs go in `env_ops_conformance_test.go` only. If the
setup can't be built without changing non-test code, stop and report what
is missing instead of changing it.

---

## T11. Docs: tools, state, `ws llm`, ADR 0072 accepted

- **Depends on:** T9. **Parallel:** T10.
- **Pinned values touched:** constraints-ops.md › Docs with lists, ADRs.

### Files

Change: `docs/tools/ws-claude.md`, `docs/tools/ws-shell.md`,
`docs/tools/ws-list.md`, `docs/tools/ws-env.md`, `docs/state.md`,
`internal/cli/guides/ws.md` (agents' text), `docs/adrs/0072-remote-environments.md`,
`docs/adrs/README.md`, `docs/adrs/0012-workspace-layout.md`,
`docs/adrs/0014-command-set.md`, `docs/adrs/0044-workspace-trees.md`
(one "Amended by 0072" line each), `README.md` (`## Roadmap`),
`docs/design/remote-environments/README.md` (the failure-message table:
the outdated message per D5).

### Content

- `ws-claude.md`: `--env`, the `environment` field, D2's parent rules, no
  attach on an environment, the three codes.
- `ws-shell.md`: `--env` and D7.
- `ws-list.md`: `--env`, the three fields, the stale rule, the `ENV`
  column, D3's scope ("local by default until the desktop shows
  environments").
- `ws-env.md`: a section "Operations" with ADR 0073's table as built (the
  nine operations, their timeouts and `Since`), the request id and
  deduplication (D4), the protocol number (D5), and the security rules
  (D6).
- `docs/state.md`: `workspace.toml` version 4 with `environment`;
  `state/.environments/<name>/snapshot.json`'s fields with `protocol` and
  `agents`.
- `guides/ws.md`: for agents, one paragraph: "On a remote environment, `ws
  machine spawn` and `ws review start` run on that machine; your children
  are there too. You never choose an environment." (`ws llm` output; the
  guide test checks only codes and topics.)
- ADR 0072: `Status: Accepted (<date>)`, and an "Amendments" section with
  D2, D3 (including the PR 3 scope of `ws list`), D4, D5, D7 and D9 as
  accepted by the owner. `docs/adrs/README.md`'s row 0072: `Accepted`.
- README roadmap: an entry for PR 3.

### Acceptance

- Every link resolves (`grep -o '](\([^)]*\))'` on the changed files, each
  path exists).
- `go test -p 2 -count=1 -run 'GuideTopics|LLM' ./internal/cli/` passes.

### Verify

```
go test -p 2 -count=1 -run 'GuideTopics|LLM' ./internal/cli/
make lint
```
