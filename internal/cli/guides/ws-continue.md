# ws continue

```
ws continue [--dry-run] [--watch] [--json] [<workspace>...]
```

Tells agents whose turn ended at Claude's 5-hour or weekly usage limit to
carry on once the limit has reset. It types "Your usage limit has reset.
Continue where you left off." into each such session, as a person
replying would.

## When to use it

- A child of yours shows the limit message ("You've hit your 5-hour
  limit · resets …") and the reset time has passed. Name it:
  `ws continue --json <child>`. Don't continue other agents unless told
  to.
- A session is continued only when it is `idle`, its last message is the
  limit, and the reset was at least a minute ago. Anything else is left
  alone, including a `stopped` session: `ws restore` it first.
- On macOS nothing is sent unless the Mac is fully awake (not in a dark
  wake). Such sessions are reported as `asleep`.
- After 3 tries in a row that hit the limit again, at least 5 minutes
  apart, a session is `gave_up` until it does something else or someone
  replies.

`--watch` runs a pass every minute until interrupted, for a terminal or a
launchd job; it prints text only.

## Flags

| Flag        | Meaning                                         |
| ----------- | ----------------------------------------------- |
| `--dry-run` | Report what would be continued; send nothing    |
| `--watch`   | Repeat every minute, printing only what changed |
| `--json`    | Print the result as JSON (not with `--watch`)   |

## JSON output

```json
{
  "workspaces": [
    {"id": "fix-login-d8efb24f", "session": "e8cdf5d8", "state": "idle",
     "limit": {"type": "five_hour", "resets_at": "2026-10-29T04:30:00Z"},
     "action": "continued", "error": null},
    {"id": "docs-pass", "session": "3c11e0f2", "state": "working", "limit": null, "action": null, "error": null}
  ],
  "warnings": []
}
```

- `state` is `ws list`'s: `idle`, `working`, `starting`, `stopped`, or
  `unknown`; `null` when it wasn't read.
- `limit` is `null`, or the limit the turn ended at: `type` is
  `five_hour`, `seven_day`, or another value Claude reports.
- `action` is `continued`, `would_continue` (`--dry-run`), `waiting` (not
  reset yet, or tried less than 5 minutes ago), `asleep`, `gave_up`, or
  `null` for a session that isn't limited or isn't idle.
- `error` is `null` or `{"code", "message"}`. The exit code is the first
  failure's; the others are still continued.

## Exit codes

| Situation                                                   | Error code            | Exit |
| ----------------------------------------------------------- | --------------------- | ---- |
| `claude agents` failed; nothing is sent                     | `claude_failed`       | 1    |
| `--json` with `--watch`                                     | `usage`               | 2    |
| A named workspace not found, a shell workspace, or one whose session was never recorded | `workspace_not_found` | 3 |
| A named workspace still being handed to its session         | `workspace_busy`      | 4    |
| Claude Code lists its session in another directory          | `session_mismatch`    | 4    |
| A message wasn't recorded in the transcript within 15 s     | `claude_failed`       | 1    |

## Examples

```console
$ ws continue --dry-run --json
$ ws continue --json fix-login-d8efb24f
```
