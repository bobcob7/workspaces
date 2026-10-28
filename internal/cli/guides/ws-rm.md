# ws rm

```
ws rm [--dry-run] [--keep-children] [--json] <workspace>...
```

Removes workspaces now, with their sessions, and every workspace they
started (their descendants), deepest first. For an agent workspace it
runs `claude stop` and `claude rm` on the session, then deletes the
workspace and its records.

## When to use it

- To remove a workspace you created, once its work is pushed or no longer
  needed. Never one you didn't create, unless told to.
- Everything in the workspace is deleted, whatever its git state:
  uncommitted changes, unpushed commits, and stashes are lost. Check with
  `git status` and `git log @{u}..` in each repo first, and push.
- To clean up after yourself: remove each child (a helper from
  `ws claude`, a review) as soon as you've used its result. Find them with
  `ws list --json --parent <your workspace>`.
- Not needed after `claude rm <session>`: the workspace then goes on its
  own, with its descendants, at the start of a later `ws` command.
- Run `--dry-run` first on a workspace with children: they go too.

A workspace is named by its id, path, or agent session id. A running
shell's workspace is refused: the shell has to exit, which removes it.

## Flags

| Flag        | Meaning                                                      |
| ----------- | ------------------------------------------------------------ |
| `--dry-run` | Check and report what would be removed, descendants included; remove nothing |
| `--keep-children` | Make the workspace's children top-level instead of removing them |
| `--json`    | Print the result as JSON                                     |

## JSON output

```json
{
  "workspaces": [
    {"id": "login-tests-3be0a9c1", "parent": "fix-login-d8efb24f", "removed": true, "error": null},
    {"id": "fix-login-d8efb24f", "parent": null, "removed": true, "error": null},
    {"id": "nope", "parent": null, "removed": false, "error": {"code": "workspace_not_found", "message": "…"}}
  ],
  "warnings": []
}
```

One item per workspace removed: for each one named, in order, its
descendants deepest first and then itself. `parent` is set on a
descendant, `null` on a workspace named. If a descendant can't be
removed, it and its ancestors are kept (`child_kept` on the ancestors);
other branches still go. `removed` is `false` with
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
| A workspace it started couldn't be removed, so it's kept     | `child_kept`          | 4    |
| `claude agents`, `claude stop`, or `claude rm` failed; the workspace is kept | `claude_failed` | 1 |
| Its recorded directory isn't its own directory under the workspace root; nothing is deleted | `internal` | 1 |

Never delete a workspace's directory by hand, or retry a refused removal
another way: tell a person what `ws` said.

## Examples

```console
$ ws rm --json fix-login-d8efb24f
$ ws rm --json e8cdf5d8            # by session id
$ ws rm --dry-run --json api-bump-5c01e7a2
```
