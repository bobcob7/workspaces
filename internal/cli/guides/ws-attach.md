# ws attach

```
ws attach [--env <name>] <workspace>
```

Attaches this terminal to the Claude Code agent of a workspace. Locally it
runs `claude attach` on the workspace's session, replacing `ws`. With `--env`
it attaches through the daemon to the agent on that environment (`ws env
list`): `ws` puts the terminal in raw mode, sends keystrokes and size
changes to the remote agent, and shows its output, until the terminal closes.

`ws attach` is for people and the desktop app. Agents don't run it: it needs
an interactive terminal.

## When to use it

- To talk to a running agent, here or on another machine.
- Not to start an agent: `ws claude` does that.

A workspace is named by its id or path. Without `--env`, a workspace on
exactly one environment is `workspace_not_found`, and the error says which
environment to pass. A workspace whose agent is a shell or a `ws` runner
can't be attached (`usage`).

Detaching with Claude Code's own key leaves the agent running and `ws attach`
exits 0. If the environment goes offline, `ws attach` says so and exits 1;
the agent keeps running, and attaching again picks it up. The local terminal
is restored on every exit, including a signal. A terminal that sends input
faster than 128 KiB/s, or that isn't heard from for 60 s, is closed by the
agent.

## Flags

| Flag           | Meaning                                                       |
| -------------- | ------------------------------------------------------------- |
| `--env <name>` | Attach to the agent on that environment; default: here        |

`--json` is refused (`usage`): there is no result to print.

## Exit codes

| Situation                                     | Error code            | Exit |
| --------------------------------------------- | --------------------- | ---- |
| stdin isn't a terminal; not a Claude agent    | `usage`               | 2    |
| Workspace not found                           | `workspace_not_found` | 3    |
| Environment not enrolled                      | `environment_unknown` | 3    |
| Environment offline, or it went offline       | `environment_offline` | 1    |
| The agent doesn't speak terminals yet         | `environment_outdated` | 1   |
| The agent already has 8 terminals open        | `terminal_limit`      | 4    |
| `claude attach` failed                        | `claude_failed`       | 1    |

When the terminal closes normally, `ws attach` exits 0.

## Examples

```console
$ ws attach fix-login-d8efb24f
$ ws attach --env linux-server api-bump-5c01e7a2
```
