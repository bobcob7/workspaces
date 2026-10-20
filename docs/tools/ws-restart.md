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
   environment. It reads the old service's pid with `claude daemon status`
   before the stop, and ignores it while it waits for the new one, so an
   old service still exiting isn't taken for another `claude`'s. If some other `claude` started one first, `ws` warns that
   it may have that process's environment; run `ws restart` again.
4. Respawns (`claude respawn`) each recorded session that isn't `working`,
   meaning in the middle of a turn: Claude Code reports its `status` as
   `busy` ([0024](../adrs/0024-claude-code-client.md)). A `working` session
   is respawned as soon as it stops working; `ws` waits and shows which are
   left.

Run it from a shell that already has the change: a new terminal, or after
`source ~/.zshrc`.

Only one `ws restore` or `ws restart` respawns at a time: `ws restart`
holds a lock on `state/.respawn.lock` from before it lists the sessions
until its last respawn has shown a process, and another run waits for it
(Ctrl-C stops the wait) ([0027](../adrs/0027-respawn-lock-and-record-versions.md)).
It waits up to 10 s for `claude daemon status` to report the new service
before respawning anything, and after each respawn it waits, as
[`ws restore`](ws-restore.md) does, up to 10 s for the session's new
process; if that doesn't show, it warns and goes on. While it waits for
`working` sessions it checks every 2 s, and on a terminal it shows them on
stderr as `waiting` lines. A session deleted before its respawn is shown
as `gone`.

Once the old service is stopped, `ws restart` starts the new one even if
interrupted, so the sessions are never left without a service:

- If `claude daemon stop` fails while the old service still runs, nothing
  is restarted and the command fails. If it fails and no service runs,
  `ws restart` warns and goes on. If `claude daemon status` fails too, it
  runs `claude daemon run` in case no service runs, restarts nothing, and
  fails.
- If `claude daemon run` fails, or no service shows up within 10 s, the
  sessions that aren't `working` are still respawned, even if interrupted
  until one respawn succeeds, since a respawn starts a service with `ws`'s
  environment if none runs. If no respawn succeeds, `ws` runs `claude
  daemon run` once more and waits again. If no service started either
  way, `ws` warns, `working` sessions are left `stale`, each with a
  `claude_failed` error, and, if no respawn succeeded, the warning says
  that background sessions stop within about a minute unless a service is
  started: run `ws restart` again, or `claude respawn <id>`. With no
  running session, the command fails instead.
- If interrupted, it respawns no more sessions (a respawn already begun
  finishes), leaves the rest `stale`, each with an error, and releases the
  lock; run it again later.

`claude daemon stop`, `claude daemon status`, and `claude respawn` run in
a process group of their own, so a Ctrl-C at the terminal doesn't
interrupt them partway.

As with [`ws rm`](ws-rm.md) and [`ws restore`](ws-restore.md), once any
session is reported, a failure is that session's, not the command's: it
is printed on stderr, or carried in the session's `error` with `--json`,
and the exit code is the first failure's
([0016](../adrs/0016-output-contract.md)).

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
| `--force`   | Respawn `working` sessions at once; their current turn is interrupted. Not with `--no-wait` |
| `--json`    | Print `{"sessions": [{"id", "workspace", "outcome", "restarted", "error"}], "warnings"}` |

`workspace` is `null` for sessions `ws` didn't start, or that Claude Code
lists outside their workspace. `outcome` is the first column of the text
output: `restarted`, `stale` (still running with the old environment,
including after a failed respawn), or `gone` (Claude Code no longer has the
session); consumers must accept values they don't know. `restarted` is
`true` only when `outcome` is `restarted`. `error` is `null` for a
session left `stale` by `--no-wait`, and set for one left `stale` by a
failure or an interruption.

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
| `claude agents` failed; nothing is changed | `claude_failed` | 1 |
| `claude daemon stop` failed while the old service still runs, or `claude daemon status` failed too; nothing is restarted | `claude_failed` | 1 |
| `claude daemon run` failed, or started no service, twice, and no session was running | `claude_failed` | 1 |
| `--no-wait` with `--force` | `usage` | 2 |
| Some session wasn't restarted: its `claude respawn` failed (`claude_failed`), it was `working` when the service didn't start (`claude_failed`), or ws was interrupted (`internal`); the others are still restarted, and each result carries its own `error` | first failure's | 1 |

## See also

[`ws restore`](ws-restore.md), [`ws list`](ws-list.md),
[0016](../adrs/0016-output-contract.md),
[0018](../adrs/0018-restart-claude-daemon.md),
[0024](../adrs/0024-claude-code-client.md)
