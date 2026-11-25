# Design: environment scopes for children

Implements [ADR 0082](../../adrs/0082-environment-scopes-for-children.md).
Environment limits, timings and pinned values are in
[constraints.md](constraints.md); the build order is in [tasks.md](tasks.md).

Terms:

- **Controller**: the `ws` daemon on the person's machine, holding the MQTT
  broker and the CA ([0074](../../adrs/0074-mqtt-transport-embedded-broker.md)).
  Its machine is the environment `local`.
- **Environment ref**: `{"id": "env-1a2b3c4d", "name": "linux-server"}`, or
  `{"id": "local", "name": "local"}` for the controller's machine. Scopes are
  compared by **id**; names are for people and output.
- **Self**: the environment a `ws` process runs on. On a machine with an
  enrolled agent identity (`CONFIG_DIR/environment/`, the agent's
  certificate's CN), self is that id; otherwise `local`.
- **Scope** of a workspace: a set of environment refs that always contains
  self.
- **Ledger**: the controller's record of the remote workspaces it created
  with a scope or as a cross-environment child.

## 1. Commands

### 1.1 Granting (people)

```
ws claude [--env <name>] [--env-scope <name>[,<name>...]] ...
ws shell  [--env <name>] ...                       (unchanged flags, new refusal)
```

- `--env-scope` takes environment names from `ws env list`, plus `local`;
  comma-separated and repeatable (`StringSliceVar`). The new workspace's scope
  is `{self of the new workspace} ∪ names`. Without the flag it is
  `{self} ∪ [environments] default_scope`.
- Each name must be `local` or an enrolled environment: otherwise
  `environment_unknown` (exit 3, `details.environment`), and nothing is
  created.
- **Inside a workspace** (the enclosing-workspace check `ws machine` uses),
  `ws claude --env-scope`, `ws claude --env` and `ws shell --env` fail with
  `usage`: `an agent never grants a scope or picks an environment with ws
  claude; use ws machine spawn --env`. This replaces today's warning in
  `parentOnEnv` ("not a child of … it runs on …"). `ws claude` inside a
  workspace without those flags is unchanged and its child **inherits** the
  enclosing workspace's scope.
- A local workspace whose scope is exactly `{local}` gets **no** `scope.json`
  (so default runs write nothing new).
- `ws claude --env A --env-scope ...` goes through the daemon route
  `POST /v1/scope/spawn` with `"workspace": null` (a person grant, ceiling =
  every enrolled environment plus `local`), so the ledger records it. With no
  `--env-scope` and an empty `default_scope`, `ws claude --env A` uses the old
  `POST /v1/environments/A/call` path unchanged.

Config (`config.toml`):

```toml
[environments]
default_scope = ["linux-server"]   # names; default []; "local" allowed
```

Key `environments.default_scope`, type `strings` (`config.TypeStrings`), in
`environmentRules()`. Each entry must be 1-64 bytes with no whitespace,
comma or control character, and not repeated, else `config_invalid` (the
config package doesn't import `internal/environment`). Whether a name is
enrolled is checked at grant time, not at load (`environment_unknown`). It is read **only** when a person starts
a workspace with no parent: no child ever reads it.

### 1.2 `ws machine spawn --env` (agents)

```
ws machine spawn [--env <name>] [--env-scope <name>[,<name>...]] [existing flags] [<spec>...] -- "<prompt>" ...
```

| Case | Result |
| --- | --- |
| no `--env`, or `--env` = self's name (`local` on the controller) | as today: child on this machine, parent = caller. Child scope = caller's, or `--env-scope` narrowed |
| `--env X`, X in caller's effective scope | child on X (see 2.2). Output = spawn JSON + `"environment": "X"` + `"scope": [...]` |
| `--env X`, X not in scope | `environment_out_of_scope` (exit 4), nothing created |
| `--env-scope` names any env not in caller's scope | `environment_out_of_scope` (exit 4), `details.environment` = first such name |
| `--env X`, X in the scope but its environment was removed (`ws env rm`) | `environment_unknown` (exit 3). A name **not** in the scope is `environment_out_of_scope` even if it isn't enrolled, so an agent learns nothing about environments outside its scope |
| `--env X`, X offline | `environment_offline` (exit 1, existing details) |
| `--env X`, X's agent protocol < 4 | `environment_outdated` (exit 1, `need_protocol: 4`) |
| `--env local` from a remote environment | `usage`: `starting a child on local from an environment isn't supported yet` |
| on a remote env whose controller can't be reached (agent socket missing, or agent offline) | `environment_offline` with `details.reason` = `"agent_unreachable"` |

The child's scope always contains its own environment: `--env X --env-scope
Y` gives `{X} ∪ {Y}` (both must be in the caller's scope).

Spawn JSON for a cross-environment child (local children: **unchanged**, no
new fields):

```json
{
  "id": "helper-1a2b3c4d",
  "session": "e8cdf5d8",
  "path": "/home/u/.config/ws/workspaces/helper-1a2b3c4d",
  "parent": null,
  "repos": [],
  "warnings": [],
  "role": null,
  "runner": "claude",
  "model": null,
  "ws_runner": null,
  "environment": "win-rig",
  "scope": ["linux-server", "win-rig"]
}
```

`parent` is `null` because the remote record has no parent on its own
machine; the link lives in the ledger. `scope` is names, sorted, `local`
first.

### 1.3 `ws machine envs` (new, agents)

```
ws machine envs
```

No flags. Lists the caller's effective scope. Always JSON.

```json
{
  "environments": [
    {"name": "local", "id": "local", "state": "online", "current": true},
    {"name": "win-rig", "id": "env-2b3c4d5e", "state": "offline", "current": false}
  ],
  "warnings": []
}
```

- `state`: `online` | `offline` | `removed` (in the scope, but `ws env rm`
  removed it). `local` is always `online` on the controller. On a remote
  environment whose controller can't be reached, the command still succeeds
  with each entry from the caller's `scope.json`, `state` = `"unknown"`, and a
  warning.
- `current`: the entry for self.
- Order: `current` first, then by name.

Exit codes: `outside_workspace` (2), `internal` (1).

### 1.4 Remote children in the other `ws machine` commands

A **cross-environment child** is a ledger entry whose `parent` is the caller
or one of the caller's descendants on the caller's machine.

| Command | Behaviour for a cross-environment child |
| --- | --- |
| `ws machine children [--all]` | listed after local ones, each the child's `ws list --json` entry from its environment's snapshot (with `"environment"`, `"stale"`), plus `role`, `result`, `progress`, `rollup` from `machine.record`. If its environment is offline: the snapshot entry (stale), `result` from the copied-back record if there is one, else `null`, and a warning. `--all` adds nothing beyond direct cross-env children of every local descendant (their own descendants on the other machine are not walked in this version) |
| `ws machine wait <id>...` | an id is first matched locally, then against the ledger. Polls a remote child every **10 s** (`scope.child` op `record` + `status`). Finished = it has a result, or its session state is `done`/`stopped` with no result (outcome `missing`). On finish the result is **copied back** (2.5). Wait item gains `"environment"` (`"local"` for local children; always present) |
| `ws machine logs <id>` | `scope.child` op `logs` (= `agent.logs`, `tail` ≤ 2000). After removal: `workspace_not_found` (remote logs aren't kept) |
| `ws machine rm <id>` / `--all` | `scope.child` op `rm` (`workspace.rm`); the ledger entry is dropped on success or `workspace_not_found`. `--dry-run` lists it with `"environment"`. Offline: `environment_offline`, entry kept |
| `ws machine result` | unchanged: the child runs it on its own machine; its `spawn.toml` there (written by the agent from `CreateArgs.spawn`) holds the role's verdicts |
| `ws machine progress show` | the tree nests cross-env children with the `progress`/`rollup` that `machine.record` returned on the last poll (`null` if never polled) |

An id that is neither a local descendant nor in the ledger under the caller
stays `not_descendant` (exit 4) as today.

## 2. Flows

### 2.1 Local parent → remote child (on the controller machine)

1. CLI: `caller()`; read caller's `scope.json` (`envscope.Read`).
2. CLI checks `--env` and `--env-scope` against it (fast, clear error).
3. CLI → daemon `POST /v1/scope/spawn` with `ScopeSpawnArgs` (2.6),
   `workspace` = caller.
4. Daemon (`Controller.Scope(ctx, "local", "spawn", args)`): re-reads the
   caller's `scope.json` itself (ignores any claimed scope from a local
   caller), checks target ∈ scope, builds the child scope, calls
   `workspace.create` on the target with `scope`, `spawn` and request id =
   the request's, then appends the ledger entry.
5. CLI writes nothing locally; prints the reply.

### 2.2 Remote parent on A → child on B

1. CLI on A: caller, caller's `scope.json` on A, ancestors on A
   (`machine.Ancestors`), local precheck.
2. CLI → A's agent socket `POST /v1/scope/spawn` (same body).
3. A's agent checks the workspace exists on A (honest-A check; a refusal is
   `workspace_not_found`) and publishes `{"op": "scope.spawn", "args": ...,
   "request_id": <new>, "deadline": now+10m}` on `env/A/up`, response topic
   `env/A/req`, waits up to deadline + 15 s.
4. Controller `onUp` → `answer` → `Controller.Scope(ctx, A, "spawn", args)`:
   `ceiling(A, workspace, ancestors)` from the ledger; `effective = claimed ∩
   ceiling` (and always ∋ A); then as 2.1 step 4, ledger parent = `(A,
   workspace)`.
5. A's agent returns the reply body to the CLI; the CLI prints it.

`ceiling(E, w, ancestors)`: for `x` in `[w] + ancestors` (nearest first), the
first ledger entry with `environment == E && workspace == x` gives its scope;
none gives `{E}`. For E = `local` the ceiling is the caller's own
`scope.json` read by the daemon (no ledger).

### 2.3 The target environment B

`workspace.create` with `scope` and/or `spawn` (protocol 4):

- creates the workspace exactly as today, with `parent` = `null`;
- writes `state/<id>/scope.json` (`source: "granted"`, `parent` from the
  grant) **before** starting the session; if the write fails, the create
  fails `internal` and the workspace is removed;
- with `spawn`, writes `state/<id>/machine/<id>/spawn.toml` (it is its own
  tree's root on B) via `machine.Store.WriteSpawn`.

`machine.record` on B reads `machine.Store.Read(root, id)` and
`ReadProgress`, where root = `machine.Root(list, id)`.

### 2.4 Same-machine children (no `--env`)

Unchanged creation; afterwards (in the existing `started` callback) the CLI
writes the child's `scope.json` = the caller's scope (narrowed by
`--env-scope`, `source: "inherited"` or `"narrowed"`), **only if** the result
is wider than `{self}`. A missing or failed write leaves the child at
`{self}` (fail closed) with a warning.

### 2.5 Copy-back

When `wait` or `children` first sees a remote child's `result`, it writes,
under the **caller's tree root** on the caller's machine:

```
state/<root>/machine/remote/<env-id>/<child-id>/result.toml   (machine.Result, version 1)
state/<root>/machine/remote/<env-id>/<child-id>/spawn.toml    (machine.Spawn, when role != null)
state/<root>/machine/remote/<env-id>/<child-id>/report.md     (the report, ≤ 256 KiB, 0600)
```

`result.report` in JSON is that local `report.md` path. Later reads use the
copy; it is never overwritten (first copy wins). The copy is removed with the
root's state, as local records are.

### 2.6 Ledger cleanup (daemon, every 60 s)

For each ledger entry:

- its environment's record is gone (`ws env rm`) → drop;
- its environment is online with a snapshot ≤ 90 s old that doesn't list the
  child → drop;
- its parent is gone → if the child's environment is online, call
  `workspace.rm {"workspace": id, "keep_children": false}`; on ok or
  `workspace_not_found`, drop; else keep and retry next pass.
  "Gone": parent env `local` and the local workspace store says not found;
  parent env remote, online, fresh snapshot not listing it; parent env record
  removed.

## 3. Wire and state formats

### 3.1 `state/<id>/scope.json` (any machine)

```json
{
  "version": 1,
  "environments": [
    {"id": "env-1a2b3c4d", "name": "linux-server"},
    {"id": "env-2b3c4d5e", "name": "win-rig"}
  ],
  "self": "env-1a2b3c4d",
  "source": "granted",
  "parent": {"environment": "local", "workspace": "plan-9f8e7d6c"},
  "written_at": "2026-11-24T10:00:00Z"
}
```

- `source`: `granted` (person or `default_scope`), `inherited`, `narrowed`.
- `parent`: `null` except for a cross-environment child.
- Rules (reader, `envscope.Read`): file mode ≤ 0600 written atomically;
  size ≤ 16 KiB; unknown fields, `version != 1`, more than 64 entries, an id
  that isn't `local` or `envproto.ValidID`, or `self` not among the entries →
  error. Every caller treats an error as **`{self}` plus a warning** (fail
  closed). Missing file → `{self}`, no warning.

### 3.2 Ledger: `CONFIG_DIR/environments/scopes.json` (controller only)

```json
{
  "version": 1,
  "entries": [
    {
      "environment": "env-2b3c4d5e",
      "workspace": "helper-1a2b3c4d",
      "scope": ["env-1a2b3c4d", "env-2b3c4d5e"],
      "parent": {"environment": "env-1a2b3c4d", "workspace": "plan-9f8e7d6c"},
      "role": "implementer",
      "created_at": "2026-11-24T10:00:00Z"
    }
  ]
}
```

- `parent` `null` for a person grant (`ws claude --env --env-scope`).
- Written only by the daemon, under `environments/scopes.lock`
  (`internal/lockfile`), atomically, mode 0600. At most 4096 entries: a 4097th
  add fails `internal` ("scope ledger full") and the created workspace is
  removed. Key: `(environment, workspace)`; adding an existing key replaces it
  (idempotent retries).

### 3.3 `envproto` (protocol 4)

```go
const Protocol = 4
const ProtocolScopes = 4

const (
    OpMachineRecord = "machine.record"   // controller → agent, Since 4, 30 s
    OpScopeEnvs     = "scope.envs"       // agent → controller (up), 30 s
    OpScopeSpawn    = "scope.spawn"      // up, 10 min
    OpScopeChildren = "scope.children"   // up, 30 s
    OpScopeChild    = "scope.child"      // up, 2 min
)

type EnvRef struct {
    ID   string `json:"id"`   // "local" or ValidID
    Name string `json:"name"`
}

type WorkspaceAt struct {
    Environment string `json:"environment"` // "local" or ValidID
    Workspace   string `json:"workspace"`
}

type ScopeGrant struct {
    Environments []EnvRef    `json:"environments"` // 1..64, contains the target
    Parent       *WorkspaceAt `json:"parent"`
}

type SpawnInfo struct {
    Role  string            `json:"role"`
    Model string            `json:"model"`
    Pass  []string          `json:"pass"`
    Fail  []string          `json:"fail"`
    Vars  map[string]string `json:"vars"`
}

// CreateArgs gains two fields; both omitted when nil so a protocol-3 agent
// sees exactly today's bytes.
Scope *ScopeGrant `json:"scope,omitempty"`
Spawn *SpawnInfo  `json:"spawn,omitempty"`

type MachineRecordResult struct {
    Workspace       string          `json:"workspace"`
    Role            *string         `json:"role"`
    Result          *RecordResult   `json:"result"`
    Report          *string         `json:"report"`           // ≤ MaxRecordReportBytes
    ReportTruncated bool            `json:"report_truncated"`
    Progress        json.RawMessage `json:"progress"`         // ws machine progress JSON or null
    Rollup          json.RawMessage `json:"rollup"`           // or null
}
type RecordResult struct {
    Verdict    string    `json:"verdict"`
    Outcome    string    `json:"outcome"`
    RecordedAt time.Time `json:"recorded_at"`
}
const MaxRecordReportBytes = 256 << 10

// The scope.* up requests, and the bodies of the daemon's and agent
// socket's POST /v1/scope/<op> routes. Workspace is the caller; nil only
// for a person grant on the daemon socket.
type ScopeCallerArgs struct {
    Workspace *string  `json:"workspace"`
    Ancestors []string `json:"ancestors"` // nearest first, ≤ 64
    Scope     []string `json:"scope"`     // claimed ids, ≤ 64
}
type ScopeEnvsArgs struct{ ScopeCallerArgs }
type ScopeSpawnArgs struct {
    ScopeCallerArgs
    Target string      `json:"target"` // environment name
    Narrow []string    `json:"narrow"` // names, or null for "same as caller"
    Create CreateArgs  `json:"create"` // Scope must be nil; Parent must be nil
    Spawn  *SpawnInfo  `json:"spawn"`
}
type ScopeChildrenArgs struct {
    ScopeCallerArgs
    Descendants []string `json:"descendants"` // caller's descendants on its machine, ≤ 1024
}
type ScopeChildArgs struct {
    ScopeCallerArgs
    Environment  string `json:"environment"` // the child's, a name
    Child        string `json:"child"`
    Parent       string `json:"parent"`      // the child's ledger parent (caller or a descendant)
    Op           string `json:"op"`          // "status" | "logs" | "rm" | "record"
    Tail         int    `json:"tail"`
    KeepChildren bool   `json:"keep_children"`
}
type ScopeEnvsResult struct {
    Environments []ScopeEnv `json:"environments"`
}
type ScopeEnv struct {
    Name    string `json:"name"`
    ID      string `json:"id"`
    State   string `json:"state"`   // online | offline | removed
    Current bool   `json:"current"`
}
type ScopeChildrenResult struct {
    Children []ScopeChild `json:"children"`
}
type ScopeChild struct {
    Environment string          `json:"environment"` // name
    Workspace   string          `json:"workspace"`
    Parent      string          `json:"parent"`
    Role        *string         `json:"role"`
    Entry       json.RawMessage `json:"entry"`       // ws list JSON object from the snapshot, or null
}
```

`DecodeArgs` decodes each new op with `DisallowUnknownFields`, size ≤
`MaxRequestBytes`, and validates: ids `local`|`ValidID`, workspace ids as `validateWorkspace`
(existing) plus `machine` store's single-path-element rule (no `/`, `\`,
leading `.`), lists within the stated bounds, `op` in the
four values, `tail` 0..`MaxTail`. A `ScopeSpawnArgs.Create` with `scope` or
`parent` set is `usage`.

`LookupOp`: the four `scope.*` ops have `Caller: CallerAgent`; the
controller's `answer` dispatches `cert.renew` and `scope.*`. The agent
refuses `scope.*` from the controller (they aren't `CallerController`).

The agent's up request uses the op's `Timeout` (not the fixed
`RequestTimeout`) for its wait, plus `Grace`. `scope.spawn`'s request id is
reused as the `workspace.create` request id, so the target's D4 dedupe
makes a retried spawn create one workspace.

### 3.4 Local routes

Daemon socket (controller machine) and the agent socket (enrolled remote),
both `POST`, body = the args above, response 200 with an `envproto.Reply`
body (`{"ok", "result", "error"}`), so the CLI maps errors with the existing
`remoteReplyError`.

| Route | Daemon | Agent socket |
| --- | --- | --- |
| `/v1/scope/envs` | `Controller.Scope(ctx, "local", "envs", body)` | forwards as `scope.envs` |
| `/v1/scope/spawn` | as above; `workspace: null` allowed (person grant) | forwards; `workspace: null` → `usage` |
| `/v1/scope/children` | as above | forwards |
| `/v1/scope/child` | as above | forwards |

Agent socket path: `daemon.PathsFor(...).AgentSocket` = the daemon socket's
directory, file `<12 hex>-agent.sock`, created by `ws env agent run`, 0600 in a
0700 directory, every connection's peer uid checked (`peerListener`). The
agent removes it on exit.

## 4. Errors

| Code | Exit | When | `details` |
| --- | --- | --- | --- |
| `environment_out_of_scope` (**new**) | 4 | `--env` or an `--env-scope` name outside the caller's scope; also a controller refusal of `scope.spawn` | `{"environment": "<name>", "allowed": ["<name>", ...]}` (names, `local` first, then sorted) |
| `environment_unknown` | 3 | `--env`/`--env-scope` name not enrolled, or removed | `{"environment": "<name>"}` |
| `environment_offline` | 1 | target offline; or on a remote env the agent socket is missing / agent not connected (`reason: "agent_unreachable"`) | existing shape `{"environment", "request_id", "op", "reason"}` |
| `environment_outdated` | 1 | target's protocol < 4 for a create with `scope`/`spawn`, or `machine.record` | existing shape, `need_protocol: 4` |
| `not_descendant` | 4 | a ledger child whose ledger parent isn't on the caller's environment, or isn't the caller/descendant | `{"workspace": "<id>"}` |
| `usage` | 2 | `--env`/`--env-scope` on `ws claude`/`ws shell` inside a workspace; `--env local` from a remote | — |

Message for the new code: `environment <name> isn't in your scope
(<allowed, comma-separated>); ask a person to start you with --env-scope`.
Add it to `exitCode()` (exitRefused), `known()`, the `ws llm` overview row
for exit 4, and every guide's "Exit codes" table that can return it.

## 5. Threat model

Actors: **P**, the person (trusted). **W**, a workspace agent (untrusted; may
be prompt-injected) running as the user's uid on its machine. **A**, **B**:
enrolled remote machines; their env agent and all their workspaces share one
uid, so anything W on A can do, a compromised A can do. **C**, the controller
(trusted, holds the CA).

What each can forge:

- W can set any environment variable, pass any flag, edit any file the uid
  can write (including `CONFIG_DIR` on its machine), and call the local
  daemon socket or agent socket directly.
- A (or any W on A) can send any `env/A/up` message, with any claimed
  workspace, ancestors and scope. It **can't** publish as another environment
  (ACL: CN = topic id), can't read C's ledger, and can't make C skip the
  ledger check.

| # | Attack | Defence | Test |
| --- | --- | --- | --- |
| 1 | W runs `ws machine spawn --env-scope local,b` to get a wider child | `Narrow` refuses a superset → `environment_out_of_scope` | T5 failing-first |
| 2 | W runs `ws machine spawn --env b` with b outside its scope | CLI precheck and C's recheck → `environment_out_of_scope` | T5, T3 |
| 3 | W edits `config.toml` `default_scope` | read only for parentless person starts; children copy their parent's scope | T5 failing-first |
| 4 | W sets `WS_ENV_SCOPE`, `WS_ENVIRONMENT`, `WS_SCOPE` or similar | no scope code reads the environment; `WS_ENVIRONMENT` is read only by `ws shell` (record field) | T5 failing-first |
| 5 | W runs `ws claude --env b` or `ws claude --env-scope b` inside its workspace | refused `usage` | T5 failing-first |
| 6 | W deletes its `scope.json` | missing = `{self}`: narrows | T1 |
| 7 | W writes a malformed `scope.json` | invalid = `{self}` + warning | T1 |
| 8 | W on A forges `scope.spawn` claiming scope `{A, B}` while its ledger ceiling is `{A}` | effective = claim ∩ ceiling = `{A}` → refused | T3 failing-first |
| 9 | W on A claims an ancestor that has a wide ledger entry on **another** environment | ceiling only uses entries with `environment == A` | T3 failing-first |
| 10 | W on A runs `scope.child rm` on a child whose ledger parent is on B or `local` | `not_descendant` | T3 failing-first |
| 11 | W on A publishes on `env/B/up` | broker ACL refuses (existing) | existing `policy_test.go` |
| 12 | W on A sends `workspace.create` with a scope to B directly | agents can't publish on `env/B/req` (ACL) | existing |
| 13 | A replays an old `scope.spawn` | same request id → target's dedupe returns the stored reply; ledger add is idempotent | T3 |
| 14 | W on the controller machine calls the daemon socket `/v1/scope/spawn` with `workspace: null` (person grant) from `cd /tmp` | **not defended**: same uid ([0066](../../adrs/0066-runner-security-boundaries.md)). Documented residual; the CLI never does it from inside a workspace | — |
| 14b | W `cd`s out of its workspace, then runs `ws claude --env B` or `ws machine spawn` | **not defended**: "inside a workspace" is decided by the cwd (the enclosing-workspace check), so the process is treated as a person. Same uid ([0066](../../adrs/0066-runner-security-boundaries.md)). Documented residual; a follow-up could add an environment marker | — |
| 15 | W on the controller machine edits another workspace's `scope.json` or the ledger | **not defended** (same uid, 0066). The daemon re-reads files, so it narrows nothing extra | — |
| 16 | Compromised A uses wider scopes granted to other workspaces on A | bounded by the union of ledger scopes on A (inherent to one uid per machine) | — |
| 16b | W on A names another workspace on A as `Parent`, or lists its ids in `Descendants`, to read or remove that workspace's children | **not defended**: sibling isolation between workspaces on one environment is not a boundary. The controller bounds a forged claim only by A's ledger ceiling (same uid, 0066) | — |
| 16c | A stuck holder of the ledger lock stalls scope requests | **not defended**: ledger reads (`Get`, `List`, `Ceiling`) use `context.Background()` and can block while a writer holds the lock; writes take the request context | — |
| 17 | B lies in `machine.record` or snapshots about a child | affects only that child's reported result, already B's to report; reports are capped at 256 KiB and stored as data, never executed | T4 |

Hostile-input limits (reviewers check these): every new args type ≤
`MaxRequestBytes`; lists ≤ 64 (scope, ancestors), ≤ 1024 (descendants);
`scope.json` ≤ 16 KiB; report ≤ 256 KiB (truncated with
`report_truncated: true`); ledger ≤ 4096 entries; ids validated before any
path is built from them (`env-id` and workspace ids go into
`state/<root>/machine/remote/<env-id>/<child-id>/`).

## 6. Docs to change

- `internal/cli/guides/ws.md`: replace "You never choose an environment."
  with: "You may start a child on another environment only with `ws machine
  spawn --env <name>`, and only on one in your scope (`ws machine envs`); a
  person grants the scope." Add `ws llm machine envs` to the overview, and
  `environment_out_of_scope` to the exit-4 row.
- `internal/cli/guides/ws-machine.md` (synopsis, "When to use it"),
  `ws-machine-spawn.md` (`--env`, `--env-scope`, JSON, exit codes),
  new `ws-machine-envs.md`, `ws-machine-wait.md`/`-children.md`/`-logs.md`/
  `-rm.md` (remote children, copy-back, `environment` field),
  `ws-claude.md` (`--env-scope`, the refusal inside a workspace),
  `ws-shell.md` (refusal).
- `docs/tools/ws-machine.md`, `docs/tools/ws-claude.md`, `docs/tools/ws-env.md`
  (scopes section), `docs/state.md` (`scope.json`, ledger,
  `machine/remote/`), `docs/config.md` (`default_scope`), `README.md`
  (one paragraph under remote environments).
- ADR index already has 0082 (Proposed).
