# 0043. A workspace can be hidden: `hidden = true` in its record, set by `ws review start`, `ws claude --hidden`, and `ws hide`; `ws list` and the desktop app show hidden workspaces only when asked

- Status: Proposed
- Date: 2026-10-28

## Context

### Problem

Some workspaces aren't made by a person for a task they're following.
`ws review start` makes one per review, whether an agent asked for it or
the desktop app started it automatically for a new pull request
([0035](0035-desktop-review-checker.md), [0039](0039-ws-review-command.md)).
Scripts and agents can make others with `ws claude`. These land in
`ws list` and in the desktop app's Agents list next to the user's own
agents, and a review agent that has submitted and gone idle counts as
"waiting for you". How does a workspace stay out of the way, while still
being one `ws list` flag or one click away?

### What a solution needs

1. **Out of the default views**: hidden workspaces aren't in `ws list`,
   the Agents list, or the "waiting for you" count, unless asked for.
2. **Still reachable**: one flag in the CLI and one click in the app show
   them, and every other command (`rm`, `rename`, `restore`, `restart`,
   the removal pass) treats them like any other workspace.
3. **Chosen by whoever creates the workspace**, and changeable later, by
   a person or an agent.
4. **Safe across versions**: an older `ws` doesn't misread or remove a
   hidden workspace ([0027](0027-respawn-lock-and-record-versions.md)).

### Relevant facts

- A workspace's record is `state/<id>/workspace.toml`, written at
  creation and rewritten by `ws rename`
  ([0012](0012-workspace-layout.md), [0036](0036-workspace-id-suffix-and-rename.md)).
- Reading a record fails on a key it doesn't know, and on a `version`
  newer than the one it knows; either way the workspace is left alone and
  a warning is printed (`internal/workspace/record.go`,
  [0027](0027-respawn-lock-and-record-versions.md)). 0027: "A change that
  an older `ws` could misread raises the version."
- The desktop app builds the Agents list from `ws list --json`, and the
  Reviews section finds each pull request's review workspace in that same
  listing by its `review` record (`desktop/src-tauri/src/commands.rs`,
  [0039](0039-ws-review-command.md)).
- The app counts an agent as waiting for you when it's idle or needs
  input, and shows the count in the top bar, the window title, and the
  rail's Agents badge (`desktop/src/features/agents/status.ts`).
- Prior art for hiding by default behind one flag: `ls` hides names
  beginning with a dot unless given `-a`[^ls], and `docker ps` lists only
  running containers unless given `--all`[^docker-ps].

## Decision

We will record hiddenness in the workspace record, let the creator or
anyone later set it, and filter it out in `ws list` and the app's Agents
list unless asked.

**The record.** `workspace.toml` gains `hidden = true`. It's written only
when true, and a record that has it is `version = 2`; a visible
workspace's record stays `version = 1`, byte for byte as today.

**Setting it.**

```
ws review start ...                 # always hidden
ws claude --hidden [OPTIONS] ...    # hidden from the start
ws hide <WORKSPACE>                 # hide an existing workspace
ws unhide <WORKSPACE>               # show it again
```

- `ws review start` makes every review workspace hidden. The review is
  followed in the Reviews section, with `ws review wait`, or as
  `ws list -a`.
- `ws claude --hidden` is for scripts and agents that start helpers.
  `ws shell` has no `--hidden`: the user is in the shell.
- `ws hide` and `ws unhide` take a workspace the way `ws rename` does (id,
  unique prefix, or name), rewrite the record, and are no-ops when it's
  already in that state. `--json` prints `{"id", "name", "path",
  "hidden", "warnings"}`.
- The desktop app's "send fixes to a new agent" ([0041](0041-desktop-acts-on-findings.md))
  stays visible: the user asked for that agent and follows it.

**Showing it.**

- `ws list` leaves hidden workspaces out; `-a`/`--all` includes them,
  with `(hidden)` after the name in the table. `-q` and `--repo` follow
  the same rule. Without `-a`, if any were left out, `ws list` prints
  `N hidden workspaces not shown; ws list -a shows them` on stderr.
- `ws list --json` gives each workspace `"hidden": true|false` and adds a
  top-level `"hidden_count"`: how many were left out (0 with `-a`).
- Every other command finds, removes, restores, and restarts hidden
  workspaces exactly as before.
- The desktop app runs `ws list --all --json`, so Reviews still sees
  every review workspace. The Agents list puts hidden entries in a
  **Hidden** group at the end, collapsed by default; they don't count
  toward "waiting for you" or the rail badge. A hidden agent's header
  shows a **Hidden** chip and an **Unhide** action; a visible one has
  **Hide**. Cross-links to a hidden agent ("Open agent" from a review)
  expand the group and select it.

### Why this option

1. Filtering in `ws list` and in the app keeps both default views to the
   user's own work, and the badge stops counting finished reviews.
2. One flag (`-a`, as in `ls` and `docker ps`) and one group header bring
   them back; nothing else changes behaviour, so a hidden agent is
   restored after a reboot and removed with its session like any other.
3. The creator decides at creation (`ws review start` always, `ws claude
   --hidden` on request), and `ws hide`/`ws unhide` change it later,
   from the CLI, the app, or an agent.
4. An older `ws` already leaves a version-2 record alone and says to
   upgrade; visible workspaces keep version 1, so a downgrade only
   affects hidden ones.

It's weak in that a hidden agent that really needs input is easy to miss:
the app doesn't count it, by design. Reviews have their own section and
badge, so this matters most for helpers started with `ws claude
--hidden`. And hiding is a flag, not a category: nothing records *why* a
workspace is hidden beyond the review record that review workspaces
already have.

## Alternatives

### Derive hiddenness from the review record

Treat any workspace with `state/<id>/review.toml` as hidden, with no new
state.

**Pros**

- No record change and no new commands.

**Cons**

- Only reviews can be hidden; other automation can't use it, and a
  review can't be shown in the Agents list when someone wants to follow
  it.

### Hide only in the desktop app

Keep a list of hidden workspace ids in `desktop.json` ([0034](0034-desktop-state-file.md)).

**Pros**

- No change to `ws`.

**Cons**

- `ws list` stays as noisy, for people and for agents; a workspace made
  by an agent or script can't be hidden at creation; ids of removed
  workspaces pile up in the file.

### Labels or an origin field with filters

Record `origin = "review"` or free-form labels, and filter with
`ws list --label` or `--origin`.

**Pros**

- More general: could group, not just hide.

**Cons**

- More surface (label syntax, filter rules) than the problem needs, and
  still needs a default-hidden rule on top. Could complement this later:
  `hidden` would stay the default filter.

### A separate root for hidden workspaces

Put hidden workspaces under `workspaces/.hidden/`.

**Pros**

- `ls` of the workspace root is clean too.

**Cons**

- Hiding or unhiding would move the directory under a running session,
  whose working directory and transcript path name it; and the layout
  in 0012 changes for every reader.

## Consequences

- Amends [0012](0012-workspace-layout.md) (`hidden` and version 2 in
  `workspace.toml`), [0014](0014-command-set.md) (`ws hide`,
  `ws unhide`, `ws claude --hidden`, `ws list -a`),
  [0039](0039-ws-review-command.md) (review workspaces are hidden), and
  [0031](0031-desktop-sections-rail.md) (the Agents list's Hidden group).
- `docs/state.md`, `docs/tools/ws-list.md`, `ws-claude.md`, a new
  `ws-hide.md`, and `ws llm` describe it; the design is in
  [docs/design/hidden-workspaces](../design/hidden-workspaces/README.md).
- An agent that runs `ws list` to find its review must add `-a`, or use
  `ws review wait` with the id `ws review start` printed. The
  `ws llm review` guide says so.
- Conformance: Go tests that a hidden record round-trips as version 2 and
  a visible one stays version 1; that `ws list` leaves hidden workspaces
  out, counts them, and shows them with `-a`, `-q -a`, and `--repo`; that
  `ws review start` and `ws claude --hidden` write `hidden`; that hide
  and unhide are idempotent; and that `ws restore` and the removal pass
  still handle hidden workspaces. Cargo and Vitest tests for the Hidden
  group, the badge count, and Hide/Unhide.
- **Unverified:** that an older `ws` with this repo's current code prints
  the upgrade warning for a version-2 record and removes nothing; checked
  by a test that runs `readRecord`'s version check on a version-2 record
  with `formatVersion` 1, and by hand with the last release before this
  change.

## References

[^ls]: ls(1), macOS 26.6.2. "`-a` Include directory entries whose names begin with a dot (`.`)."
[^docker-ps]: `docker ps --help`, Docker 29.2.1 (run 2026-10-28). "`-a, --all` Show all containers (default shows just running)"
