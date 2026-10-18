# ws rm

Remove workspaces and their sessions now.

## Synopsis

```
ws rm [OPTIONS] <WORKSPACE>...
```

## Description

For an **agent workspace**, runs `claude stop <id>` and `claude rm <id>`,
then deletes the workspace and its state. The conversation transcript stays
available through `claude --resume`.

For a **shell workspace**, refuses while the shell, or anything started from
it, is running. Exit the shell instead; that removes it.

Git state is ignored: uncommitted changes, unpushed commits, and stashes go
with the workspace ([0015](../adrs/0015-workspace-lifetime-follows-session.md)).
Only directories recorded in a valid `$CONFIG_DIR/state/<id>/workspace.toml`,
directly under the workspace root, are ever deleted
([0012](../adrs/0012-workspace-layout.md)).

You don't need `ws rm` after `claude rm` or deleting the session in agent
view: the workspace is removed at the start of a later `ws` command (checked
at most once a minute).

## Options

| Option      | Description                                   |
| ----------- | --------------------------------------------- |
| `--dry-run` | Print what would be removed, change nothing   |
| `--json`    | Print `{"workspaces": [{"id", "removed", "error"}], "warnings"}` |

## Examples

```console
$ ws rm fix-login
removed  fix-login  e8cdf5d8

$ ws rm e8cdf5d8          # by Claude session id
```

## Errors

| Situation                                     | Error code            | Exit |
| --------------------------------------------- | --------------------- | ---- |
| Workspace not found                           | `workspace_not_found` | 3    |
| Shell workspace with a running shell          | `shell_running`       | 4    |
| `claude rm` refused or failed; the workspace is kept | `claude_failed` | 1    |

With several workspaces, the others are still removed, each result carries
its own `error`, and the exit code is that of the first failure.

## See also

[`ws list`](ws-list.md),
[0012](../adrs/0012-workspace-layout.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md)
