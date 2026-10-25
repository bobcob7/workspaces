# 0036. Ids from `--name` end with a random suffix, and the name can be changed

- Status: Proposed
- Date: 2026-10-25

## Context

### Problem

[0012](0012-workspace-layout.md) makes a workspace's id, and so its
directory, from `--name` exactly: `--name "Fix Login"` is `fix-login`, and
a second `--name "Fix Login"` fails with `workspace_exists`. The original
`--name` is kept in `workspace.toml` "for display", but nothing shows it,
and it can't be changed.

People want to name a workspace for what it's about, reuse a name, and
change the name as the task changes, without moving the workspace. Should
the id stay the name, and can the name change?

### What a solution needs

1. **A workspace never moves.** Its directory is an agent's working
   directory, Claude Code keys its history by that path, and its session is
   named after the id ([0012](0012-workspace-layout.md),
   [0025](0025-ws-claude-session-hand-off.md)).
2. **Any name can be used again**, by a person or by the desktop app.
3. **The name is shown and can be changed** any number of times.
4. **The id still tells the workspace apart at a glance**, in paths,
   `claude agents`, and `ws list`.

### Relevant facts

- **Renaming a directory under a running agent breaks it.** Its shell, and
  Claude Code's session, keep the old path as their working directory, and
  Claude Code finds a session's history by the encoded path
  ([0012](0012-workspace-layout.md), footnote `bench-claude-enc`).
- **`workspace.toml` is already rewritten safely.** Every record is written
  through a temp file renamed into place, so a reader sees all of one or
  none ([0022](0022-workspace-record-before-rename.md)).
- **8 hex digits are 2^32 values.** With a thousand workspaces sharing one
  sanitized name, the chance that a new suffix is taken is under 1 in
  4 million; creation retries a taken id, as it already does for generated
  ids.
- **`claim` already retries collisions** for generated ids
  (`ws-<date>-<6 chars>`), five times.

## Decision

**Ids.** With `--name`, the id is the sanitized name (0012's steps, cut to
55 characters), `-`, and 8 random lowercase hex digits:
`--name "This is a Test"` makes `this-is-a-test-d8efb24f`. The id stays at
most 64 characters. A taken id is retried with a new suffix, as for
generated ids, and `workspace_exists` now means only that no free id was
found. Without `--name`, ids are generated as before.

**Names.** `workspace.toml`'s `name` is the workspace's display name.
`--name` sets it; `ws rename <WORKSPACE> <NAME>` replaces it, rewriting
the record through a temp file and a rename. The id, the directory, and
the session's name never change. A name is kept as given with surrounding
whitespace trimmed; an empty name or one with a control character is
`name_invalid`. `ws list` shows it in a NAME column and as `name` in JSON
(`null` without one).

Workspace arguments are still an id, a path, or a session id, never a
name, since names needn't be unique.

### Why this option

1. **Never moves.** Rename touches only the record in `state/<id>/`.
2. **Names can repeat.** Each use gets its own suffix.
3. **Shown and changeable.** The record's existing `name` field holds it,
   so the record format, and its version, don't change.
4. **Recognizable.** The id still starts with the name it was created
   with. After a rename the two differ, which is the cost: the id then
   reflects the first name, not the current one.

## Alternatives

### Keep the id as the name, and rename the directory on `ws rename`

**Pros**

- The directory always matches the name.

**Cons**

- Moves a running agent's working directory, and loses its Claude Code
  history.
- A name still can't be used twice.

### Add a suffix only when the name is taken

**Pros**

- The first `fix-login` keeps the plain id.

**Cons**

- Which workspace gets the plain id depends on what else exists, so
  scripts can't predict ids either way, and still have to read them from
  the output.

### Opaque ids, with the name only in the record

**Pros**

- Ids never mislead after a rename.

**Cons**

- Paths, `claude agents`, and `ws list -q` show nothing recognizable.

## Consequences

- 0012's status line notes this amendment: the "fail instead of adding a
  suffix" rule is replaced.
- [0014](0014-command-set.md)'s command set gains `ws rename`.
- Scripts that ran `ws claude --name x` and then used `x` as the id must
  read the id from the output (`id` with `--json`). The desktop app's
  shells and reviews no longer predict ids: reviews match their pull
  request by the id's prefix, and the app links a shell's terminal to the
  workspace `ws list` shows for it.
- `docs/state.md`: `workspace.toml` is written by creation and rewritten
  by `ws rename`.
- Conformance: tests that `--name` gives `<sanitized>-<8 hex>` within 64
  characters, that a name can be used twice, that a taken id is retried,
  and that renaming several times keeps the id and the directory.
