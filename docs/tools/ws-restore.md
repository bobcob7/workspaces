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
process. The agent comes back with its conversation, waiting for input. To
have it pick up interrupted work, reply to it (`claude attach <id>`).

Without arguments it restores every agent workspace; with `WORKSPACE`
arguments, only those. Shell workspaces aren't restored.

## Options

| Option      | Description                                   |
| ----------- | --------------------------------------------- |
| `--dry-run` | Print what would be restarted, change nothing |
| `--json`    | Print `{"workspaces": [{"id", "session", "restored", "error"}], "warnings"}` |

## Examples

```console
$ ws restore
restored  fix-login            e8cdf5d8
restored  ws-20261018-k3f9qa   5d8d652c
running   docs-pass            3c11e0f2
```

## Errors

| Situation                                   | Error code            | Exit |
| ------------------------------------------- | --------------------- | ---- |
| `claude agents` failed                      | `claude_failed`       | 1    |
| A named workspace not found, or a shell workspace | `workspace_not_found` | 3 |
| Some `claude respawn` failed; the others are still restarted, and each result carries its own `error` | `claude_failed` | 1 |

Respawning starts Claude Code's background service if it isn't running,
with `ws restore`'s environment.

## See also

[`ws claude`](ws-claude.md), [`ws list`](ws-list.md),
[`ws restart`](ws-restart.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md)
