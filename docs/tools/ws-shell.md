# ws shell

Create a workspace and open an interactive shell in it.

## Synopsis

```
ws shell [OPTIONS] <SPEC>...
```

## Description

Creates a workspace from the repo specs ([Creating a
workspace](README.md#creating-a-workspace)), then runs `$SHELL -i` (or
`/bin/sh -i` if `SHELL` is unset) in it, in the foreground.

**When the shell exits, the workspace is removed**, whatever the exit code
and whatever state its repos are in. Push, or copy out, anything worth
keeping first. If `ws` itself is killed, for example when the terminal is
closed or the machine reboots, the workspace is removed at the start of the
next `ws` command once nothing in it holds the session lock
([0015](../adrs/0015-workspace-lifetime-follows-session.md)). Shell
workspaces aren't restored.

While the shell runs, it and every command started from it hold the
workspace's session lock, so `ws rm` refuses to remove it.

The shell's environment gains:

| Variable       | Value                       |
| -------------- | --------------------------- |
| `WS_ID`        | workspace id                |
| `WS_WORKSPACE` | absolute workspace path     |

`ws shell` exits with the shell's exit code.

## Options

| Option          | Description                       |
| --------------- | --------------------------------- |
| `--name <name>` | Workspace name                    |
| `--offline`     | Don't fetch; use the cache as-is  |

## Examples

```console
# Try something on a branch, then throw it away
$ ws shell buff-bear@feat/new-thing
$ cargo test
$ exit

# Two repos side by side
$ ws shell --name api-bump buff-bear bobcob7/api@v2.1.0
```

## Errors

See the [workspace creation errors](README.md#creating-a-workspace).
Otherwise `ws shell` exits with the shell's exit code.

## See also

[`ws claude`](ws-claude.md),
[0014](../adrs/0014-command-set.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md)
