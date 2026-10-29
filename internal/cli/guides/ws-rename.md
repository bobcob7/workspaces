# ws rename

```
ws rename [--json] <workspace> <name>
```

Changes a workspace's display name: the name `ws list` and the desktop
app show. The workspace's id, its directory, and its session don't
change, so a workspace can be renamed any number of times.

`ws rename` is for people and the desktop app. Agents don't run it: use `ws machine` (see `ws llm machine`), or tell a person what you need.

## When to use it

- To give a workspace you created a clearer name once its task is known.
  Never rename one you didn't create unless told to.
- Not to find a workspace: commands take its id, path, or session id, not
  its name, since names needn't be unique.

A workspace is named by its id, path, or agent session id. Surrounding
spaces are trimmed; any characters are kept, unlike `--name`, which also
makes the id. Put `--` before the arguments when the name starts with `-`:
`ws rename -- fix-login-d8efb24f "-draft"`.

## JSON output

```json
{"id": "fix-login-d8efb24f", "name": "Fix the login page", "path": "/…/workspaces/fix-login-d8efb24f", "warnings": []}
```

## Exit codes

| Situation                                      | Error code            | Exit |
| ---------------------------------------------- | --------------------- | ---- |
| Not exactly a workspace and a name             | `usage`               | 2    |
| The name is empty or holds a control character | `name_invalid`        | 2    |
| Workspace not found                            | `workspace_not_found` | 3    |

## Examples

```console
$ ws rename --json fix-login-d8efb24f "Fix the login page"
$ ws rename --json e8cdf5d8 "Login fix"   # by session id
```
