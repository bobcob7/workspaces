# ws daemon

Decided in [0068](../adrs/0068-ws-daemon.md). Each subcommand is in the
[`ws llm`](ws-llm.md) guides: `ws llm daemon run`, `status` and `stop`.

```
ws daemon run [--origin transient|managed|app]
ws daemon status [--json]
ws daemon stop [--keep-runners] [--json]
```

`ws`'s background service. It starts, watches and restarts the agents
that run on `ws`'s own runner (`ws machine spawn --runner ws`), resumes
the ones a crash, reboot or container restart interrupted, and gives them
their environment: its own, then `[runner.env]` from `config.toml`, then
`CONFIG_DIR/secrets.env`. There's one per config directory, reached over a
Unix socket only you can open.

`ws daemon` is for people, the desktop app, `ws-online` and service
managers. Agents don't run it: use `ws machine` (see `ws llm machine`), or
tell a person what you need.

## When to use it

- Usually never: the desktop app keeps one running while it's open,
  `ws-online` keeps one running, and the commands that need it start one
  otherwise (`ws machine spawn --runner ws`, `ws claude --runner ws`, a
  loop run on the runner, `ws restore`, `ws rm` of such an agent).
- `ws daemon status` to see whether it runs, since when, and which runners
  it watches.
- `ws daemon run` under launchd or systemd, so runners resume after a
  reboot without anything else running; see `ws llm daemon run`.
- `ws daemon stop --keep-runners` after changing your shell environment:
  the next command that needs the daemon starts a new one from your shell,
  and it adopts the running runners.

Claude Code agents don't use it: they run under Claude Code's own service,
which `ws restart` restarts.

## Subcommands

- `ws daemon run`: run it in the foreground
- `ws daemon status`: whether it runs, and its runners
- `ws daemon stop`: stop it, and interrupt or keep its runners

## Exit codes

| Situation                                        | Error code           | Exit |
| ------------------------------------------------ | -------------------- | ---- |
| An unknown subcommand or a flag error            | `usage`              | 2    |
| `run`: another daemon already runs for this config directory | `daemon_running` | 4 |
| `status`, `stop`: the socket answered with an error | `daemon_unavailable` | 1  |

## Examples

```console
$ ws daemon status
$ ws daemon stop --keep-runners
$ ws daemon run --origin managed
```
