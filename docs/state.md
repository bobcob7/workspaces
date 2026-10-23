# Workspace state

How `ws` records workspaces, which locks it takes, and when it removes
them. This page describes the current design; the decisions behind each part
are in the ADRs it links. When a decision changes, this page changes with
it.

## Layout

Under `CONFIG_DIR` ([config](config.md)):

| Path | What it is | Written |
| --- | --- | --- |
| `workspaces/<id>/` | the workspace: the user's repos and files only | moved into place by creation |
| `workspaces/CLAUDE.md` | tells agents to run `ws llm` ([0014](adrs/0014-command-set.md)) | when `workspaces/` is first created, if missing, through a temp file linked into place; never overwritten |
| `state/<id>/workspace.toml` | the workspace record ([0012](adrs/0012-workspace-layout.md)) | once, by creation |
| `state/<id>/session` | the session record ([0023](adrs/0023-shell-session-keeps-creation-lock.md)) | once, when the session is known |
| `state/<id>/lock` | the workspace lock | by creation |
| `state/<id>/trust` | the Claude Code trust `ws claude` added for a one-repo workspace's repo ([0028](adrs/0028-single-repo-session-directory.md)) | once, before the trust is written; read by every removal, which takes the trust back |
| `state/<id>/building/` | the workspace while it's being built ([0022](adrs/0022-workspace-record-before-rename.md)) | by creation, then moved |
| `state/.removal-pass` | its modification time is when the removal pass last started ([0026](adrs/0026-removal-pass-timing.md)) | by the removal pass |
| `state/.respawn.lock` | the respawn lock ([0027](adrs/0027-respawn-lock-and-record-versions.md)) | by `ws restore` and `ws restart` |

- **Workspace contents.** Nothing `ws` writes goes inside
  `workspaces/<id>/`.
- **Hidden names.** A name in `state/` that starts with `.` is never a
  workspace.
- **Unrecorded directories.** A directory under `workspaces/` without a
  record isn't a workspace, and `ws` never deletes it.

## Records

Both records are TOML files with a top-level `version`. They're written
through a temporary file renamed into place, so a reader sees all of a
record or none of it.

The session record holds these fields:
- `kind`: `"shell"` or `"claude"`;
- `claude_session_id`: empty for a shell;
- `started_at`: RFC 3339, UTC.

A session record with no `version` is version 1.

A workspace whose record is from a newer `ws` (a version this `ws` doesn't
know), or can't be parsed, is left alone
([0027](adrs/0027-respawn-lock-and-record-versions.md)):
- nothing removes it;
- commands warn about it, naming the file;
- `ws cache prune` removes no commit while it exists.

## Locks

**The workspace lock** (`state/<id>/lock`) is taken exclusively when
creation makes `state/<id>/`. Who holds it then depends on the kind of
workspace:

- **A shell workspace** holds it for its whole life
  ([0023](adrs/0023-shell-session-keeps-creation-lock.md)).
  - `ws` holds it, and so do the shell and every command the shell runs:
    they get it as descriptor 3.
  - It's released after removal. If `ws` was killed, it's released when
    the shell and its commands have all exited.
- **An agent workspace** holds it until its session record is written
  ([0025](adrs/0025-ws-claude-session-hand-off.md)). `claude` never gets it.
  After that the lock is free, and Claude Code's listing decides whether
  the workspace is live.

**The respawn lock** (`state/.respawn.lock`) is taken exclusively by
`ws restore` and `ws restart`
([0027](adrs/0027-respawn-lock-and-record-versions.md)):

- Each takes it before listing sessions, and holds it until its last
  respawn has shown up. For `ws restart --no-wait`, that's the last idle
  session's respawn.
- A second run waits for it; Ctrl-C stops the wait.
- A `ws restore` dry run, or one that involves no agent workspace, doesn't
  take it. `ws restart` always takes it, since it replaces the background
  service even when no session runs.

**Checking a lock** takes a shared lock for a moment, without waiting and
without creating the file or its directory
([0026](adrs/0026-removal-pass-timing.md)).

**Lock order.** No command waits for one lock while holding another:
- the removal pass, `ws rm`, and creation never take the respawn lock;
- a command holding the respawn lock only checks workspace locks.

## Life of a workspace

| Stage | On disk | Kept because |
| --- | --- | --- |
| Building | `state/<id>/building/`, no `workspace.toml` | workspace lock held |
| Recorded | `workspace.toml`, no `workspaces/<id>/` yet | workspace lock held |
| Handing off | `workspaces/<id>/`, no session record | workspace lock held |
| Live shell | session record `shell` | workspace lock held |
| Live agent | session record `claude` | Claude Code lists the session, or still has its jobs directory |

Creation goes through the stages in order
([0022](adrs/0022-workspace-record-before-rename.md)):
1. Make `state/<id>/` and lock it.
2. Clone the repos into `building/`.
3. Write `workspace.toml`.
4. Rename `building/` to `workspaces/<id>/`.
5. Write the session record.

A workspace stops being kept in one of two ways:

- **Gone**: its session ended.
  - A shell workspace's lock is free.
  - An agent's session isn't listed **and** its jobs directory is missing
    ([0015](adrs/0015-workspace-lifetime-follows-session.md)).
- **Abandoned**: it's in one of the first three stages, its lock is free,
  and `state/<id>/` hasn't changed for a minute. This is left behind by an
  interrupted creation or hand-off.

A Claude Code session's state comes only from its `pid`, `status` and
`startedAt` ([0024](adrs/0024-claude-code-client.md)):

| State | Meaning |
| --- | --- |
| running | it has a process |
| working | its status is `busy` |
| starting | no process yet, started less than 10 s ago |
| stopped | none of the above |

## Removal

**Leftover cleanup** runs at the start of every command that reads
`CONFIG_DIR`, which is all of them except `ws llm`, `--help` and
`--version`. It removes an abandoned `state/<id>/` that has no
`workspace.toml`, or whose `workspaces/<id>/` doesn't exist.

**The removal pass** runs at the start of the same commands, at most once a
minute; `ws list` runs it every time
([0026](adrs/0026-removal-pass-timing.md)). It:

1. Reads every session record, then lists Claude Code's sessions once, and
   only if a workspace needs it.
2. Removes each gone or abandoned workspace. For an abandoned one it first
   runs `claude rm` on each background session whose working directory is
   exactly that workspace, or its one repo
   ([0025](adrs/0025-ws-claude-session-hand-off.md),
   [0028](adrs/0028-single-repo-session-directory.md)).
3. If the listing fails, keeps agent and abandoned workspaces, with a
   warning.

**Every removal** goes through the deletion guard
([0012](adrs/0012-workspace-layout.md)), then deletes `workspaces/<id>/`,
then takes back the Claude Code trust recorded in `state/<id>/trust`, if
any (a failure is a warning), then deletes `state/<id>/`
([0028](adrs/0028-single-repo-session-directory.md)).
- A workspace another command already removed counts as removed.
- One whose removal fails stays listed, with a warning.

## What each command does

- **`ws shell`**: creates the workspace, records a `shell` session and
  runs the shell under the lock. When the shell exits, it removes the
  workspace ([0023](adrs/0023-shell-session-keeps-creation-lock.md)).
- **`ws claude`**: creates the workspace and runs `claude --bg`, which
  finishes even if `ws` is interrupted. Then it records a `claude` session
  and releases the lock.
  - If `claude --bg` fails, it removes the workspace.
  - If the record can't be written, it runs `claude rm` on the new session
    and then removes the workspace
    ([0025](adrs/0025-ws-claude-session-hand-off.md)).
- **`ws rm`**: runs the pass first, then handles each workspace named:
  - **a running shell**: refuses with `shell_running`;
  - **one being handed off**: refuses with `workspace_busy`;
  - **an agent whose session Claude Code lists in another directory**
    than the workspace or its one repo:
    refuses with `session_mismatch`;
  - **any other agent**: stops and removes its session, then the
    workspace ([0026](adrs/0026-removal-pass-timing.md)).
- **`ws restore`**: takes the respawn lock, then respawns each stopped
  session that Claude Code lists in its own workspace, one at a time,
  waiting up to 10 s for each to show a process ([ws restore](tools/ws-restore.md)).
- **`ws restart`**: takes the respawn lock, then replaces the background
  service and respawns every background session that has a process, `ws`'s
  or not ([0018](adrs/0018-restart-claude-daemon.md)). After each respawn it
  waits as `ws restore` does. It reads session records only to show each
  session's workspace, and changes no record.
- **`ws list`**: runs the pass, then shows what remains. An agent is
  `stale` when its process started before the background service's
  ([0026](adrs/0026-removal-pass-timing.md)).
