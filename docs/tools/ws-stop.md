# ws stop

Stop the Claude Code agents of workspaces: locally with `claude stop`, or on
an environment with `--env` (`agent.stop`). The workspace is kept.

## Synopsis

```
ws stop [--env <name>] [--json] <workspace>...
```

## Output

With `--json`, `{"stopped": [{"workspace", "session", "stopped", "environment"}]}`,
one item per workspace. `session` is `null` without an agent; `stopped` is
`false` when the agent had already ended. The full guide is `ws llm stop`.
