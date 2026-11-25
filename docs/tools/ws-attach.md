# ws attach

Attach this terminal to a workspace's Claude Code agent, here or on an
environment.

## Synopsis

```
ws attach [--env <name>] <workspace>
```

## Description

Locally, `ws attach` runs `claude attach` on the workspace's session and
replaces `ws`. With `--env <name>` (`ws env list`) it opens a terminal on the
environment's agent through the daemon (`terminal.open`, kind `attach`):
`ws` puts the local terminal in raw mode, sends keystrokes and size changes
to the agent, and shows its output until the terminal closes. The local
terminal is restored on every exit, including a signal. It needs a terminal
on stdin, and `--json` is refused, since there is no result to print.

A workspace is named by its id or path. Without `--env`, a workspace that is
on exactly one environment is `workspace_not_found`, and the message says to
pass that `--env`. Only a Claude Code agent can be attached: a shell or a
`ws` runner workspace is `usage`.

Detaching with Claude Code's own key leaves the agent running, and `ws attach`
exits 0. On an environment, terminals don't survive a reconnect: if it goes
offline, `ws attach` prints `ws: <name> went offline; the agent keeps
running` and exits 1 (`environment_offline`); attaching again picks the agent
up. The controller pings every 15 s, and the agent closes a terminal it hasn't
heard from for 60 s, or whose input exceeds 128 KiB/s. The agent allows 8
terminals at once. The terminal protocol is in [`ws env`](ws-env.md#terminals).

Redrawing after a gap makes the program repaint by changing the terminal's
size by one row and back; Claude Code repaints, a plain shell shows only a
marker.

## Options

| Option         | Description                                              |
| -------------- | -------------------------------------------------------- |
| `--env <name>` | Attach to the agent on this environment; default: here   |

## Errors

| Situation                                     | Error code             | Exit |
| --------------------------------------------- | ---------------------- | ---- |
| stdin isn't a terminal; the workspace isn't a Claude agent; `--json` | `usage` | 2 |
| Workspace not found                           | `workspace_not_found`  | 3    |
| `--env` names no environment                  | `environment_unknown`  | 3    |
| The environment is offline, or went offline   | `environment_offline`  | 1    |
| The agent runs a protocol older than 2        | `environment_outdated` | 1    |
| The agent already has 8 terminals open        | `terminal_limit`       | 4    |
| The environment's OS has no pty support (today Windows) | `platform_unsupported` | 1 |
| `claude attach` failed                        | `claude_failed`        | 1    |

When the terminal closes normally, `ws attach` exits 0.

## Examples

```console
$ ws attach fix-login-d8efb24f
$ ws attach --env linux-server api-bump-5c01e7a2
```

## See also

[`ws shell`](ws-shell.md), [`ws stop`](ws-stop.md), [`ws env`](ws-env.md),
[0072](../adrs/0072-remote-environments.md),
[0074](../adrs/0074-mqtt-transport-embedded-broker.md)
