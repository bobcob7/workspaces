# 0082. A person grants each agent an environment scope; `ws machine spawn --env` may start a child on any environment in it, checked by the controller from its own ledger, and a child's scope is never wider than its parent's

- Status: Proposed
- Date: 2026-11-24

## Context

### Problem

[0072](0072-remote-environments.md) keeps a workspace tree on one machine:
`ws machine spawn` always starts the child where the parent runs, and "Let a
child pick another environment" was left for its own ADR. A person now wants
an agent to start children on other enrolled environments (an agent on
`linux-server` starts a helper on `win-rig`, and each reports its private IP),
but only on environments the person allowed. Who decides which environments an
agent may use, where is that stored, and how does an environment that receives
the request know the claim is true?

### What a solution needs

1. **Only a person widens a scope**: by `config.toml` or when starting an
   agent. Nothing an agent runs, sets or edits through `ws` makes its scope,
   or any descendant's, wider.
2. **A remote environment never trusts a workspace's claim.** A scope reaches
   another machine only over the authenticated controller channel
   ([0074](0074-mqtt-transport-embedded-broker.md),
   [0075](0075-environment-enrollment-and-certificates.md)), and the
   controller checks it against what it granted.
3. **Default unchanged**: with no scope granted, a workspace's scope is its own
   environment, and every command, record and test behaves as today.
4. **Children elsewhere stay manageable**: `wait`, `children`, `logs`, `rm`,
   `result` and `progress` work for a child on another environment, and its
   report stays readable after it is removed.
5. **Clear failures** for an environment out of scope, offline, unknown or
   removed.

### Relevant facts

- The controller is the `ws` daemon that holds the broker and the CA
  ([0074](0074-mqtt-transport-embedded-broker.md)). An agent's MQTT client id
  must equal its certificate's CN, the environment id, and the ACL lets it
  publish only under `ws/v1/env/<its id>/`[^policy]. So the controller knows
  which environment sent a message; it can't know which workspace on that
  machine did.
- An agent already sends requests to the controller on `env/<id>/up` with the
  reply on `env/<id>/req`; the only such operation is `cert.renew`[^up].
- A workspace's Claude Code session runs as the same OS user as `ws`, its
  daemon and its env agent, with `--permission-mode auto` for the built-in
  roles. [0066](0066-runner-security-boundaries.md) states the boundary is the
  user's account, not a sandbox. The daemon socket accepts any process of that
  uid[^peercred].
- `ws claude --env <name>` run inside a workspace today starts an unparented
  remote workspace with only a warning; `ws review --env` inside a workspace
  is refused ([0080](0080-reviews-and-loops-on-environments.md), R5)[^claude-env].
- `ws machine` results live under the tree root's state
  (`state/<root>/machine/<child>/`) and are read from files on one
  machine ([0050](0050-machine-wait-results-and-roles.md)).
- `workspace.create`'s arguments are decoded with unknown fields refused, and
  each op has a `Since` protocol; the current protocol is 3[^proto].

## Decision

We will add **environment scopes**: the set of environments (by id, plus
`local` for the controller's machine) a workspace may start children on.

**Granting (people only).**

- `ws claude [--env <e>] --env-scope <name>[,<name>...]` grants a scope to the
  agent a person starts. Without `--env-scope`, `[environments] default_scope`
  in `config.toml` (a list of names, default empty) applies. The workspace's
  own environment is always in its scope.
- `--env-scope`, and `--env`, on `ws claude` and `ws shell` are refused with
  `usage` inside a workspace (the R5 rule of 0080, extended). An agent never
  grants a scope or picks an environment with a person's command.
- Names are resolved to environment ids when granted, so a removed and
  re-added environment of the same name is not in an old scope. An unknown
  name is `environment_unknown`.

**Storing.**

- Each workspace's scope is `state/<id>/scope.json`, outside its tree, written
  by `ws` when the workspace is created. A missing file means "its own
  environment only", so deleting it only narrows.
- The controller keeps a **ledger**, `CONFIG_DIR/environments/scopes.json`, of
  every workspace it created on a remote environment with a scope, and every
  cross-environment child: its environment, id, scope, and parent
  `(environment, workspace)`. The ledger, not anything a remote sends, is the
  authority for remote workspaces.

**Spawning.** `ws machine spawn --env <name> [--env-scope <names>]` starts the
child on that environment if it is in the caller's scope, else fails with
`environment_out_of_scope` (exit 4, details list the allowed environments).
The child's scope is the caller's, or the narrower `--env-scope`; a wider one
is `environment_out_of_scope`. Without `--env` nothing changes: the child is on
the caller's machine and inherits (or narrows) the caller's scope.

- On the controller's machine, `ws` asks the daemon, which checks the caller's
  `scope.json`, sends `workspace.create` with the child's scope to the target,
  and writes the ledger.
- On a remote environment A, `ws` asks A's env agent over a new local socket
  (uid-checked, like the daemon's), which forwards `scope.spawn` on
  `env/A/up`. The controller computes the allowed set as **the claim
  intersected with the ledger's ceiling for A**: the scope of the nearest
  ancestor of the caller that the ledger lists on A, or just `{A}` when none
  is. A forged claim from A can never reach past what the controller granted
  to workspaces on A.
- The target receives the scope in `workspace.create` from the controller, over
  mTLS, and stores it. Protocol 4; a scope or role sent to an older agent is
  `environment_outdated`.

**Children elsewhere.** `ws machine children`, `wait`, `logs` and `rm` include
cross-environment children, found in the ledger. The controller allows an
operation on such a child only from the environment its ledger parent is on.
A new `machine.record` op returns the child's role, verdict, progress and
report (at most 256 KiB); `ws machine wait` and `children` copy a finished
child's result and report into the caller's own state, so they stay readable
after `ws machine rm`. When a ledger parent is gone, the daemon removes its
cross-environment children within a minute of noticing.

**Seeing it.** `ws machine envs` lists the caller's scope with each
environment's state (`online`, `offline`, `removed`), for agents.

**Not in this ADR:** `ws review start --env` (reviews stay where their
requester runs, 0080 R5); the desktop tree showing cross-environment parents.

### Why this option

1. Criterion 1: every widening path an agent has through `ws` is closed:
   `--env-scope` only narrows on `spawn` and is refused on `ws claude` inside
   a workspace; a child never reads `config.toml` for its scope; no
   environment variable is read. Where it's weak: a process of the user's uid
   can write `CONFIG_DIR` or call the daemon socket directly from outside a
   workspace. That is [0066](0066-runner-security-boundaries.md)'s boundary
   and isn't changed here; the tests pin the `ws` surfaces.
2. Criterion 2: the controller identifies the sending environment by its
   certificate and bounds every claim by its own ledger. A compromised
   machine A can use at most the scopes the person granted to workspaces on A.
3. Criterion 3: absent file = own environment; the old `workspace.create`
   path is used when no scope or role is sent.
4. Criterion 4: the ledger gives the parent link that 0072 kept on one
   machine; copy-back gives the "readable after rm" property of 0050.
5. Criterion 5: existing codes plus one new one.

## Alternatives

### Each workspace holds a signed capability token

The controller signs `(environment, workspace, scope)` and the agent presents
it. **Pros:** no ledger lookup. **Cons:** every process on A can read the
token anyway (same uid), so it adds no bound beyond the ledger's; it needs key
handling and expiry; revocation needs a ledger regardless.

### Trust the remote's stored scope

**Pros:** no controller state. **Cons:** any workspace on A could claim any
scope; fails criterion 2.

### Let agents call `ws claude --env` (status quo)

**Pros:** no new command. **Cons:** no scope, no parent link, no way to wait
on or remove the child; fails criteria 1 and 4.

## Consequences

- Amends [0072](0072-remote-environments.md) (a tree may span environments,
  through the ledger; `environment_mismatch` still applies to `--parent`),
  [0016](0016-output-contract.md) (`environment_out_of_scope`, exit 4),
  [0014](0014-command-set.md) (`ws machine envs`, `--env`/`--env-scope` on
  `ws machine spawn`, `--env-scope` on `ws claude`),
  [0073](0073-environment-agent-is-ws.md) (`machine.record`, the `scope.*` up
  operations, the agent socket, protocol 4) and
  [0080](0080-reviews-and-loops-on-environments.md) R5 (extended to
  `ws claude`/`ws shell`).
- The `ws llm` rule "You never choose an environment" becomes "only within
  your scope, with `ws machine spawn --env`".
- Conformance: failing-first Go tests that a child can't widen by flag, by
  `config.toml` edit or by environment variable; that a forged `scope.spawn`
  from A outside A's ceiling is refused; that A can't act on a child whose
  ledger parent is on another environment; that with no grant every spawn is
  local and unchanged. The design and tasks are in
  [docs/design/env-scope](../design/env-scope/design.md).
- Harder: a second store (the ledger) to keep consistent with snapshots; a
  remote child's state reaches its parent only through polling (10 s).
- Residual: "inside a workspace" is decided by the working directory, so a
  process that `cd`s out of its workspace is treated as a person and can
  grant a scope (same uid, [0066](0066-runner-security-boundaries.md)). A
  follow-up could mark the environment of agent processes.
- **Unverified:** the cross-machine flow against real agents (the pod has no
  remote environment; tests use `envtest` and fakes); that the cleanup of a
  gone parent's children is timely when the child's environment is offline
  for long (entries wait for it to come back).

## References

[^policy]: `internal/environment/policy.go` at `0763608`, `connect` and `allowed`: "Publish: <id>/status, <id>/snapshot, <id>/up, <id>/res/<x> … for the peer's own id (= CN) only."
[^up]: `internal/environment/controller.go` at `0763608`, `onUp` and `answer`; `internal/envagent/renew.go`, `requestCertificate`.
[^peercred]: `internal/daemon/server.go` at `0763608`, `peerListener`: "accepts only connections from uid".
[^claude-env]: `internal/cli/env_call.go` at `0763608`, `parentOnEnv`: "The default parent, the local workspace ws runs in, is dropped with a warning"; `internal/cli/review_env.go`, `reviewEnv`.
[^proto]: `internal/envproto/payloads.go` at `0763608`: `const Protocol = 3`; `internal/envproto/ops.go`, `DecodeArgs` uses `DisallowUnknownFields`.
