# ws restore

```
ws restore [--dry-run] [--json] [<workspace>...]
```

Restarts the agents of agent workspaces whose Claude Code session has no
process, with `claude respawn`. Each comes back with its conversation and
the options it was started with, waiting for input; reply with
`claude attach <session>` to have it carry on.

`ws restore` is for people and the desktop app. Agents don't run it: use `ws machine` (see `ws llm machine`), or tell a person what you need.

## When to use it

- After a reboot, or when Claude Code's background service was down for
  more than about a minute: background sessions then stop and don't come
  back on their own. `ws list` shows them as `stopped`.
- Without arguments it restores every agent workspace. Name workspaces to
  restore only those; restore only your own unless told to.
- A session that is running (`idle` or `working`) or `starting` is left
  alone, and so is one Claude Code doesn't list (`unknown`), such as one
  deleted with `claude rm`. Shell workspaces aren't restored.

Sessions are respawned one at a time, oldest first or in the order named,
waiting up to 10 s for each to show a process. Only one `ws restore` or
`ws restart` respawns at a time; another waits for it.

## Flags

| Flag        | Meaning                                                |
| ----------- | ------------------------------------------------------ |
| `--dry-run` | Report what would be restored; change nothing           |
| `--json`    | Print the result as JSON                               |

## JSON output

```json
{
  "workspaces": [
    {"id": "fix-login-d8efb24f", "session": "e8cdf5d8", "state": "stopped", "restored": true, "error": null},
    {"id": "docs-pass", "session": "3c11e0f2", "state": "idle", "restored": false, "error": null}
  ],
  "warnings": []
}
```

- `state` is the session before restoring, with `ws list`'s values:
  `stopped` (restored, or would be with `--dry-run`), `starting`, `idle`,
  `working`, or `unknown`; `null` when it wasn't read, as for a workspace
  that wasn't found.
- `restored` is `true` only when `claude respawn` succeeded.
- `error` is `null` or `{"code", "message"}`. The exit code is the first
  failure's; the others are still restored.

## Exit codes

| Situation                                                   | Error code            | Exit |
| ----------------------------------------------------------- | --------------------- | ---- |
| `claude agents` failed; nothing is restored                 | `claude_failed`       | 1    |
| A named workspace not found, a shell workspace, or one whose session was never recorded | `workspace_not_found` | 3 |
| A named workspace still being handed to its session         | `workspace_busy`      | 4    |
| Claude Code lists its session in another directory; nothing is touched | `session_mismatch` | 4 |
| A `claude respawn` failed                                   | `claude_failed`       | 1    |

## Examples

```console
$ ws restore --dry-run --json
$ ws restore --json fix-login-d8efb24f
```
