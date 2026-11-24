# ws shell

Create a workspace and open an interactive shell in it.

## Synopsis

```
ws shell [--env <name>] [OPTIONS] [<SPEC>...]
```

## Description

Creates a workspace from the repo specs ([Creating a
workspace](README.md#creating-a-workspace)), or an empty one when there are
none, then runs `$SHELL -i` (or
`/bin/sh -i` if `SHELL` is unset or empty) in it, in the foreground. With
exactly one repo, the shell starts in that repo's directory; `WS_WORKSPACE`
is still the workspace ([0028](../adrs/0028-single-repo-session-directory.md), proposed).

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

## Environments

With `--env <name>` the shell runs on that environment's machine, in a
workspace that lives as long as this terminal is open
([0072](../adrs/0072-remote-environments.md)). The environment's agent runs
its own `ws shell` in a pty, and `ws` sends keystrokes and size changes over
the daemon, putting the local terminal in raw mode and restoring it on every
exit. It needs a terminal on stdin (`usage` otherwise), and `--json` is
refused. When the shell exits, `ws shell` exits with its status. Terminals
don't survive a reconnect: if the environment goes offline, the shell ends and
`ws shell` exits 1 (`environment_offline`). The agent allows 8 terminals at
once (`terminal_limit`, exit 4), and an agent older than protocol 2 refuses
with `environment_outdated`.

`WS_ENVIRONMENT` is set by the environment agent in the pty of a remote shell,
so the `ws shell` that runs there stamps the workspace with the
environment's name. It is not for people.

Terminals share the agent's one connection to the controller, whose socket
bounds its unsent queue (`TCP_NOTSENT_LOWAT` 16 KiB). Under bulk output on a
10 Mbit/s uplink, a control request can wait about 0.2 s behind it; the
[spike](../design/remote-environments/terminal-spike.md) measured 0.195 s
against a design limit of 0.142 s.

## Options

| Option          | Description                       |
| --------------- | --------------------------------- |
| `--env <name>`  | Run the shell on this environment (`ws env list`), not here |
| `--name <name>` | Workspace name; the id is derived from it with a random suffix |
| `--offline`     | Don't fetch; use the cache as-is  |

## Examples

```console
# Try something on a branch, then throw it away
$ ws shell buff-bear@feat/new-thing
$ cargo test
$ exit

# Two repos side by side, starting in the workspace
$ ws shell --name api-bump buff-bear bobcob7/api@v2.1.0

# An empty scratch directory
$ ws shell
```

## Errors

See the [workspace creation errors](README.md#creating-a-workspace). If
the session can't be recorded or the shell can't be started, the
workspace is removed and `ws` fails with `internal` (exit 1). Otherwise
`ws shell` exits with the shell's exit code. With `--env`: `environment_unknown`
(exit 3), `environment_offline` and `environment_outdated` (exit 1), and
`terminal_limit` (exit 4).

## See also

[`ws claude`](ws-claude.md), [`ws attach`](ws-attach.md),
[0014](../adrs/0014-command-set.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md),
[0023](../adrs/0023-shell-session-keeps-creation-lock.md)
