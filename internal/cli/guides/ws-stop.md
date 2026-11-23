# ws stop

```
ws stop [--env <name>] [--json] <workspace>...
```

Stops the Claude Code agents of workspaces. Locally it runs `claude stop`
on each workspace's session and keeps the workspace, so its conversation
can still be reviewed; `claude rm` or `ws rm` removes it. With `--env` it
stops each agent on that environment (`ws env list`), as the desktop app's
Stop does for a remote agent.

`ws stop` is for people and the desktop app. Agents don't run it: use `ws machine` (see `ws llm machine`), or tell a person what you need.

## When to use it

- To stop an agent that is running, without removing its workspace.
- Not to remove a workspace: `ws rm` does that.

A workspace is named by its id, path, or agent session id. Without `--env`,
a workspace on exactly one environment is `workspace_not_found`, and the
error says which environment to pass.

## Flags

| Flag          | Meaning                                                      |
| ------------- | ------------------------------------------------------------ |
| `--env <name>` | Stop the agents on that environment (`ws env list`); default: here |
| `--json`      | Print the result as JSON                                     |

## JSON output

```json
{"stopped": [{"workspace": "fix-login-d8efb24f", "session": "e8cdf5d8", "stopped": true, "environment": "local"}]}
```

One item per workspace, in the order given. `session` is `null` when the
workspace has no Claude Code agent. `stopped` is `false` when there is no
agent or its session has already ended; that is not an error. The first
failure ends the command.

## Exit codes

| Situation                                | Error code            | Exit |
| ---------------------------------------- | --------------------- | ---- |
| No workspace given                       | `usage`               | 2    |
| Workspace not found                      | `workspace_not_found` | 3    |
| Environment not enrolled                 | `environment_unknown` | 3    |
| Environment offline                      | `environment_offline` | 1    |
| `claude stop` failed                     | `claude_failed`       | 1    |

## Examples

```console
$ ws stop --json fix-login-d8efb24f
$ ws stop --env linux-server --json api-bump-5c01e7a2
```
