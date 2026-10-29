# 0046. Agents manage their child workspaces with `ws machine`; the top-level commands are for people and the desktop app

- Status: Proposed
- Date: 2026-10-28

## Context

### Problem

Agents use the same commands as people: `ws claude --json` to start a
helper, `ws list --json --parent <id>` to find it, and `ws rm` to remove
it ([0014](0014-command-set.md), [0044](0044-workspace-trees.md)). Those
commands are built for a person who may act on any workspace on the
machine, so an agent has to remember `--json`, look up its own workspace
id, and keep to "don't remove a workspace you didn't create", which
nothing enforces. How should agents create, list, and remove the
workspaces they start, so the rules are enforced rather than remembered,
and where should commands meant for agents live?

### What a solution needs

1. **Scoped to the caller.** An agent can only list and remove its own
   descendants, and can't reach another agent's workspaces, or its own,
   by mistake.
2. **Nothing to remember.** No `--json`, no parent id, no `--parent`: the
   defaults are the right ones for an agent.
3. **The same contract** ([0016](0016-output-contract.md)): the same JSON
   objects, error codes, and exit codes as the commands they wrap, so
   nothing new has to be learned to parse them.
4. **One place for agent commands.** The top-level commands stay the
   ones a person or the desktop app runs, and `ws llm` sends agents to
   one namespace.

### Relevant facts

- `ws claude`, `ws review start`, and `ws list --parent` already find the
  workspace `ws` runs in from the working directory
  (`enclosingOr` in `internal/cli/review_start.go`), and a workspace
  started from inside another is its child ([0044](0044-workspace-trees.md)).
- `ws rm` removes a workspace's descendants first, reports each as an
  item with `parent`, and supports `--dry-run` and `--keep-children`
  ([0044](0044-workspace-trees.md)). Nothing stops it removing any
  workspace, including the caller's own.
- `ws claude` with no prompt after `--` attaches the terminal to the new
  session ([0025](0025-ws-claude-session-hand-off.md)); an agent's
  `ws claude` has no terminal, so it must always pass a prompt.
- `ws llm`'s rules tell agents to parse `--json`, to remove only what
  they created, and to `ws rm` children once used
  (`internal/cli/guides/ws.md`).
- Prior art: git splits its commands into "porcelain", for people, and
  "plumbing", whose output is stable for scripts[^git].

## Decision

We will add `ws machine`, a namespace of commands for agents, scoped to
the workspace they run in. Its first commands start, list, and remove
that workspace's children.

```
ws machine spawn [--name <n>] [--offline] [--hidden] [<spec>...] -- "<prompt>" [<claude args>...]
ws machine children [--all]
ws machine rm [--dry-run] [--keep-children] (<WORKSPACE>... | --all)
```

**The caller.** Every `ws machine` command acts for the workspace
containing the working directory, found as `enclosingOr` finds it.
Outside a workspace, it fails with `outside_workspace` (exit 2). There is
no flag to act for another workspace.

**Output.** `ws machine` always prints JSON, as if `--json` were given;
`--json` is accepted and changes nothing. Errors are JSON on stderr.

- **`ws machine spawn`** is `ws claude --json --parent <caller>`. The
  prompt is required: nothing after `--` is a `usage` error, never an
  attached terminal. It prints `ws claude --json`'s object, with
  `parent`.
- **`ws machine children`** prints `ws list --json -a`'s object for the
  caller's children: direct children, or every descendant with `--all`,
  hidden ones (such as reviews) included, oldest first.
- **`ws machine rm`** is `ws rm`, but every target must be a descendant
  of the caller. Any other workspace, the caller itself included, is
  refused as its item's error, `not_descendant` (exit 4), and the other
  targets are still removed. `--all` removes every direct child, and
  with them their descendants. `--keep-children` makes the target's
  children the caller's, not top-level, so they stay in its subtree. Its
  output is `ws rm --json`'s.

**Guidance.** `ws llm`'s rules tell agents to use only `ws machine`,
apart from `ws review` to get their work reviewed, and to tell a person
when `ws machine` can't do what they need rather than reach for another
command. `ws llm machine` documents it, and `ws llm` lists its commands
for agents apart from those for people. Every other command's guide says
it is for people and the desktop app; those for `ws claude`, `ws list`,
and `ws rm` point agents to their `ws machine` counterparts. The
workspace root's `CLAUDE.md` says the same. The top-level commands keep
working for agents; nothing is removed or blocked.

**`ws --help`** lists `machine` in its own group, "Commands for agents",
and the top-level commands but `review` and `llm` in "Commands for people
and the desktop app". The help for `ws`, `ws claude`, `ws list`, and
`ws rm` sends agents to `ws machine`.

**What else goes here.** Later commands written for agents, and only for
them, go under `ws machine`. `ws review start`, `wait`, and the reviewer's
commands stay where they are; moving them is a separate decision.

### Why this option

1. **Scoped.** `rm` refuses anything outside the caller's subtree, and
   `children` shows nothing else, so the rule in `ws llm` becomes a
   check. A confused agent can't remove itself or a sibling.
2. **Nothing to remember.** The parent is implicit, JSON is the default,
   and `spawn` can't hang on an attach.
3. **Same contract.** Each command prints the object of the command it
   wraps; the two new error codes follow [0016](0016-output-contract.md)'s
   exit codes.
4. **One namespace.** `ws machine` separates the agent's commands the way
   git separates plumbing, and leaves room for more without crowding
   `ws --help`.

Where it's weak: the caller is still found from the working directory,
so an agent whose shell has moved outside its workspace (`cd /tmp`) gets
`outside_workspace` and must `cd` back. And there are now two ways to do
the same thing; until the top-level guides stop mentioning agents, an
agent may still reach for `ws claude`.

## Alternatives

### Keep the top-level commands, add an `--own` check to `ws rm`

**Pros:** no new commands.
**Cons:** agents must remember another flag, and `--json`, and the
parent id for `ws list --parent`; nothing gets enforced by default.

### `ws children` or `ws subagents`

**Pros:** say what the commands act on.
**Cons:** narrower than a home for every agent-facing command; and
"subagents" already means Claude Code's in-process subagents, which an
agent would confuse with these.

### Detect agents and change the top-level commands' defaults

Switch `ws claude`, `ws list`, and `ws rm` to JSON and scoped behavior
when run inside a workspace.

**Pros:** one set of commands.
**Cons:** a person in a `ws shell` would get the agent behavior; the
same command would behave two ways depending on where it runs.

## Consequences

- Amends [0014](0014-command-set.md) (`ws machine` and its commands) and
  [0016](0016-output-contract.md) (`outside_workspace` under exit 2,
  `not_descendant` under exit 4).
- `docs/tools/ws-machine.md`, `docs/tools/README.md`, and `ws llm` (the
  overview, `ws llm machine`, and every other command's guide) describe
  it.
- An agent that needs something `ws machine` lacks, such as continuing a
  child stopped by a usage limit or starting work meant to outlive it,
  asks a person until a `ws machine` command covers it.
- Conformance: Go tests that each `ws machine` command fails with
  `outside_workspace` outside a workspace; that `spawn` sets the parent
  to the caller and refuses a missing prompt; that `children` lists only
  the caller's children, or descendants with `--all`, hidden included;
  that `rm` refuses the caller, its ancestors, and unrelated workspaces
  with `not_descendant` while removing the rest; and that JSON is printed
  without `--json`. The existing guide test covers `ws llm machine`.
- **Unverified:** whether agents reliably pick `ws machine` over
  `ws claude` once `ws llm` points to it; to be checked by watching the
  first agents that start helpers after it ships.

## References

[^git]: git, "git(1): Low-level commands (plumbing)." <https://git-scm.com/docs/git#_low_level_commands_plumbing> (accessed 2026-10-28). "Although Git includes its own porcelain layer, its low-level commands are sufficient to support development of alternative porcelains."
