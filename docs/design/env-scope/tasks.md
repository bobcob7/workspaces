# Tasks: environment scopes for children

Implements [ADR 0082](../../adrs/0082-environment-scopes-for-children.md);
formats, flows, errors and the threat model are in [design.md](design.md).
**Every task first reads [constraints.md](constraints.md)**: what this pod
can't run (no second machine, no Windows), timings, the pinned values a task
can break (protocol, guide tests, error-code overview, moq), the Makefile
lint rules, and the failing-first rule for security tests.

Base: `feat/env-scope`. No new module dependency: `go.mod`/`go.sum` stay
unchanged.

## Order

```
T1 ─┐ (T1 ∥ T2)      ┌─ T3 ─┐ (T3 ∥ T4)
T2 ─┴────────────────┤      ├─ T5 ── T6
                     └─ T4 ─┘
```

- **T1 ∥ T2**: disjoint files (`internal/envscope`, `internal/config`, vs
  `internal/envproto`). Both `small`-ish, but T1 has three source files.
- **T3 ∥ T4** after T1 and T2: T3 touches `internal/environment`,
  `internal/daemon/{server,interfaces,client}.go`, `internal/cli/env_daemon.go`;
  T4 touches `internal/envagent`, `internal/daemon/{paths,listen}.go`,
  `internal/cli/env_agent*.go`. Disjoint. Merge the second with
  `merge.sh --rebase` and compile.
- **T5** after T3 and T4. **T6** after T5 (it also finishes the docs).

Common verify block (every task runs it, plus its own lines):

```bash
go build ./...
test -z "$(go tool golangci-lint fmt --diff ./...)"
go tool golangci-lint run ./...
go test -count=1 -run '^TestRepository$' ./internal/blanklines/
git diff --exit-code origin/feat/env-scope -- go.mod go.sum
```

Report: list every command that was refused or couldn't run under
`## Not run here`; record `PASS` if everything that could run passed. For
the failing-first tests, paste the test name and its first failure line.

---

## T1. `internal/envscope`: scopes, `scope.json`, the ledger; `default_scope` config

Depends on: none. Parallel with T2. Constraints: §3 "Config keys", §5 rows
T1.

### Files

New: `internal/envscope/scope.go`, `scope_test.go`, `file.go`,
`file_test.go`, `ledger.go`, `ledger_test.go`.
Changed: `internal/config/config.go`, `internal/config/edit.go`, their
`_test.go` files, `docs/config.md` (one row in the `[environments]` table).

### API (`package envscope`, doc: `// Package envscope holds environment scopes: which environments a workspace may start children on (ADR 0082).`)

```go
const Local = "local"
const MaxEntries = 64
const MaxFileBytes = 16 << 10
const MaxLedgerEntries = 4096

var ErrInvalid = errors.New("invalid scope")
var ErrOutOfScope = errors.New("environment out of scope")

type Ref struct {
	ID   string `json:"id"`   // Local or envproto.ValidID
	Name string `json:"name"` // Local for Local
}

// Scope is a set of refs that always holds Self. The zero value is invalid.
type Scope struct { /* unexported: self string; refs []Ref sorted */ }

func Only(self Ref) Scope                                   // {self}
func New(self Ref, refs []Ref) (Scope, error)               // dedupe by ID, add self, validate ids, ≤ MaxEntries, sort: Local first, then by Name
func (s Scope) Self() Ref
func (s Scope) Refs() []Ref                                  // copy, sorted
func (s Scope) IDs() []string
func (s Scope) Names() []string
func (s Scope) Contains(id string) bool
func (s Scope) ByName(name string) (Ref, bool)
func (s Scope) WiderThanSelf() bool
// Narrow returns {self} ∪ ids. self.ID and every id must be in s, else an
// *OutOfScopeError for the first that isn't (input order).
func (s Scope) Narrow(self Ref, ids []string) (Scope, error)
// Intersect keeps the refs whose ID is in ids, and always self.
func (s Scope) Intersect(ids []string) Scope

type OutOfScopeError struct {
	Environment string   // the offending name (or id when no name is known)
	Allowed     []string // s.Names()
}
func (e *OutOfScopeError) Error() string // "environment <x> isn't in your scope (<a>, <b>)"
func (e *OutOfScopeError) Unwrap() error  // ErrOutOfScope
```

`file.go` (design §3.1):

```go
type Parent struct {
	Environment string `json:"environment"`
	Workspace   string `json:"workspace"`
}
type File struct {
	Version      int       `json:"version"` // 1
	Environments []Ref     `json:"environments"`
	Self         string    `json:"self"`
	Source       string    `json:"source"` // "granted" | "inherited" | "narrowed"
	Parent       *Parent   `json:"parent"`
	WrittenAt    time.Time `json:"written_at"`
}
func Path(stateDir, id string) string // state/<id>/scope.json
// Read returns Only(self) and nil, nil when the file is missing; Only(self)
// and an error wrapping ErrInvalid when it is malformed, too big, has
// unknown fields, version != 1, > MaxEntries, a bad id, self.ID absent, or
// File.Self != self.ID. Never a wider scope on any error.
func Read(stateDir, id string, self Ref) (Scope, *File, error)
// Write writes f atomically (temp file + rename) with mode 0600, creating
// state/<id>/ (0700) if needed. f is validated as Read would.
func Write(stateDir, id string, f *File) error
func FileFor(s Scope, source string, parent *Parent, now time.Time) *File
```

`ledger.go` (design §3.2):

```go
type Entry struct {
	Environment string    `json:"environment"` // ValidID
	Workspace   string    `json:"workspace"`
	Scope       []string  `json:"scope"`       // ids
	Parent      *Parent   `json:"parent"`
	Role        *string   `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}
type Ledger struct { /* path, lock string */ }
func NewLedger(environmentsDir string) *Ledger // scopes.json, scopes.lock
func (l *Ledger) Put(ctx context.Context, e *Entry) error      // replace by (Environment, Workspace); > MaxLedgerEntries → error
func (l *Ledger) Remove(ctx context.Context, env, ws string) error // missing key is not an error
func (l *Ledger) List() ([]Entry, error)                        // missing file → empty
func (l *Ledger) Get(env, ws string) (Entry, bool, error)
// Ceiling returns the Scope ids of the first entry, in chain order, whose
// Environment == env and Workspace == chain[i]; nil when none.
func (l *Ledger) Ceiling(env string, chain []string) ([]string, error)
```

Use `internal/lockfile.Exclusive` on `scopes.lock` for `Put`/`Remove`,
`Shared` for reads; atomic write, mode 0600. Validate ids with
`envproto.ValidID` (or `Local` for `Parent.Environment`).

Config: `Environments.DefaultScope []string \`toml:"default_scope" json:"default_scope"\``;
validation in the existing `[environments]` check: each entry 1-64 bytes,
no whitespace, `,` or control character, no duplicates →
`fmt.Errorf("%w: environments.default_scope ...", ErrInvalid)`.
`environmentRules()` gets `{key: "environments.default_scope", typ: TypeStrings, table: "environments", leaf: "default_scope"}` **last**.
`docs/config.md` row: `` | `default_scope` | strings | `[]` | Environments every agent a person starts may also start children on (ADR 0082); a person overrides it with `ws claude --env-scope` | ``.

### Tests (failing first for the starred ones)

- `TestScope_Narrow`*: narrowing to a subset works; a superset id →
  `*OutOfScopeError` with that name and `Allowed`; self not in s → error;
  result always contains self; nil ids → `{self}`.
- `TestNew`, `TestScope_Intersect` (keeps self even when ids is empty),
  `TestScope_Contains`.
- `TestRead`*: missing → `Only(self)`, nil error; each malformed case above
  → `Only(self)` and `errors.Is(err, ErrInvalid)`; a file wider than self
  but with `self` mismatched → `Only(self)`.
- `TestWrite`: round-trip; mode 0600 (skip the mode check on Windows).
- `TestLedger_Put`, `_Remove`, `_List`, `_Get`, `_Ceiling` (nearest wins;
  entries on another environment ignored; nil when none), full ledger.
- Config: a valid list loads; each invalid case → `ErrInvalid`;
  `ws config` lists the key (extend the existing environment-keys test).

### Verify

```bash
go test -p 2 -count=1 ./internal/envscope ./internal/config
```

### Acceptance

All the above; `envscope` imports only the standard library,
`internal/envproto` and `internal/lockfile`; no other package changed.

---

## T2. `envproto`: protocol 4, scope payloads and ops

Depends on: none. Parallel with T1. `small` (3 source files). Constraints:
§3 rows "Protocol", "`LookupOp` table tests".

### Files

Changed: `internal/envproto/payloads.go`, `internal/envproto/ops.go`,
`internal/envproto/ops_test.go`. New: `internal/envproto/scope.go`,
`internal/envproto/scope_test.go`.

### Changes

- `payloads.go`: `const Protocol = 4`; add `const ProtocolScopes = 4`
  (comment: "scopes and machine.record, ADR 0082").
- `ops.go`: `CreateArgs` gains `Scope *ScopeGrant \`json:"scope,omitempty"\``
  and `Spawn *SpawnInfo \`json:"spawn,omitempty"\`` (last two fields).
  `validateCreate` also validates them (below).
- `scope.go`: every type and constant in [design §3.3](design.md#33-envproto-protocol-4)
  verbatim (`EnvRef`, `WorkspaceAt`, `ScopeGrant`, `SpawnInfo`,
  `MachineRecordResult`, `RecordResult`, `MaxRecordReportBytes`, the five
  `Op*` constants, `ScopeCallerArgs`, `ScopeEnvsArgs`, `ScopeSpawnArgs`,
  `ScopeChildrenArgs`, `ScopeChildArgs`, `ScopeEnvsResult`, `ScopeEnv`,
  `ScopeChildrenResult`, `ScopeChild`), plus:
  - `scopeOpSpec(name string) (OpSpec, bool)`: `machine.record` →
    `{Caller: CallerController, Since: ProtocolScopes, Timeout: 30 * time.Second}`;
    `scope.envs`, `scope.children` → `{Caller: CallerAgent, Timeout: 30 * time.Second}`;
    `scope.child` → `{Caller: CallerAgent, Timeout: 2 * time.Minute}`;
    `scope.spawn` → `{Caller: CallerAgent, Timeout: 10 * time.Minute}`.
    `LookupOp` calls it before `loopOpSpec`.
  - `newScopeArgs(op string) (any, bool)` used by the args factory (beside
    `newReviewArgs`): `machine.record` → `*WorkspaceArgs`; the four
    `scope.*` → their `*Scope…Args`.
  - `Validate` cases: `ScopeCallerArgs.Workspace` nil or a valid workspace
    (`validateWorkspace`); `Ancestors` ≤ 64 and `Scope` ≤ 64 entries, each
    valid; `Scope` ids `"local"` or `ValidID`; `Target`/`Environment`
    non-empty ≤ 64 bytes, no control char; `Narrow` ≤ 64;
    `Create` validated with `validateCreate` and must have `Scope == nil`
    and `Parent == nil`; `Descendants` ≤ 1024; `Op` ∈ {`status`, `logs`,
    `rm`, `record`}; `Tail` 0..`MaxTail`; `Child`, `Parent` valid
    workspaces with no `/`, `\`, or leading `.` (they become path
    elements). `ScopeGrant`: 1..64 refs, ids valid, names non-empty.
    `SpawnInfo`: `Role` 1-64 bytes, `Pass`/`Fail` ≤ 16 entries each ≤ 32
    bytes, `Vars` ≤ 64 keys.
  Every failure is `*ArgsError{Code: "usage"}`.

### Tests

- `ops_test.go` `TestLookupOp`: rows for the five ops (caller, since,
  timeout).
- `scope_test.go`: `TestDecodeArgs` for each new op (valid; unknown field
  refused; each bound +1 refused); `CreateArgs` with nil `Scope`/`Spawn`
  marshals to exactly the protocol-3 bytes (no `scope`/`spawn` keys);
  `ScopeSpawnArgs.Create` with `Scope` set → usage.

### Verify

```bash
go test -p 2 -count=1 ./internal/envproto ./internal/envagent ./internal/environment
GOOS=windows go vet ./internal/envproto
```

(`envagent`/`environment` use `envproto.Protocol` in tests and must still pass.)

### Acceptance

The tests above pass; no other package's code changes.

---

## T3. Controller: `Controller.Scope`, ceiling, ledger, cleanup; daemon routes

Depends on: T1, T2. Parallel with T4. Constraints: §3 rows "Controller
`answer`", "moq mocks"; §5 rows T3. Design §2.1, §2.2, §2.6, §3.4, §4, §5.

### Files

New: `internal/environment/scope.go`, `internal/environment/scope_test.go`.
Changed: `internal/environment/controller.go` (`answer`, a 60 s cleanup
ticker started in `start`), `internal/environment/interfaces.go`
(`LocalScopes`), `internal/environment/moq_test.go` (regenerated),
`internal/daemon/server.go` (4 routes), `internal/daemon/interfaces.go`
(`EnvironmentHost.Scope`), `internal/daemon/client.go` (`Client.Scope`),
`internal/daemon/moq_test.go` (regenerated), `internal/daemon/env.go` or the
file holding `NopEnvironments` (its `Scope`), `internal/cli/env_daemon.go`
(`envHost.Scope`, pass a `LocalScopes`).

### API

```go
// interfaces.go (add to the go:generate list)
type LocalScopes interface {
	// Scope is local workspace id's scope (envscope.Read with self local);
	// ErrWorkspaceNotFound when the workspace doesn't exist here.
	Scope(id string) (envscope.Scope, error)
	// Exists reports whether local workspace id exists.
	Exists(id string) (bool, error)
}

// scope.go
var ErrWorkspaceNotFound = errors.New("workspace not found")
func (c *Controller) SetLocalScopes(l LocalScopes, ledger *envscope.Ledger)
// Scope runs one scope operation for caller ("local" or an environment id)
// and returns the reply to send. op is "envs", "spawn", "children" or
// "child"; args the JSON body. It never returns a Go error: failures are
// error replies with ws error codes.
func (c *Controller) Scope(ctx context.Context, caller, op string, args json.RawMessage) envproto.Reply
```

Behaviour (each a small unexported func):

- `effectiveScope(caller, a *envproto.ScopeCallerArgs) (envscope.Scope, error)`:
  - caller `local`: `a.Workspace == nil` → every enrolled environment +
    `local` (person grant; only reachable from the daemon route).
    Otherwise `LocalScopes.Scope(*a.Workspace)` — the claim in `a.Scope`
    is **ignored**.
  - caller env E: `a.Workspace == nil` → `usage`. Self = E's record.
    `ceiling := ledger.Ceiling(E, [w] + ancestors)`; none → `{E}`.
    Result = `New(self, refs of ceiling)` then `.Intersect(a.Scope)`.
- `envs`: map each id to a `ScopeEnv`: `local` → online; a record → its
  presence (`online`/`offline`); no record → `removed`. `current` = id ==
  caller.
- `spawn`: resolve `Target` with `store.Get` (`environment_unknown`);
  target id ∉ effective → `environment_out_of_scope` with details
  `{"environment": target, "allowed": names}`; `Target == "local"` and
  caller ≠ local → `usage` ("starting a child on local from an environment
  isn't supported yet"); target id == caller → `usage` ("spawn on your own
  environment without --env"). Child scope = effective, or
  `Narrow(targetRef, ids of Narrow names)` (unknown name →
  `environment_unknown`, outside → `environment_out_of_scope`). Send
  `workspace.create` = `a.Create` with `Scope = &ScopeGrant{refs, parent}`
  (parent = `{caller, *a.Workspace}`, nil for a person grant) and
  `Spawn = a.Spawn`, via the existing `c.call` with request id = the up
  request's id (for local callers: a fresh `newRequestID`) and deadline
  now + 10 min. Before sending: target protocol < `ProtocolScopes` →
  `environment_outdated` (existing `*OutdatedError` path, `Since` 4). On an
  ok reply, read `id` from the result and `ledger.Put`; a failed `Put` →
  call `workspace.rm` for that id and reply `internal`. Reply = the target's
  result object + `"environment": <target name>` + `"scope": names`.
- `children`: ledger entries with `Parent.Environment == caller` and
  `Parent.Workspace ∈ {workspace} ∪ Descendants`; `Entry` from the child's
  environment snapshot (`store.ReadSnapshot`, the workspace object with that
  id) or `null`.
- `child`: `ledger.Get(env id of a.Environment, a.Child)`; missing →
  `workspace_not_found`; `Parent.Environment != caller` or
  `Parent.Workspace != a.Parent` → `not_descendant`. Forward: `status` →
  `agent.status`, `logs` → `agent.logs{tail}`, `rm` →
  `workspace.rm{keep_children}`, `record` → `machine.record`. After `rm`
  ok or `workspace_not_found`, `ledger.Remove`.
- `answer` (controller.go): `cert.renew` as today; `scope.<x>` →
  `c.Scope(ctx, id, x, req.Args)`, with the up request's `request_id`
  passed through for `spawn`. Unknown agent op stays `usage`.
- Cleanup (design §2.6): `func (c *Controller) sweepLedger(ctx)` every 60 s
  while listening (use the controller's `now`; tests call it directly).
- Daemon: `POST /v1/scope/{op}` (op ∈ envs, spawn, children, child; else
  404) reads ≤ `envproto.MaxRequestBytes`, calls
  `envs.Scope(ctx, "local", op, body)`, answers 200 with the JSON
  `envproto.Reply`. `EnvironmentHost.Scope(ctx, caller, op string, args json.RawMessage) envproto.Reply`;
  `NopEnvironments.Scope` replies `environment_offline` with
  `{"reason": "not_listening"}`. `Client.Scope(ctx, op string, args any) (envproto.Reply, error)`.
- `internal/cli/env_daemon.go`: `envHost.Scope` delegates; build a
  `LocalScopes` from `a.workspaces.Find` and `envscope.Read(a.paths.State, id, envscope.Ref{ID: "local", Name: "local"})`;
  `ctrl.SetLocalScopes(..., envscope.NewLedger(a.paths.Environments))`.

### Tests (`scope_test.go`, with `envtest` and the helpers in `controller_test.go`/`call_test.go`; starred = failing first)

- `TestController_Scope`:
  - `spawn refuses a claim wider than the ledger ceiling`*: ledger entry
    `(A, w)` scope `{A}`; A's up request claims `{A, B}` and targets B →
    `environment_out_of_scope`, `allowed: [A's name]`, no request sent to B.
  - `spawn ignores ledger entries on other environments`*: ledger `(C, w)`
    scope `{C, B}`; request from A with workspace `w` → refused.
  - `child refuses a ledger parent on another environment`*: entry
    `(B, c)` parent `(C, p)`; A asks `rm` → `not_descendant`, no message to B.
  - `spawn sends the scope and writes the ledger`: ledger ceiling `{A,B}`,
    fake agent B answers `workspace.create` ok `{"id": "h-1a2b3c4d"}`; the
    request on B has `scope` and `parent {A, w}`; ledger has the entry; the
    reply has `environment` and `scope`.
  - `spawn narrows`, `spawn refuses a wider narrow`, `spawn to an
    outdated agent is environment_outdated` (hello protocol 3),
    `spawn to an offline environment`, `spawn to local from an environment
    is usage`, `a retried spawn reuses the request id`.
  - `local caller uses the local scope file, not the claim` (LocalScopes
    mock returns `{local}`; claim `{local, B}` → refused).
  - `envs lists states` (online, offline, removed, current).
  - `children lists ledger children of the caller and its descendants`.
  - `child forwards status, logs, rm and record`; `rm removes the entry`.
- `TestController_sweepLedger`: parent gone (local, remote snapshot fresh)
  → `workspace.rm` sent and entry dropped; child's env offline → kept;
  env record removed → dropped.
- `TestController_answer`: `scope.envs` from an agent is answered on
  `env/<id>/req`; `cert.renew` unchanged.
- daemon: `TestServer_routes` (or the existing server test) for
  `/v1/scope/spawn` → `EnvironmentHostMock.ScopeFunc` gets caller `local`;
  unknown op → 404; `TestClient_Scope`.

### Verify

```bash
go generate ./internal/environment ./internal/daemon
go test -p 2 -count=1 ./internal/environment/... ./internal/daemon
go test -p 2 -count=1 -run 'Env|Daemon' ./internal/cli
```

### Acceptance

Starred tests were seen failing first (in the report). Every refusal in
design §4 that the controller produces has a test. `cert.renew` tests
unchanged and green.

---

## T4. Agent side: create with scope, `machine.record`, the agent socket

Depends on: T1, T2. Parallel with T3. Constraints: §3 rows "Agent refuses
non-controller ops", "`remembered()` list". Design §2.2 steps 2-3, §2.3,
§3.4 (agent socket).

### Files

New: `internal/envagent/up.go`, `up_test.go`, `internal/daemon/listen.go`,
`listen_test.go`.
Changed: `internal/envagent/agent.go` (mutex around `pending`; `Up`),
`internal/envagent/renew.go` (use `Up`'s plumbing or keep, but guard
`pending`), `internal/envagent/requests.go` (lookup under the mutex),
`internal/envagent/ops.go` (`remembered`), `internal/daemon/paths.go`
(`AgentSocket`), `internal/cli/env_agent.go` (start the socket in
`runEnvAgentRun`), `internal/cli/env_agent_ops.go` (+test: create with
scope/spawn, `machine.record`).

### API

```go
// daemon/paths.go: Paths gains AgentSocket = same dir as Socket, name
// "<12 hex>-agent.sock".
// daemon/listen.go
// ListenPeer prepares the socket's directory as the daemon does (0700, the
// owner's DACL on Windows), removes a stale socket, listens, and accepts
// only connections from the current uid.
func ListenPeer(ctx context.Context, socket string, log *slog.Logger) (net.Listener, error)

// envagent/up.go
// Up publishes {op, args, request_id, deadline} on env/<id>/up with the
// response topic env/<id>/req and waits for the reply until the op's
// Timeout + Grace. Safe for concurrent use. Only scope.* ops.
func (a *Agent) Up(ctx context.Context, op string, args json.RawMessage) (envproto.Reply, error)
// SocketHandler serves POST /v1/scope/{envs,spawn,children,child}: checks
// the body size and decodes the args (envproto.DecodeArgs), refuses
// workspace null (usage), checks the workspace exists under the agent's
// workspaces root (CheckWorkspaceRef; workspace_not_found), then Up and
// writes the Reply as JSON. Not connected → an error reply
// environment_offline with details {"reason": "agent_unreachable"}.
func (a *Agent) SocketHandler() http.Handler
```

- The agent's main loop still owns the MQTT client; `Up` may be called from
  the HTTP goroutines, so `pending` is guarded by a `sync.Mutex`, and the
  reply dispatch in `requests.go` takes the lock. `Up` returns
  `errDisconnected` when the connection ends while waiting.
- `runEnvAgentRun`: `ln, err := daemon.ListenPeer(ctx, paths.AgentSocket, log)`;
  serve `agent.SocketHandler()` with an `http.Server` until ctx ends;
  remove the socket on exit. A listen failure is logged as a warning and
  the agent still runs.
- `env_agent_ops.go`, `workspace.create`: when `args.Scope != nil`, build
  the `envscope.File` (self = this agent's `{id, name}`, source `granted`,
  parent from the grant) and write it **before** the session starts (hook
  the creation the same way the `started` callback does; if it can only run
  after, write it there and document why); a write failure removes the
  workspace and returns `internal`. When `args.Spawn != nil`, write
  `spawn.toml` with `machine.Store.WriteSpawn(id, id, ...)` (Role, Model,
  Pass, Fail, Vars, SpawnedAt now).
- `machine.record`: find the workspace (`workspace_not_found`), root =
  `machine.Root(list, id)`, `Store.Read(root, id)`, `ReadProgress`; report
  read from `ReportPath`, cut to `MaxRecordReportBytes` (on a UTF-8
  boundary) with `report_truncated`; `progress` and `rollup` as the JSON
  `ws machine progress show` prints for that workspace (reuse its encoder),
  else `null`. Add `machine.record` to the not-remembered list.

### Tests

- `up_test.go`: `TestAgent_Up` with `envtest` (a fake controller
  subscription answers on `env/<id>/req` with the correlation data):
  reply delivered; two concurrent `Up` calls get their own replies;
  timeout uses the op's timeout (fake clock `opts.After`); disconnected →
  error. `TestAgent_SocketHandler`: null workspace → `usage`; unknown
  workspace → `workspace_not_found`; not connected →
  `environment_offline`/`agent_unreachable`; ok path returns the reply.
- `listen_test.go`: `TestListenPeer` creates a 0700 dir and a socket that
  accepts this uid (unix only, `//go:build unix` on the file if needed).
- `env_agent_ops_test.go`: create with scope writes `scope.json` with self
  = the agent and the grant's parent; with spawn writes `spawn.toml`; a
  protocol-3-shaped create writes neither (unchanged); `machine.record`
  returns result, truncated report, progress; unknown workspace →
  `workspace_not_found`.
- `ops_test.go`: `scope.spawn` from the controller is refused `usage`
  (it's `CallerAgent`).

### Verify

```bash
go generate ./internal/envagent
go test -p 2 -count=1 ./internal/envagent ./internal/daemon
go test -p 2 -count=1 -run 'EnvAgent|AgentOperations|Conformance' ./internal/cli
GOOS=windows go build ./... && GOOS=windows go vet ./internal/daemon ./internal/envagent
```

### Acceptance

Tests pass; the conformance tests (`env_ops_conformance_test.go`) still
pass unchanged; no route on the agent socket other than the four.

---

## T5. CLI: grant, `ws machine spawn --env`, `ws machine envs`, `environment_out_of_scope`

Depends on: T3, T4. Constraints: §3 rows "Error codes" (all three), "Every
command has a guide", "Every flag is in its command's guide", "Commands
with a result document their JSON", "The overview points to every guide",
"Exact JSON"; §5 rows T5. Design §1.1-§1.3, §2.1, §2.4, §4.

### Files

New: `internal/cli/scope.go`, `scope_test.go`, `internal/cli/machine_envs.go`,
`machine_envs_test.go`, `internal/cli/guides/ws-machine-envs.md`.
Changed: `internal/cli/errors.go` (code, `exitCode`, `known`),
`internal/cli/interfaces.go` (+ `go generate ./internal/cli`),
`internal/cli/machine.go` (flags, spawn path, `newMachineEnvsCmd` in
`AddCommand` after children), `internal/cli/claude.go` (`--env-scope`,
refusals, inherit), `internal/cli/env_call.go` (`parentOnEnv`: inside a
workspace → `usage` instead of the warning), `internal/cli/shell.go`
(refuse `--env` inside a workspace), `internal/cli/llm.go`
(`"machine envs"` after `"machine children"`), guides `ws.md`,
`ws-machine.md`, `ws-machine-spawn.md`, `ws-claude.md`, `ws-shell.md`, and
their tests (`machine_test.go`, `claude_test.go`, `shell_test.go`).

### API

```go
// errors.go
codeEnvironmentOutOfScope errorCode = "environment_out_of_scope" // exit 4 (ADR 0082)

// interfaces.go (add to go:generate)
type scopeCaller interface {
	Scope(ctx context.Context, op string, args any) (envproto.Reply, error)
}

// scope.go
func (a *App) selfRef() (envscope.Ref, error)        // agent identity in a.paths.Environment → {ID, Name}; none → local
func (a *App) workspaceScope(id string) envscope.Scope // envscope.Read; on ErrInvalid append a warning, return Only(self)
func (a *App) scopeClient(ctx context.Context) (scopeCaller, error) // self local → daemon client (dialEnvironments); else agent socket client (daemon.PathsFor(...).AgentSocket); unreachable → environment_offline {reason: agent_unreachable}
func (a *App) callScope(ctx context.Context, op string, args any) (json.RawMessage, error) // Reply → result or remoteReplyError
func (a *App) grantScope(names []string) (envscope.Scope, error) // person grant: names or config default_scope; resolve via envStore (local only on the controller); unknown → environment_unknown
func (a *App) writeChildScope(child string, s envscope.Scope, source string) // only if s.WiderThanSelf(); failures are warnings
func scopeError(err error) error // *envscope.OutOfScopeError → cliError environment_out_of_scope with details {"environment", "allowed"}
```

- `ws claude`: `--env-scope` (`StringSliceVar`). Inside a workspace,
  `--env-scope` or `--env` → `usage` with the design §1.1 message (check
  before anything is created). Outside: local start → after creation write
  `scope.json` from `grantScope` if wider than `{local}`; remote start with
  a grant wider than `{target}` → `callScope("spawn", ScopeSpawnArgs{workspace: nil, ...})`;
  otherwise the existing path. Inside a workspace without those flags, the
  child inherits the enclosing scope (`writeChildScope`).
- `ws shell --env` inside a workspace → `usage`.
- `ws machine spawn`: `--env` (string), `--env-scope` (`StringSliceVar`).
  Without `--env` or with self's name: today's path, then
  `writeChildScope(child, narrowed, "inherited"|"narrowed")` in the
  `started` callback (and after `startRunnerAgent` for `--runner ws`).
  With another env: precheck against `workspaceScope(caller)`
  (`environment_out_of_scope` / `environment_unknown`), render the role as
  today (prompt, model) into `SpawnInfo`, build `CreateArgs` with
  `remoteCreateArgs`, and `callScope("spawn", ScopeSpawnArgs{...})` with
  `ancestors` = `machine.Ancestors(list, caller)` and `scope` = caller's
  ids. Print the result object (design §1.2) with `warnings`.
- `ws machine envs`: self local and scope `{local}` → no daemon call,
  `[{local, online, current}]`. Otherwise `callScope("envs", ...)`; on
  `environment_offline`/`daemon_unavailable` fall back to the scope file's
  refs with `state: "unknown"` and a warning. Output design §1.3.

### Guides (same task, the guide tests require it)

- `ws-machine-envs.md`: heading `# ws machine envs`, synopsis, when to use,
  "Output is always JSON; `--json` is accepted and changes nothing.",
  `## JSON output` (design §1.3), `## Exit codes` (`outside_workspace`,
  `internal`), `## Examples`.
- `ws-machine-spawn.md`: synopsis gets `[--env <name>] [--env-scope <name>,...]`;
  a `## Environments` section (scope, `ws machine envs`, narrowing only,
  the child's scope contains its environment, `local` from a remote isn't
  supported yet); flags rows; JSON for a remote child; exit rows
  `environment_out_of_scope` (4), `environment_unknown` (3),
  `environment_offline`, `environment_outdated` (1).
- `ws-machine.md`: synopsis line for `envs` and `spawn`'s new flags; a
  "When to use it" bullet.
- `ws.md`: the rule (design §6, verbatim), `` `ws llm machine envs` `` in
  the guide list, `environment_out_of_scope` in the exit-4 row; the
  "People, not agents" paragraph mentions `ws claude --env-scope` and
  `[environments] default_scope`.
- `ws-claude.md`: `--env-scope` row, the refusal inside a workspace, exit
  rows (`environment_unknown`, `usage`). `ws-shell.md`: the refusal.

### Tests (starred = failing first; run before writing the code)

In `machine_test.go` `TestApp_runMachineSpawn` (use `serveDaemonWith` +
`EnvironmentHostMock.ScopeFunc`, and a temp state dir):

- `scope without a grant spawns locally and writes no scope.json`*
  (default-local unchanged: no daemon call, output equals today's JSON).
- `scope --env out of scope is environment_out_of_scope`*: details list
  `["local"]`; no Scope call made.
- `scope --env-scope wider than the caller's is refused`*.
- `scope ignores a default_scope edited in config.toml`*: config
  `default_scope = ["b"]`, caller has no `scope.json`, `--env b` → refused.
- `scope ignores environment variables`*: `WS_ENV_SCOPE=b`,
  `WS_SCOPE=b`, `WS_ENVIRONMENT=b` in `a.environ` → `--env b` refused.
- `scope --env in scope calls scope spawn` (args: workspace = caller,
  ancestors, scope ids, target, create, spawn for `--role`) and prints
  `environment` and `scope`.
- `scope a local child inherits and narrows` (`scope.json` contents).
In `claude_test.go`: `TestApp_runClaude/refuses --env-scope inside a workspace`*,
`.../refuses --env inside a workspace`*, `.../grants --env-scope outside a workspace`,
`.../uses default_scope`, `.../unknown --env-scope name is environment_unknown`.
`shell_test.go`: `--env` inside a workspace → usage.
`machine_envs_test.go`: local only (no daemon); with a scope via the
daemon; daemon unavailable → `unknown` + warning; outside a workspace →
`outside_workspace`.

### Verify

```bash
go generate ./internal/cli
go test -p 2 -count=1 -run 'Scope|MachineSpawn|MachineEnvs|RunClaude|Shell|Guide|LLM|Error' ./internal/cli
go test -p 2 -count=1 ./internal/cli
```

### Acceptance

Every starred test was seen failing first. `ws llm` tests green (guides,
overview, error rows). Local `ws machine spawn` and `ws claude` JSON
unchanged (existing `JSONEq` tests untouched and green).

---

## T6. Remote children in `wait`, `children`, `logs`, `rm`; copy-back; docs

Depends on: T5. Constraints: §3 rows "Exact JSON" (wait items gain
`environment`), "Every flag…" (no new flags). Design §1.4, §2.5, §6.

### Files

New: `internal/cli/machine_remote.go`, `machine_remote_test.go`,
`internal/machine/remote.go`, `remote_test.go`.
Changed: `internal/cli/machine_wait.go`, `internal/cli/machine.go`
(`runMachineChildren`, `runMachineRm`), `internal/cli/machine_logs.go`,
`internal/cli/machine_progress.go` (`show` nests remote children), guides
`ws-machine-wait.md`, `ws-machine-children.md`, `ws-machine-logs.md`,
`ws-machine-rm.md`, `ws-machine-progress-show.md`; docs
`docs/tools/ws-machine.md`, `docs/tools/ws-claude.md`, `docs/tools/ws-env.md`,
`docs/state.md`, `README.md`.

### API

```go
// machine/remote.go
// RemoteDir is state/<root>/machine/remote/<envID>/<child>/; each part is
// checked like Dir's (checkComponent) and envID must be envproto.ValidID.
func (s *Store) RemoteDir(root, envID, child string) (string, error)
// WriteRemote writes result.toml, spawn.toml (when sp != nil) and report.md
// (mode 0600) once: an existing result.toml leaves everything as it is.
func (s *Store) WriteRemote(root, envID, child string, sp *Spawn, r *Result, report []byte) error
func (s *Store) ReadRemote(root, envID, child string) (Record, error)

// cli/machine_remote.go
type remoteChild struct {
	env, envID, id, parent string
	role                   *string
	entry                  json.RawMessage
}
func (a *App) remoteChildren(ctx context.Context, caller string, descendants []string) ([]remoteChild, []string /*warnings*/)
func (a *App) remoteChildOp(ctx context.Context, rc *remoteChild, op string, extra map[string]any) (json.RawMessage, error)
func (a *App) remoteRecord(ctx context.Context, root string, rc *remoteChild) (machine.Record, *envproto.MachineRecordResult, error) // copies back on first result
```

- `remoteChildren`: `callScope("children", ScopeChildrenArgs{workspace: caller, descendants: <local descendants>})`.
  Self local with scope `{local}` and an empty ledger is the common case:
  skip the call when the caller's scope isn't wider than self **and** self
  is local and the ledger file is absent (default-local unchanged: no
  daemon start). Errors become warnings; local results still print.
- `children`: append one `machineChildJSON` per remote child (entry from
  the reply, plus role/result/progress/rollup from `remoteRecord`).
- `wait`: an argument not found locally is looked up in
  `remoteChildren`; remote targets are polled every 10 s
  (`scope.child` `status` + `record`); finished per design §1.4; each wait
  item gains `Environment string \`json:"environment"\`` (`"local"` for
  local ones). `--timeout`, `--any` work across both.
- `logs`: `scope.child` `logs` with `tail`.
- `rm`: `scope.child` `rm`; `--all` includes remote direct children;
  `--dry-run` lists them with `environment`.
- `progress show`: remote children appear as leaves with the last
  `machine.record` progress.

### Tests

`machine_remote_test.go` (daemon mock `ScopeFunc` returning canned
replies): children lists a remote child with its environment; wait
returns pass with the report copied to `machine/remote/<env>/<id>/report.md`
and the path in `result.report`; wait after the child is removed reads the
copy; logs forwards `tail`; rm calls `child rm`; a `not_descendant`
reply is passed through as exit 4; offline → warning in `children`,
`environment_offline` from `rm`. `remote_test.go`: `RemoteDir` refuses
`..`, `/`, a bad env id; `WriteRemote` writes once. Default-local: with no
scope and no ledger, `children`/`wait` make **no** daemon call (existing
tests unchanged).

### Docs

- `docs/tools/ws-machine.md`: `envs`, `spawn --env/--env-scope`, remote
  children, copy-back, the new error.
- `docs/tools/ws-claude.md`: `--env-scope`, `default_scope`, refusal inside
  a workspace. `docs/tools/ws-env.md`: a "Scopes" section (granting, the
  ledger, protocol 4, what the controller checks).
- `docs/state.md`: `state/<id>/scope.json`, `environments/scopes.json` and
  `scopes.lock`, `state/<root>/machine/remote/<env-id>/<child>/`, the agent
  socket `<12 hex>-agent.sock`.
- `README.md`: one paragraph in the remote-environments part: a person
  grants scopes; agents use `ws machine spawn --env`.
- ADR 0082 stays `Proposed` (the owner accepts it on merge).

### Verify

```bash
go test -p 2 -count=1 ./internal/machine
go test -p 2 -count=1 -run 'Machine|Guide|LLM' ./internal/cli
go test -p 2 -count=1 ./internal/cli
make lint
```

### Acceptance

All tests green; every guide test green; `make lint` clean; the docs list
above updated.
