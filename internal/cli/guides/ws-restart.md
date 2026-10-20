# ws restart

```
ws restart [--no-wait | --force] [--json]
```

Replaces Claude Code's background service with one started from this
shell, then respawns every background session that has a process, so they
pick up new environment variables, MCP servers, or a Claude Code update.
Sessions keep their conversations.

## When to use it

**Never run `ws restart` unless a person told you to.** It interrupts
every background agent on the machine, not only yours: it restarts
Claude Code's background service and respawns every session, including
ones `ws` didn't start. A person runs it
after changing their shell environment or updating Claude Code, from a
shell that has the change.

To bring back agents that stopped, after a reboot, use `ws restore`
instead.

A session in the middle of a turn (`working`) is respawned once it
finishes; `ws` waits for it. Until it's respawned, a session keeps the
old environment and `ws list` marks it `stale`. Only one `ws restore` or
`ws restart` respawns at a time.

## Flags

| Flag        | Meaning                                                      |
| ----------- | ------------------------------------------------------------ |
| `--no-wait` | Respawn only sessions that aren't `working`, then exit; the rest stay `stale` |
| `--force`   | Respawn `working` sessions at once, interrupting their turn. Not with `--no-wait` |
| `--json`    | Print the result as JSON                                     |

## JSON output

```json
{
  "sessions": [
    {"id": "e8cdf5d8", "workspace": "fix-login", "outcome": "restarted", "restarted": true, "error": null},
    {"id": "7a40c2d9", "workspace": null, "outcome": "stale", "restarted": false, "error": null}
  ],
  "warnings": []
}
```

- `id` is the Claude Code session id. `workspace` is `null` for sessions
  `ws` didn't start, or that Claude Code lists outside their workspace.
- `outcome`: `restarted`, `stale` (still running with the old
  environment), or `gone` (Claude Code no longer has it). Accept values
  you don't know. `restarted` is `true` only for `restarted`.
- `error` is `null` for a session left `stale` by `--no-wait`, and
  `{"code", "message"}` for one left by a failure or an interruption. The
  exit code is the first failure's.

## Exit codes

| Situation                                                       | Error code      | Exit |
| --------------------------------------------------------------- | --------------- | ---- |
| An argument, or `--no-wait` with `--force`                      | `usage`         | 2    |
| `claude agents`, `claude daemon stop`, or `claude daemon run` failed before any session was restarted | `claude_failed` | 1 |
| A session wasn't restarted: its respawn failed (`claude_failed`), or `ws` was interrupted (`internal`) | first failure's | 1 |

## Examples

```console
# Only when a person asked for it
$ ws restart --json
$ ws restart --no-wait --json
```
