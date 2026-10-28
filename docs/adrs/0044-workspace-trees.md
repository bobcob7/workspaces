# 0044. A workspace started from inside another is its child; removing a workspace removes its descendants first

- Status: Accepted (2026-10-28)
- Date: 2026-10-28

## Context

### Problem

Agents start other agents: helpers with `ws claude`, and reviewers with
`ws review start`. Nothing records which agent started which, so when the
parent's task is done its helpers keep running, and their workspaces stay
until someone finds and removes each one. How does `ws` know which
workspaces belong to another, remove them with it, and nudge agents to
clean up after themselves?

### What a solution needs

1. **Recorded at creation, without the agent having to ask**: an agent
   that starts a helper the usual way gets the link.
2. **Removing a parent removes its descendants**, whichever way the
   parent goes: `ws rm`, `claude rm` followed by the removal pass, or
   [0045](0045-finished-reviews-are-removed.md)'s finished reviews.
3. **No surprise loss**: what will be removed is visible before it's
   removed, and a child can be kept.
4. **Safe across versions** ([0027](0027-respawn-lock-and-record-versions.md)).

### Relevant facts

- `ws review start` already records a **requester**: `--requester ID|none`,
  defaulting to the workspace `ws` runs in, found from the working
  directory (`internal/cli/review_start.go`,
  [0039](0039-ws-review-command.md)).
- A workspace is removed by `ws rm`, or by the removal pass once its
  session is gone ([0015](0015-workspace-lifetime-follows-session.md),
  [0026](0026-removal-pass-timing.md)). Removal ignores git state
  ([0013](0013-cleanup-ignores-git-state.md)).
- `ws rm` refuses a shell workspace whose shell runs (`shell_running`) and
  a workspace still being handed over (`workspace_busy`), and reports
  each workspace as an item ([0016](0016-output-contract.md),
  [0026](0026-removal-pass-timing.md)).
- `workspace.toml` is version 1, or 2 when hidden
  ([0043](0043-hidden-workspaces.md)); reading it rejects keys it
  doesn't know (`internal/workspace/record.go`).
- Process trees are the usual prior art: when a session leader exits,
  the kernel sends SIGHUP to the foreground process group of its
  controlling terminal[^exit].

## Decision

We will record a workspace's parent in its record, set it by default
from where `ws` runs, and remove descendants before their ancestor.

**The record.** `workspace.toml` gains `parent = "<id>"`, written only
when set. A record with a parent is `version = 3` (2 stays "hidden
without a parent", 1 "neither").

**Setting it.**

- `ws claude` run inside a workspace makes it the new workspace's parent,
  as `ws review start` finds the requester. `--parent <WORKSPACE>|none`
  overrides it; the parent must exist (`workspace_not_found`).
- `ws review start` sets the parent to the requester; `--requester none`
  means no parent.
- `ws shell` takes no parent: a person runs it, and the shell's own
  lifetime already ends it.
- The desktop app runs `ws` outside any workspace, so what it starts has
  no parent.
- The parent is set only at creation, to a workspace that exists, so the
  links can't form a cycle. A parent that is gone (removed outside `ws`,
  or a record that can't be read) makes the child a root.

**Removing a tree.** Whenever `ws` removes a workspace, it first removes
its descendants, deepest first, each the way `ws rm` would.

- `ws rm <W>` reports every workspace removed, `W` and its descendants,
  as items, each with `parent`. `--dry-run` lists them without removing
  anything. If a descendant can't be removed (`shell_running`,
  `workspace_busy`, a failed `claude rm`), it and its ancestors are kept
  and the failure is its item's error, `child_kept` (exit 4) on each
  ancestor; the other branches still go.
- `ws rm --keep-children <W>` instead clears `parent` in each direct
  child, making it a root, then removes `W` alone.
- The removal pass does the same for a workspace whose session is gone:
  its descendants go with it, whatever their own sessions' state, and
  failures are warnings, retried on the next pass.

**Seeing a tree.**

- `ws list --json` gives each workspace `"parent"` (an id or `null`).
  `--parent <WORKSPACE>` lists only that workspace's direct children.
- The table lists a child right after its parent, its id indented with
  `└ `; JSON keeps the order oldest first.
- `ws claude --json` and `ws review start --json` print `parent`.

**Encouraging cleanup.** `ws llm` tells agents:
- what they start from their workspace is their child, and is removed
  with them;
- to `ws rm` each child once they've used its result, and not to leave
  finished helpers running;
- to use `--parent none` only for work meant to outlive them.

The desktop app shows a parent's children and asks before a cascade; see
[docs/design/workspace-trees](../design/workspace-trees/README.md).

### Why this option

1. Taking the parent from the working directory, as the requester
   already is, means an agent following `ws llm` gets trees without new
   flags.
2. Cascading from every removal path is what makes the tree useful: an
   agent removed with `claude rm` takes its helpers with it, as a
   terminal's processes go with it.
3. `--dry-run`, the per-item report, the app's confirmation, and
   `--keep-children` and `--parent none` cover the cases where a child
   should stay.
4. Version 3 keeps older `ws` away from records with a parent, so an old
   removal pass never removes a parent and leaves its children unnoticed.

Where it's weak: a parent removed by the removal pass takes its children
without anyone confirming, including a helper's unpushed work, as
removal already does for the parent itself (0013). And a child kept when
its parent can't be removed keeps the parent too, so one stuck shell can
hold a tree until it exits.

## Alternatives

### Use the review requester only

Cascade only from requesters to their reviews.

**Pros:** no new record field.
**Cons:** helpers from `ws claude`, the common case, stay orphaned.

### Make the parent opt-in (`ws claude --parent`)

**Pros:** nothing is removed that its creator didn't tie to a parent.
**Cons:** agents forget flags; the default would leave the orphans this
is meant to stop. `--parent none` gives the same control.

### Orphan children instead of removing them

Clear `parent` when the parent goes, as `--keep-children` does, and
leave cleanup to the removal pass.

**Pros:** never removes a child's work implicitly.
**Cons:** a finished agent's helpers keep running and holding disk, which
is the problem; it complements the decision as `--keep-children`.

## Consequences

- Amends [0016](0016-output-contract.md) (error code `child_kept` under
  exit 4), [0012](0012-workspace-layout.md) (`parent` and version 3),
  [0014](0014-command-set.md) (`ws claude --parent`, `ws rm
  --keep-children`, `ws list --parent`), [0015](0015-workspace-lifetime-follows-session.md)
  (a workspace also goes with its parent), and
  [0039](0039-ws-review-command.md) (a review's requester is its parent).
- `docs/state.md`, `docs/tools/ws-claude.md`, `ws-rm.md`, `ws-list.md`,
  `ws-review.md`, and `ws llm` describe it.
- Conformance: Go tests that the default parent is the enclosing
  workspace and `--parent none` clears it; that records with a parent are
  version 3; that `ws rm` removes descendants deepest first, keeps a
  failed child's ancestors, reports each item, and keeps children with
  `--keep-children`; that the removal pass cascades; and that a missing
  parent makes a root. Cargo and Vitest tests for the app's children list
  and confirmation.
- **Unverified:** how long a cascade over many agent sessions takes,
  since each runs `claude stop` and `claude rm`, and the removal pass
  runs before commands; to be measured on a tree of 10 agents once it
  ships, and moved off the command path if it's slow.

## References

[^exit]: _exit(2), POSIX.1-2017, The Open Group Base Specifications Issue 7. <https://pubs.opengroup.org/onlinepubs/9699919799/functions/_exit.html> (accessed 2026-10-28). "If the process is a controlling process, the SIGHUP signal shall be sent to each process in the foreground process group of the controlling terminal belonging to the calling process."
