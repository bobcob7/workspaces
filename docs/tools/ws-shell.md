# ws shell

Create a workspace and open an interactive shell in it.

## Synopsis

```
ws shell [OPTIONS] [<SPEC>...]
```

## Description

Creates a workspace from the repo specs ([Creating a
workspace](README.md#creating-a-workspace)), or an empty one when there are
none, then runs `$SHELL -i` (or
`/bin/sh -i` if `SHELL` is unset or empty) in it, in the foreground.

**When the shell exits, the workspace is removed**, whatever the exit code
and whatever state its repos are in. Push, or copy out, anything worth
keeping first. If `ws` itself is killed, for example with SIGKILL or when
the machine reboots, the workspace is removed at the start of the
next `ws` command once nothing in it holds the session lock
([0015](../adrs/0015-workspace-lifetime-follows-session.md)). Shell
workspaces aren't restored. If removal fails, `ws` says so on stderr and
still exits with the shell's status; the workspace is left for a later
`ws` command to remove.

The session lock is the one `ws` took to create the workspace, held from
then until the workspace is removed. While the shell runs, it and every
command started from it hold the lock too (as file descriptor 3), so
`ws rm` refuses to remove the workspace
([0023](../adrs/0023-shell-session-keeps-creation-lock.md)).

`ws` waits while the shell runs. Ctrl-C and Ctrl-\\ reach the shell
and its commands, and `ws` ignores them. SIGHUP and SIGTERM sent to `ws`
reach the shell as SIGHUP, so closing the terminal or `kill` on `ws` ends
the shell and removes the workspace; `ws` then exits with the shell's
status (129 from bash, 1 from zsh). A signal that arrives while `ws` is
removing the workspace doesn't stop the removal.

The shell's environment gains:

| Variable       | Value                       |
| -------------- | --------------------------- |
| `WS_ID`        | workspace id                |
| `WS_WORKSPACE` | absolute workspace path     |

`ws shell` exits with the shell's exit code, or 128+N when signal N
killed the shell.

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

# An empty scratch directory
$ ws shell
```

## Errors

See the [workspace creation errors](README.md#creating-a-workspace). If
the session can't be recorded or the shell can't be started, the
workspace is removed and `ws` fails with `internal` (exit 1). Otherwise
`ws shell` exits with the shell's exit code.

## See also

[`ws claude`](ws-claude.md),
[0014](../adrs/0014-command-set.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md),
[0023](../adrs/0023-shell-session-keeps-creation-lock.md)
