# ws restore

Restart the agents of every agent workspace whose session isn't running.

## Synopsis

```
ws restore [OPTIONS] [<WORKSPACE>...]
```

## Description

After a reboot, or when Claude Code's background service has been gone for
more than about a minute, background sessions stop and don't come back on
their own ([0015](../adrs/0015-workspace-lifetime-follows-session.md)).
Their conversations are kept.

`ws restore` reads `claude agents --json --all` and runs
`claude respawn <id>` for each agent workspace whose session has no
process. A session started less than about 10 s ago that has no process yet
is `starting`, not stopped, and is left alone
([0024](../adrs/0024-claude-code-client.md)). The agent comes back with its
conversation and the options it was started with, waiting for input. To
have it pick up interrupted work, reply to it (`claude attach <id>`).

Without arguments it restores every agent workspace; with `WORKSPACE`
arguments, only those. Shell workspaces aren't restored.

Sessions are respawned one at a time, oldest workspace first, or in the
order named. A session that is running or starting is left alone, and so
is one Claude Code doesn't list (`unknown`), such as one deleted with
`claude rm`: its workspace goes with a later removal pass. One deleted
between the listing and its respawn is `unknown` too.

After each respawn, `ws restore` waits until Claude Code lists the session
with a process, for up to 10 s; if it doesn't by then, it warns and goes
on, and the session still counts as restored. Only one `ws restore` or
`ws restart` respawns at a time: each holds a lock on
`state/.respawn.lock` from before it lists the sessions until its last
respawn is done, and another run waits for it (Ctrl-C stops the wait),
then finds the sessions running. A
`--dry-run` takes no lock, so it may show a session as stopped that
another run is restarting.

If Claude Code lists the recorded session in a directory other than the
workspace's, or its one repo's ([0028](../adrs/0028-single-repo-session-directory.md)), `ws restore` touches neither and fails that item with
`session_mismatch`, as [`ws rm`](ws-rm.md) does. Like other commands, it
first removes workspaces whose session is gone, at most once a minute
([0026](../adrs/0026-removal-pass-timing.md)). See
[0027](../adrs/0027-respawn-lock-and-record-versions.md) and
[Workspace state](../state.md).

## Options

| Option      | Description                                   |
| ----------- | --------------------------------------------- |
| `--dry-run` | Print what would be restarted, change nothing |
| `--json`    | Print `{"workspaces": [{"id", "session", "state", "restored", "error"}], "warnings"}` |

`state` is the session as `ws` read it before restoring, with
[`ws list`](ws-list.md)'s values: `stopped` (restored, or with `--dry-run`
would be), `starting`, `idle`, `working`, or `unknown` when Claude Code
doesn't list it; `null` when it wasn't read, as for a workspace that
wasn't found. `restored` is `true` only when `claude respawn` succeeded.

## Examples

```console
$ ws restore
restored  fix-login-d8efb24f  e8cdf5d8
restored  ws-20261018-k3f9qa  5d8d652c
running   docs-pass-0f7c2a19  3c11e0f2
starting  api-bump-5c01e7a2   9a7e01b4

$ ws restore --dry-run fix-login-d8efb24f
would restore  fix-login-d8efb24f  e8cdf5d8
```

## Errors

| Situation                                   | Error code            | Exit |
| ------------------------------------------- | --------------------- | ---- |
| `claude agents` failed; nothing is restored | `claude_failed`       | 1    |
| A named workspace not found, a shell workspace, or one whose session was never recorded | `workspace_not_found` | 3 |
| A named workspace still being handed to its session | `workspace_busy` | 4 |
| Its session is listed in another directory; nothing is touched | `session_mismatch` | 4 |
| Some `claude respawn` failed; the others are still restarted, and each result carries its own `error` | `claude_failed` | 1 |

With several workspaces, the exit code is that of the first failure.

Respawning starts Claude Code's background service if it isn't running,
with `ws restore`'s environment.

## See also

[`ws claude`](ws-claude.md), [`ws list`](ws-list.md),
[`ws restart`](ws-restart.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md),
[0024](../adrs/0024-claude-code-client.md),
[0027](../adrs/0027-respawn-lock-and-record-versions.md)
