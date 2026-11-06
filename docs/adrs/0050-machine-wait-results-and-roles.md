# 0050. Agents wait on children with `ws machine wait`, children record a verdict with `ws machine result`, and `ws machine spawn --role` starts a child from a stored role

- Status: Proposed
- Date: 2026-11-06

## Context

### Problem

An orchestrator agent builds a feature through a tree of child agents: a
designer, then an implementer, a reviewer, and finally a verifier per
task (the handbook's "multi-agent feature build"). `ws machine`
([0046](0046-ws-machine.md)) lets it start, list and remove children, but
not the three other things it needs, so it works around them with zsh
scripts:

- `wait.sh` polls `claude agents --json --all` through `jq` every 30 s
  until each session isn't `busy`. That is outside `ws machine`, which
  agents are told to use alone.
- Children report by writing a file into the orchestrator's job tmp dir
  with the verdict on line one (`PASS`, `FAIL`, `APPROVED`,
  `CHANGES_REQUESTED`, `FLAKY`). The path is passed in the prompt, nothing
  checks the verdict, and the file isn't tied to the child.
- `worker.sh`, `review.sh` and `verify.sh` each hold a long prompt and a
  model, which every run copies from the handbook.

How should an agent wait for its children, how should a child hand back a
verdict and a report, and how should the prompts and models of recurring
child roles be stored, all as `ws machine` commands?

### What a solution needs

1. **Only `ws machine`.** Everything an orchestrator or a child runs is a
   `ws machine` subcommand or flag, scoped to the caller's tree like the
   existing ones.
2. **A verdict that can't be lost.** A child's verdict and report outlive
   the child's workspace, so the orchestrator can remove a child before it
   has read everything, and disappear with the tree, so nothing piles up.
3. **Fail closed.** A child that stops without a verdict, or with a verdict
   that doesn't mean "pass", makes `wait` exit non-zero.
4. **No false "done".** `wait` doesn't return while a child is between
   turns but still working through its own children.
5. **Roles managed like review profiles** ([0042](0042-review-profile-management.md)):
   built-ins that can be overridden and restored, plus the user's own.
6. **Same contract** ([0016](0016-output-contract.md)): JSON on stdout,
   stable error codes, multi-item results with per-item errors.

### Relevant facts

- A session's state is ws's reading of Claude Code's `pid`, `status` and
  `startedAt`: `working`, `idle`, `starting`, `stopped`, or `unknown`
  (`internal/lifetime/lifetime.go`). `working` means Claude Code's
  `status` is `busy`, that is, mid-turn (`docs/state.md`). An orchestrator
  waits on a child by running a background command and ending its turn,
  to be woken by the command's notification[^procedure], so between turns
  it is not `working` although its children are.
- `ws review wait` already waits for one agent: it polls `lifetime.List`
  every 5 s, is done at `idle`, `stopped` or a removed workspace, and fails
  with `review_timeout` (exit 1) after `--timeout`, default 60 minutes
  (`internal/cli/review_report.go`).
- Removing a workspace deletes `state/<id>/` (`docs/state.md`, "Every
  removal"), and removing a workspace removes its descendants first
  ([0044](0044-workspace-trees.md)). So a directory under the **root's**
  `state/<root>/` lives exactly as long as the tree.
- Review profiles are Markdown files with front matter in
  `CONFIG_DIR/review/profiles/`, over built-ins embedded in the binary;
  editing a built-in writes an override, and removing the override
  restores it ([0042](0042-review-profile-management.md),
  `internal/review/profile`).
- `ws loop` renders prompts with `text/template` and
  `Option("missingkey=error")`, with `{{.Vars.KEY}}`; `{{index .Vars "K"}}`
  renders an absent key as empty instead of failing; keys are
  `[A-Za-z_][A-Za-z0-9_]*`, at most 64 bytes, values at most 4096 bytes, at
  most 32 vars (`internal/loop`, [0049](0049-loop-scripts-and-variables.md)).
- `ws machine spawn` passes everything after `--` to `claude`, the first
  argument being the prompt (`internal/cli/machine.go`).
- In three orchestrated runs, all 17 reviews and every implementer
  reported through the file convention, a verdict on line one of a file
  in the orchestrator's job tmp dir[^procedure].

## Decision

We will add three things to `ws machine`.

```
ws machine wait [--any] [--timeout <D>] <WORKSPACE>...
ws machine result <VERDICT> [--file <PATH> | --file -]
ws machine spawn --role <NAME> [--var KEY=VALUE]... [OPTIONS] [<SPEC>...] [-- [<PROMPT>] [<CLAUDE ARGS>...]]
ws machine roles [show <NAME> | add <NAME> … | edit <NAME> … | rm <NAME>]
```

The full reference, every JSON shape and every error, is
[`docs/tools/ws-machine.md`](../tools/ws-machine.md).

### 1. Results live under the root's state

A child's records live in `state/<root>/machine/<child-id>/`, where
`<root>` is the top of the caller's tree: follow `parent` until a
workspace has none, or its parent isn't a workspace any more. The
directory holds:

- `spawn.toml`, written by `ws machine spawn --role` right after the child
  starts: `version = 1`, `role`, `model`, `pass`, `fail`, `vars`,
  `ancestors` (parent first, root last) and `spawned_at`;
- `result.toml`, written by `ws machine result`: `version = 1`, `verdict`,
  `ancestors`, `recorded_at`, and `report` (true when `report.md` exists);
- `report.md`, the report, copied in.

Each is written to a temp file and renamed into place. Each child writes
only its own directory, so there is no lock. The directory is deleted
with the root's `state/<root>/`, and nothing else deletes it: a child
removed with `ws machine rm` keeps its result until the root goes.

### 2. `ws machine result` records the caller's verdict

The caller records its own verdict; there's no flag to record another's.
`VERDICT` is 1-32 characters of `[A-Za-z0-9_]` starting with a letter, and
is stored uppercase. If the caller was spawned with a role (its
`spawn.toml` exists), the verdict must be one of the role's `pass` or
`fail` verdicts, or it is `result_invalid` and the message lists them.
Without a role, any well-formed verdict is accepted. `--file` copies a
report of 1 byte to 1 MiB (`-` reads stdin). Running it again replaces the
verdict and the report; without `--file` the old report is deleted.

### 3. Pass and fail are decided when the result is read

A result's **outcome** is `pass` when its verdict is in the pass list,
`fail` otherwise. The lists are the role's, from `spawn.toml`, or for a
child without a role the defaults: pass `PASS`, `APPROVED`; fail `FAIL`,
`CHANGES_REQUESTED`. A verdict in neither list is `fail`. Deciding at read
time means a result recorded a moment before `spawn.toml` is written is
still judged by the role.

### 4. `ws machine wait`

Each `WORKSPACE` is an id, path, or agent session id, as for
`ws machine rm`, and must be the caller's descendant
(`not_descendant`, exit 4). An id that is no longer a workspace but has a
directory under the caller's root whose `ancestors` include the caller is
a **gone** child, and its result is still read. Anything else is
`workspace_not_found` (exit 3). These are checked before waiting, and
fail the whole command.

Every 5 s, `wait` reads `lifetime.List` and the results. A child is
**finished** when its workspace is gone, or when it is neither `working`,
`starting` nor `unknown`, none of its descendants is `working` or
`starting`, and either it has recorded a result or it was also not busy at
the previous poll. The second poll is what stops a child between turns
from counting as finished. `wait` returns when every child is finished,
or with `--any` when one is, or at `--timeout` (default 60 minutes).

It prints one object with an item per child: its status, `finished`,
`outcome` (`pass`, `fail`, `missing` for a finished child with no result,
`pending` for an unfinished one), and the result. Like `ws rm`, each item
has an `error` and the exit code is the first failure's:

- a finished child whose outcome is `fail` or `missing` is `child_failed`
  (exit 1);
- at the timeout, each unfinished child is `wait_timeout` (exit 1); with
  `--any`, unfinished children are not errors unless nothing finished.

So exit 0 means every child waited for finished and passed.

### 5. `ws machine children` shows results

Each workspace in its output gains `role` (a name or `null`) and `result`
(`null`, or `{verdict, outcome, report, recorded_at}`), read from the
caller's root. `ws list` is unchanged.

### 6. Roles

A role is `CONFIG_DIR/machine/roles/<name>.md`: front matter with
`description` (required, at most 120 characters), `model` (empty for
`claude`'s default), `pass` and `fail` (non-empty, disjoint verdict
lists), `vars` (the var names the prompt uses) and `claude_args` (more
arguments for `claude`, without `--model` or `--settings`), then the
prompt template as the body. Names are lowercase letters, digits and
hyphens. `ws` builds in three, written from the handbook's scripts, which
record their verdict with `ws machine result` instead of writing files:

| Role          | Model    | Pass            | Fail                | Vars                                   |
| ------------- | -------- | --------------- | ------------------- | -------------------------------------- |
| `implementer` | `haiku`  | `PASS`          | `FAIL`              | `TASK`, `TASKS`, `BASE`, `BRANCH`      |
| `reviewer`    | `sonnet` | `APPROVED`      | `CHANGES_REQUESTED` | `TASK`, `TASKS`, `BASE`, `KNOWN_ISSUES` (optional) |
| `verifier`    | `haiku`  | `PASS`, `FLAKY` | `FAIL`              | `BRANCH`, `CHECKS`                     |

Each built-in passes `claude_args = ["--permission-mode", "auto"]`.

**Management** mirrors `ws review profiles` and lives under
`ws machine roles`, because `ws machine` is the namespace agents may run
and an orchestrator needs to list and show roles: `roles` lists them with
`source` (`builtin`, `user`, `override`), `show` prints one with its
prompt, `add` writes a new one (`role_exists`, exit 4, if the name is
taken), `edit` changes the given fields (a built-in is copied to an
`override` first), and `rm` deletes the user's file (restoring the
built-in for an override; `role_builtin`, exit 4, for a bare built-in).
Every write is a temp file, parsed back, then renamed, under a lock on the
directory. `add` and `edit` also render the prompt with every declared var
set, so a prompt using an undeclared var is `role_invalid`. Roles are the
user's configuration, not part of a tree, so `ws machine roles` is the one
`ws machine` command that works outside a workspace. `ws llm` tells
agents to list and show roles, and to add, edit or remove one only when a
person asks.

**Spawning.** `--role R` renders R's prompt with `text/template` and
`missingkey=error`, with `Role` (the name) and `Vars` (from `--var`). A
`--var` whose key isn't in the role's `vars`, a duplicate key, or a render
error (such as a missing `{{.Vars.TASK}}`) is `usage`, before anything is
created. The prompt after `--` is optional with a role; when it isn't
blank it is appended after a blank line, verbatim. The arguments passed to
`claude` are: the prompt, `--model <model>` when the role has one, the
role's `claude_args`, then the caller's arguments after the prompt, so a
`--model` there overrides the role's. `--var` without `--role` is `usage`.
The output gains `role`. A failure to write `spawn.toml` is a warning; the
child is already running.

### Why this option

1. **Only `ws machine`.** Waiting, recording, and role management are all
   `ws machine` subcommands, scoped to the caller's tree, so the scripts
   and the `claude agents | jq` loop go away.
2. **Results outlive the child, not the tree.** Putting them under the
   root's `state/` reuses the one directory whose lifetime already matches
   the tree's; nothing new has to be cleaned up.
3. **Fail closed.** No result is `missing`, and an unknown verdict is
   `fail`, so `wait`'s exit 0 means every child said "pass".
4. **No false "done".** Requiring idle descendants and two quiet polls
   (or a recorded result) covers a child waiting on its own children, at a
   cost of up to 5 s per wait.
5. **Like profiles.** Same file layout, same override rules, same
   commands, so nothing new to learn; the role file is plain Markdown a
   person can edit.
6. **Same contract.** `wait` is a multi-item command with per-item errors,
   like `ws rm`; new codes fit [0016](0016-output-contract.md)'s exit codes.

Where it's weak: a child stopped by a usage limit is `idle`, so `wait`
reports it `missing` rather than waiting for `ws continue`; the
orchestrator has to tell a person. A people-only `ws rm --keep-children`
that makes a child a root leaves its earlier result under the old root,
where it is deleted with that root. The verdict lists in `spawn.toml` are
a snapshot, so editing a role doesn't change children already running. An
agent can edit roles; only `ws llm` asks it not to.

## Alternatives

### Results in the caller's own `state/<id>/`

**Pros:** no need to find the root.
**Cons:** deleted with the child, so the orchestrator must read the
result before `ws machine rm`, and a grandchild's result is gone with its
parent. The root's directory outlives every child, as the problem asks.

### Pass and fail declared on the command line (`ws machine result --fail`)

**Pros:** no role needed.
**Cons:** the child decides what counts as failure, and a child that
writes `FLAKY` or a typo passes. Declaring the lists on the role keeps the
meaning with whoever wrote the prompt.

### Role management outside `ws machine` (`ws roles`)

**Pros:** keeps editing config away from agents.
**Cons:** an orchestrator couldn't list or show roles to choose one, and
roles would sit apart from the only command that uses them.

### `wait` returns at the first idle

**Pros:** what `ws review wait` and `wait.sh` do; simpler.
**Cons:** returns while a child waits on its own children, which is the
case an orchestrator tree creates.

## Consequences

- Amends [0046](0046-ws-machine.md) (new `ws machine` subcommands),
  [0016](0016-output-contract.md) (`result_invalid` and `role_invalid`
  under exit 2, `role_unknown` under exit 3, `role_exists` and
  `role_builtin` under exit 4, `child_failed` and `wait_timeout` under
  exit 1), [0011](0011-config-dir.md) (`CONFIG_DIR/machine/roles/`) and
  [0012](0012-workspace-layout.md) (`state/<root>/machine/`).
- `docs/tools/ws-machine.md`, `docs/state.md`, `docs/config.md`, and the
  `ws llm` guides describe it; the handbook's procedure replaces
  `wait.sh`, `worker.sh`, `review.sh` and `verify.sh` with
  `ws machine spawn --role` and `ws machine wait`. `merge.sh` stays.
- Conformance: Go tests that `result` writes under the root and validates
  role verdicts; that outcomes follow the role's or the default lists;
  that `wait` returns only when the child and its descendants are quiet
  for two polls or it has a result, honors `--any` and `--timeout`, sets
  `child_failed` and `wait_timeout`, reads gone children, and refuses
  non-descendants; that `children` carries `role` and `result`; that roles
  are listed, shown, added, edited, overridden and restored; and that
  `spawn --role` renders the prompt and orders the `claude` arguments.
- **Unverified:** whether two 5 s polls are enough for a woken child to
  show `working` again on a loaded machine; to be checked on the first
  orchestrated run that uses it, and the count raised if not. Also
  unverified: that `claude` takes the last of two `--model` flags, which
  lets a caller's `--model` override the role's; T6 tests the argument
  order, and the first run with an override should confirm the model in
  `claude agents --json`.

### Follow-ups (out of scope)

- **Load-aware spawning**: hold `spawn` while the machine's load average
  is high (run 2 lost an hour to Spotlight load).
- **Cost reporting**: tokens and estimated cost per child, read from the
  transcripts, in `wait` or `children`.
- **Removing workspaces by name pattern** (`ws machine rm --prefix`), which
  `merge.sh` does with `jq`.
- **Running a whole task graph**: a task file with dependencies that `ws`
  schedules itself.
- **Usage limits in `wait`**: report a child stopped by a usage limit as
  `limited` and keep waiting, instead of `missing`.

## References

[^procedure]: Agent Handbook, "Procedure: multi-agent feature build," <https://docs.bobcob7.com/doc/procedure-multi-agent-feature-build-Q593wbigCg> (accessed 2026-11-06). "Wait with `wait.sh <session>` as a **background** command. You're notified when it finishes, so don't poll." And: "All 17 reviews across three runs approved on the first pass."
