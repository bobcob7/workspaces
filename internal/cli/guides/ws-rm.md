# ws rm

```
ws rm [--dry-run] [--json] <workspace>...
```

Removes workspaces now, with their sessions. For an agent workspace it
runs `claude stop` and `claude rm` on the session, then deletes the
workspace and its records.

## When to use it

- To remove a workspace you created, once its work is pushed or no longer
  needed. Never one you didn't create, unless told to.
- Everything in the workspace is deleted, whatever its git state:
  uncommitted changes, unpushed commits, and stashes are lost. Check with
  `git status` and `git log @{u}..` in each repo first, and push.
- Not needed after `claude rm <session>`: the workspace then goes on its
  own, at the start of a later `ws` command.

A workspace is named by its id, path, or agent session id. A running
shell's workspace is refused: the shell has to exit, which removes it.

## Flags

| Flag        | Meaning                                                      |
| ----------- | ------------------------------------------------------------ |
| `--dry-run` | Check and report what would be removed; remove nothing        |
| `--json`    | Print the result as JSON                                     |

## JSON output

```json
{
  "workspaces": [
    {"id": "fix-login", "removed": true, "error": null},
    {"id": "nope", "removed": false, "error": {"code": "workspace_not_found", "message": "…"}}
  ],
  "warnings": []
}
```

One item per workspace named, in order. `removed` is `false` with
`--dry-run`, except for a workspace the removal check at the start of
every command had already removed. `error` is `null` or
`{"code", "message"}`. The exit code is the first failure's; the others
are still removed.

## Exit codes

| Situation                                                    | Error code            | Exit |
| ------------------------------------------------------------ | --------------------- | ---- |
| No workspace given                                           | `usage`               | 2    |
| Workspace not found                                          | `workspace_not_found` | 3    |
| A shell workspace whose shell is running                     | `shell_running`       | 4    |
| Still being handed to its session by `ws claude` or `ws shell` | `workspace_busy`    | 4    |
| Claude Code lists its session in another directory; nothing is touched | `session_mismatch` | 4 |
| `claude agents`, `claude stop`, or `claude rm` failed; the workspace is kept | `claude_failed` | 1 |
| Its recorded directory isn't its own directory under the workspace root; nothing is deleted | `internal` | 1 |

Never delete a workspace's directory by hand, or retry a refused removal
another way: tell a person what `ws` said.

## Examples

```console
$ ws rm --json fix-login
$ ws rm --json e8cdf5d8            # by session id
$ ws rm --dry-run --json api-bump
```
