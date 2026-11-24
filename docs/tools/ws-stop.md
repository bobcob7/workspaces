# ws stop

Stop the Claude Code agents of workspaces: locally with `claude stop`, or on
an environment with `--env` (`agent.stop`). The workspace is kept.

## Synopsis

```
ws stop [--env <name>] [--json] <workspace>...
```

## Options

| Option         | Description                                                    |
| -------------- | -------------------------------------------------------------- |
| `--env <name>` | Stop the agents on this environment (`ws env list`), not here  |
| `--json`       | Print the result below                                         |

Without `--env`, a workspace that is on exactly one environment is
`workspace_not_found`, and the message names it. The desktop app's Stop on a
remote workspace runs this; locally it keeps calling `claude stop`. Stopping
a `ws` runner agent on a remote isn't supported yet.

## Errors

| Situation                          | Error code            | Exit |
| ---------------------------------- | --------------------- | ---- |
| No workspace named                 | `usage`               | 2    |
| Workspace not found                | `workspace_not_found` | 3    |
| `--env` names no environment       | `environment_unknown` | 3    |
| The environment is offline         | `environment_offline` | 1    |

## Output

With `--json`, `{"stopped": [{"workspace", "session", "stopped", "environment"}]}`,
one item per workspace. `session` is `null` without an agent; `stopped` is
`false` when the agent had already ended. The full guide is `ws llm stop`.
