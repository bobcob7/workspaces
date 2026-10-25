# 0012. Workspace names, state kept outside the workspace, and deletion guard

- Status: Accepted (2026-10-17); state files amended by [0015](0015-workspace-lifetime-follows-session.md) (2026-10-18); creation order amended by [0022](0022-workspace-record-before-rename.md) (2026-10-19); removal-pass timestamp in `state/` amended by [0026](0026-removal-pass-timing.md) (2026-10-19); `state/.respawn.lock` and session record version added by [0027](0027-respawn-lock-and-record-versions.md) (2026-10-20); ids from `--name` get a random suffix, and the name can be changed, by [0036](0036-workspace-id-suffix-and-rename.md) (proposed)
- Date: 2026-10-17

## Context

### Problem

Each workspace needs a directory name, and `ws` keeps state about it: the
session lock, session records, and `idle-after`
([0013](0013-cleanup-ignores-git-state.md)), plus metadata describing the
workspace. How are names chosen, where does the state live, and how does `ws`
make sure it only ever deletes workspaces?

### What a solution needs

1. **Never delete a directory that isn't a workspace.**
2. **Keep `ws` out of the workspace.** Agents work at the workspace root, so
   anything `ws` puts there is visible to them and can be edited or deleted
   by them.
3. **Distinct names stay distinct**, on every filesystem and in tools that
   key data by path, such as Claude Code.
4. **Stay correct when users delete workspaces by hand** or a removal is
   interrupted.

### Relevant facts

- **Claude Code keys history by an encoded path.** Every character other
  than ASCII letters and digits becomes `-`: `a.b_c` was stored as `a-b-c`,
  and `café-x` as `caf--x`[^bench-claude-enc]. Two workspaces whose names
  differ only in those characters would share history, and `claude
  --continue` in one could resume the other's conversation.
- **Case handling differs by platform.** On a default macOS APFS volume,
  `mkdir fix` failed with "File exists" after `mkdir Fix`. On Linux, tmpfs
  and overlayfs created both[^bench-case].
- **Accented letters can be folded to ASCII** with Unicode NFKD
  decomposition and dropping combining marks. In a prototype, `café-x`
  became `cafe-x`, `Ångström` became `angstrom`, and `ﬁle №5` became
  `file-no5`. Letters with no decomposition don't fold: `Straße` became
  `stra-e`, and `日本語` became empty[^bench-sanitize].
- **Writing a file sets its mtime**[^bench-activity]. 0013 decides idleness
  from the newest mtime in the workspace, so any `ws` file inside the
  workspace would need excluding from that scan.
- **Workspace roots can be reached through symlinks.** On macOS, `/tmp` is a
  symlink to `/private/tmp`[^macos-tmp].

## Decision

**Names.** The id is `ws-<YYYYMMDD>-<6 random [a-z0-9]>`, or derived from
`--name`:

1. Decompose with NFKD and drop combining marks (`golang.org/x/text`).
2. Lowercase ASCII letters and keep them and digits. Replace every other run
   of characters with one `-`.
3. Trim `-` from both ends, and cut to 64 characters.
4. Reject the name if the result is empty. If the id is already taken, fail
   instead of adding a suffix, since the user chose the name in order to use
   it.

The original `--name` is kept in the metadata for display.

**Layout.** Under `CONFIG_DIR` ([0011](0011-config-dir.md)):

```
workspaces/<id>/           the workspace: user content only
state/<id>/
  workspace.toml           version = 1, id, display name, path, creation time,
                           and per repo: canonical id, original spec, path,
                           upstream URL, ref, ref kind, commit checked out
  lock                     session lock (0013)
  sessions                 session records
  idle-after               earliest time the workspace can be idle
```

Nothing `ws` writes goes inside `workspaces/<id>/`, so the activity scan
needs no exclusions.

**Ordering.** Creation makes `state/<id>/` first and writes `workspace.toml`
last, after the repos are cloned. Removal deletes `workspaces/<id>/` first
and `state/<id>/` second. An interruption at any point leaves either state
without a directory or an incomplete state directory with no
`workspace.toml`. Any `ws` command removes both kinds of leftover when it
finds them. The same check cleans up after workspaces deleted by hand.

**Deletion guard.** `ws` deletes a directory only if:

- `state/<id>/workspace.toml` parses,
- the path it records matches the directory, after resolving symlinks, and
- the directory is a direct child of `workspaces/`, also resolved, unless
  `--allow-outside-root` is given.

A directory under `workspaces/` without state isn't a workspace. `ws list`
shows it as unknown, and `ws` never deletes it.

### Why this option

1. **Never the wrong directory.** Deleting needs a state record that names
   the directory, and the directory has to be in the right place.
2. **`ws` stays out of the workspace.** Agents see only their repos and
   files, and can't break `ws`'s state by editing the workspace.
3. **Distinct names stay distinct.** Only `a-z`, `0-9`, and `-` survive, and
   all three pass through Claude Code's encoding unchanged.
   Case-insensitive filesystems never see two names that differ only in
   case.
4. **Interruptions and manual deletes** leave only state that `ws` can
   recognize and remove.

The weak points are two directories to keep in step, handled by the
ordering above, and names that fold oddly (`Straße` becomes `stra-e`) or
not at all (`日本語` is rejected).

## Alternatives

### State inside the workspace, in `<workspace>/.ws/`

**Pros**

- One directory is the whole workspace, so it can't get out of step with
  its state.
- Listing reads only `workspaces/`.

**Cons**

- Visible to agents. An agent tidying up can delete `ws`'s metadata, after
  which `ws` no longer recognizes the workspace.
- The activity scan has to exclude `.ws/`. Without that exclusion, writing
  `idle-after` keeps the workspace active forever.

### A central registry file

**Pros**

- Listing reads one file.

**Cons**

- Every `ws` process contends for one file, and a corrupt file loses every
  workspace's state at once.

### Use `--name` as given

**Pros**

- No surprises: the directory is exactly what was typed.

**Cons**

- Names that differ only in punctuation or case share Claude Code history,
  or collide on macOS.
- Spaces and Unicode reach every path built from the name.

### Reject names outside `[a-z0-9-]` instead of folding them

**Pros**

- No surprising conversions such as `stra-e`.
- No `golang.org/x/text` dependency.

**Cons**

- `--name "Fix Login"` fails instead of becoming `fix-login`.

## Consequences

- `ws` depends on `golang.org/x/text`, a module maintained by the Go
  project.
- Every `ws` command reconciles `state/` against `workspaces/`, alongside
  the removal pass in 0013.
- `ws` never writes into a workspace, so treating an existing directory as a
  workspace (for example, a session in `~/some/dir`) remains possible later.
- Conformance: table-driven tests for the name rule, including every
  example above; tests that a directory without state, with state recording
  a different path, or outside the root is refused; that a symlink pointing
  outside the root is refused; and that interrupting creation or removal at
  each step leaves only leftovers that the next `ws` command removes.

## References

[^bench-claude-enc]: Observed 2026-10-17, Claude Code 2.1.302, macOS 26.6.2. `claude -p` run in `<scratch>/enc/a.b_c` and `<scratch>/enc/café-x` created `~/.claude/projects/…-scratchpad-enc-a-b-c` and `…-scratchpad-enc-caf--x`. Earlier, `/` and spaces were likewise stored as `-`.
[^bench-case]: Observed 2026-10-17 on macOS 26.6.2, boot volume with `diskutil info /` reporting "File System Personality: APFS" (the case-insensitive default, as opposed to "Case-sensitive APFS"). After `mkdir Fix`, `mkdir fix` failed with "File exists". In an `alpine:3.20` container (kernel 7.1.3), the same commands created both directories on a tmpfs mount and on the container's overlay root.
[^bench-sanitize]: Prototyped 2026-10-17, Go 1.27.1, `golang.org/x/text` v0.43.0, using `norm.NFKD` and dropping `unicode.Mn` runes, then the steps above. Inputs and outputs: `Fix Login` → `fix-login`, `café-x` → `cafe-x`, `a.b_c` → `a-b-c`, `  --x--  ` → `x`, `Straße` → `stra-e`, `ﬁle №5` → `file-no5`, `Ångström` → `angstrom`, `日本語` → empty, `🚀 launch` → `launch`, `../etc` → `etc`, 90 characters → cut to 64.
[^bench-activity]: See [0013](0013-cleanup-ignores-git-state.md), footnote `bench-activity`: measured 2026-10-17 on an Apple M4, macOS 26.6.2. In a repo with timestamps backdated using `touch -t`, editing a file changed that file's mtime and no other.
[^macos-tmp]: Observed 2026-10-17 on macOS 26.6.2: `readlink /tmp` prints `private/tmp`.
