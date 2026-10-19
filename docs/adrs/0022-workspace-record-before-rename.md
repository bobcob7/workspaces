# 0022. Build workspaces in state, write the record, then move them into place

- Status: Accepted (2026-10-19)
- Date: 2026-10-18

## Context

### Problem

[0012](0012-workspace-layout.md) orders creation as: make `state/<id>/`,
clone the repos into `workspaces/<id>/`, then write `workspace.toml` last.
An interruption leaves "either state without a directory or an incomplete
state directory with no `workspace.toml`", and any `ws` command removes such
leftovers.

Cloning straight into `workspaces/<id>/` can leave a partial workspace
directory with no record. 0012's deletion guard only deletes directories a
valid record names, so that directory is never removed. Building the repos
elsewhere and renaming them into place avoids that, but with the record
still last, a kill between the rename and the record write leaves a full
directory with no record, which is never removed either.

In what order should creation build, record, and place a workspace?

### What a solution needs

1. **Every interruption leaves something `ws` cleans up by itself**,
   without weakening the deletion guard.
2. **A workspace under `workspaces/` is complete**: no command, person, or
   agent sees a half-cloned workspace there.
3. **Concurrent `ws` commands can tell** a creation in progress from an
   abandoned one.

### Relevant facts

- **Rename is atomic.** "The rename() system call guarantees that an
  instance of new will always exist, even if the system should crash in the
  middle of the operation", and both paths "must reside on the same file
  system"[^rename]. `state/` and `workspaces/` are siblings under
  `CONFIG_DIR` ([0011](0011-config-dir.md)).
- **The deletion guard** deletes a directory only if a parsing
  `workspace.toml` names it and it's a direct child of `workspaces/`
  ([0012](0012-workspace-layout.md)).
- **Creation holds `state/<id>/lock`** exclusively while it runs, and the
  leftover check skips state that is locked or less than a minute old, so it
  never removes a creation in progress.

## Decision

We will change 0012's creation order to:

1. Make `state/<id>/` and take its lock.
2. Clone and set up every repo inside `state/<id>/building/`.
3. Write `state/<id>/workspace.toml`, through a temporary file renamed into
   place.
4. Rename `state/<id>/building/` to `workspaces/<id>/`.

If creation fails at any step, it removes `state/<id>/` and anything under
it.

The leftover check treats as abandoned, and removes, any state that is
neither locked nor less than a minute old and that has no `workspace.toml`,
or has a `workspace.toml` whose `workspaces/<id>/` doesn't exist. The second
case also covers a workspace directory deleted by hand, as 0012 already
intended. Removal keeps 0012's order: `workspaces/<id>/` first, then
`state/<id>/`.

### Why this option

1. **Every interruption is cleaned up.** Before step 4, everything is under
   `state/<id>/`, which `ws` may delete. After step 4, the workspace is
   complete and recorded. No step leaves an unrecorded directory under
   `workspaces/`.
2. **Complete or absent.** The rename makes the whole workspace appear at
   once.
3. **In progress vs abandoned** stays decided by the lock and age, as
   before.

The cost: for the moment between steps 3 and 4, a record names a directory
that doesn't exist yet. Commands that list or find workspaces must treat
that as not a workspace, rather than as a broken one.

## Alternatives

### Keep 0012's order, building in place

**Pros**

- No staging directory, and the record really is last.

**Cons**

- A killed clone leaves a partial, unrecorded directory under
  `workspaces/` that the deletion guard can never remove.

### Build in state, rename, then write the record

**Pros**

- Keeps "record last".

**Cons**

- A kill between the rename and the record write leaves a complete,
  unrecorded directory under `workspaces/` that is never removed.

## Consequences

- 0012's status line notes this amendment.
- Listing, finding, and `ws cache prune`'s check for commits in use treat a
  record without its directory as not a workspace; prune still treats it as
  a creation in progress while it is locked or young.
- Conformance: tests that simulate a kill after step 2 and after step 3,
  and check that the next leftover check removes the state, and that nothing
  is left under `workspaces/`.

## References

[^rename]: rename(2), macOS 26.6.2. "The rename() system call guarantees that an instance of new will always exist, even if the system should crash in the middle of the operation." Both paths "must reside on the same file system."
