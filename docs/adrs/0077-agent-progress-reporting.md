# 0077. Agents report their own steps and a short name with `ws machine progress`, and the workspace tree nests them

- Status: Proposed
- Date: 2026-11-22

## Context

### Problem

A long orchestrated run has an orchestrator, designers, implementers and
reviewers, and a person watching it sees only workspaces and logs. How does
an agent say how many steps its task has and has finished, with a short name,
so the desktop app can show a progress bar for the whole run, nested the way
the agents are?

### What a solution needs

1. **Nests with no wiring**: a child's progress appears under its parent's
   without the parent or child naming anything.
2. **Honest while plans change**: agents learn the real step count as they
   work, so the total must be editable, and the rollup must stay defined.
3. **Cheap for small models**: a few flags, no schema, nothing to look up.
4. **Safe**: an agent writes only its own record; limits keep it layoutable.
5. **Readable by the UI**: a plain file the desktop app can read without a
   new service.

### Relevant facts

- Workspaces already form a tree by `parent` ([0044](0044-workspace-trees.md)),
  and `ws machine` keeps per-child records under the root's
  `state/<root>/machine/<child>/` ([0050](0050-machine-wait-results-and-roles.md)).
- `ws machine` is the only part of `ws` agents run ([0046](0046-ws-machine.md)),
  and works for both the Claude Code and the ws runner.
- Records are TOML with a `version`, written through a renamed temp file.

## Decision

We will add `ws machine progress set | step | show`.

**A record per workspace.** `progress.toml` beside `spawn.toml` and
`result.toml`: `name` (up to 40 characters), `total`, `done` (`0 <= done <=
total <= 1,000,000`), `note` (up to 200), `updated_at`. Name and note are one
line of plain text. Only the workspace's own agent writes it.

**Own steps only.** A record counts the steps the agent does itself. Work it
hands to a child is that child's to report.

**Nesting is the workspace tree.** `show` returns the caller and its
descendants. Each node has a `rollup`: its own `total` and `done` plus its
children's rollups, and `fraction = done / total` (`null` while `total` is
0). A workspace with no record contributes no steps and passes its children's
up. A UI draws a bar per node from `progress`, and an overall bar from the
root's `rollup`.

**Edits.** `set` changes only the fields given, so an agent may raise
`total` mid-run; `step` adds to `done`. `done` above `total` is refused
(`progress_invalid`, exit 2) rather than clamped, so a miscounting agent is
told.

### Why this option

Nesting costs nothing because it reuses `parent`; agents learn two verbs.
Summing steps keeps the rollup defined and monotone as children report, and a
workspace that never reports doesn't break the bar. It is weaker where steps
differ in size: a ten-step child outweighs a one-step sibling, since the sum
weights by step, not by task. The UI may show a per-node bar instead.

## Alternatives

### Agent-reported percentage

One number per agent, 0 to 100.

**Pros**

- One field, no total to maintain.

**Cons**

- Can't be summed honestly, and a model's percentages jump around.

### Parent assigns each child a weight

The parent says what share of its bar each child fills.

**Pros**

- Every task weighs what the parent decides.

**Cons**

- The parent must know the plan up front and wire each child; a small model
  gets it wrong, and the spawn command grows.

### An MCP tool in the runner only

**Pros**

- Natural for ws runner agents.

**Cons**

- Claude Code children can't call it, and `ws machine` is already the one
  interface for both runners.

## Consequences

- Orchestrator and role prompts should tell agents to `set` a name and total
  early and `step` as they go; this ADR adds the command and its guide, not
  those prompt edits.
- The desktop app can read `progress.toml` for a live view; it isn't part of
  this change.
- A workspace's record is deleted with the root, as results are.
- Unverified: whether small models update progress reliably without a prompt
  nudge.
