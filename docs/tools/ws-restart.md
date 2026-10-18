# ws restart

Restart Claude Code's background service and every running background
session, so they pick up new environment variables, MCP servers, or a Claude
Code update, without interrupting work in progress.

## Synopsis

```
ws restart [OPTIONS]
```

## Description

Background agents run under Claude Code's background service, and inherit
its environment from whenever it was started, possibly hours ago in another
directory. Editing `~/.zshrc` or updating Claude Code doesn't reach them.
`ws restart` replaces the service with one started from **this** shell:

1. Records every background session that has a process, including ones
   `ws` didn't start.
2. Stops the service with `claude daemon stop --any --keep-workers`. The
   sessions keep running.
3. Starts a new service at once (`claude daemon run`, detached), with `ws`'s
   environment. If some other `claude` started one first, `ws` warns that
   it may have that process's environment; run `ws restart` again.
4. Respawns (`claude respawn`) each recorded session that isn't `working`.
   A `working` session is respawned as soon as it stops working; `ws` waits
   and shows which are left.

Run it from a shell that already has the change: a new terminal, or after
`source ~/.zshrc`.

Respawned sessions come back with their conversations. Sessions that
weren't running stay stopped; use [`ws restore`](ws-restore.md) for those.
Interactive `claude` sessions aren't affected. Until a session is
respawned, it keeps the old environment, and [`ws list`](ws-list.md) marks it
`stale`. If you interrupt `ws restart`, run it again later. See
[0018](../adrs/0018-restart-claude-daemon.md).

## Options

| Option      | Description                                         |
| ----------- | --------------------------------------------------- |
| `--no-wait` | Respawn only sessions that aren't `working`, then exit; the rest stay `stale` |
| `--force`   | Respawn `working` sessions at once; their current turn is interrupted |
| `--json`    | Print `{"sessions": [{"id", "workspace", "restarted", "error"}], "warnings"}` |

`workspace` is `null` for sessions `ws` didn't start, and `restarted` is
`false` for sessions left `stale` by `--no-wait`.

## Examples

```console
# GH_TOKEN rotated in ~/.zshrc
$ exec zsh
$ ws restart
restarted  fix-login            e8cdf5d8
restarted  -                    7a40c2d9
waiting    ws-20261018-k3f9qa   5d8d652c   working
restarted  ws-20261018-k3f9qa   5d8d652c

$ ws restart --no-wait
restarted  fix-login            e8cdf5d8
stale      ws-20261018-k3f9qa   5d8d652c   working; run `ws restart` again later
```

## Errors

| Situation                                   | Error code      | Exit |
| ------------------------------------------- | --------------- | ---- |
| `claude agents`, `claude daemon stop`, or `claude daemon run` failed | `claude_failed` | 1 |
| Some `claude respawn` failed; the others are still restarted, and each result carries its own `error` | `claude_failed` | 1 |

## See also

[`ws restore`](ws-restore.md), [`ws list`](ws-list.md),
[0016](../adrs/0016-output-contract.md),
[0018](../adrs/0018-restart-claude-daemon.md)
