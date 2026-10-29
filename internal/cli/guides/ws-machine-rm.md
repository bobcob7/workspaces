# ws machine rm

```
ws machine rm [--dry-run] [--keep-children] (<workspace>... | --all)
```

Removes workspaces your workspace started, as `ws rm` does: each with its
session and its own descendants, deepest first. Every workspace named must
be one of your descendants. Any other, your own workspace and its
ancestors included, is refused as its item's error, `not_descendant`, and
the rest are still removed.

## When to use it

- To remove a helper as soon as you've used its result, and not leave
  finished helpers running.
- Everything in the workspace is deleted, whatever its git state. Make
  sure its work is pushed first.
- `--all` before you finish, to remove everything you started.
- `--dry-run` first on a workspace with children: they go too.

A workspace is named by its id, path, or agent session id.

## Flags

| Flag              | Meaning                                                     |
| ----------------- | ----------------------------------------------------------- |
| `--all`           | Remove every direct child, and with them their descendants; not with workspaces |
| `--dry-run`       | Check and report what would be removed, descendants included; remove nothing |
| `--keep-children` | Make the workspace's children yours instead of removing them, so they stay in your tree |

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{
  "workspaces": [
    {"id": "login-tests-3be0a9c1", "parent": "fix-login-d8efb24f", "removed": true, "error": null},
    {"id": "fix-login-d8efb24f", "parent": null, "removed": true, "error": null},
    {"id": "plan-auth-1a2b3c4d", "parent": null, "removed": false,
     "error": {"code": "not_descendant", "message": "plan-auth-1a2b3c4d is not a workspace this one started"}}
  ],
  "warnings": []
}
```

`ws rm --json`'s object: for each workspace named, in order, its
descendants deepest first and then itself. `parent` is set on a
descendant, `null` on a workspace named. `removed` is `false` with
`--dry-run`. The exit code is the first failure's; the others are still
removed.

## Exit codes

| Situation                                                    | Error code            | Exit |
| ------------------------------------------------------------ | --------------------- | ---- |
| Run outside a workspace                                      | `outside_workspace`   | 2    |
| Neither workspaces nor `--all`, or both                      | `usage`               | 2    |
| Workspace not found                                          | `workspace_not_found` | 3    |
| Not one you started, or theirs; your own workspace included  | `not_descendant`      | 4    |
| A shell workspace whose shell is running                     | `shell_running`       | 4    |
| Still being handed to its session                            | `workspace_busy`      | 4    |
| Claude Code lists its session in another directory           | `session_mismatch`    | 4    |
| A workspace it started couldn't be removed, so it's kept     | `child_kept`          | 4    |
| `claude stop` or `claude rm` failed; the workspace is kept   | `claude_failed`       | 1    |

Never retry a refused removal another way: tell a person what `ws` said.

## Examples

```console
$ ws machine rm fix-login-d8efb24f
$ ws machine rm e8cdf5d8              # by session id
$ ws machine rm --dry-run --all
$ ws machine rm --keep-children plan-tests-5c01e7a2
```
