# ws rename

Change a workspace's display name.

## Synopsis

```
ws rename [OPTIONS] <WORKSPACE> <NAME>
```

## Description

Sets the name `ws list` and the desktop app show for a workspace, in its
record (`state/<id>/workspace.toml`). The workspace's id, its directory,
and its session's name in `claude agents` don't change, so a workspace can
be renamed any number of times without disturbing an agent working in it
([0036](../adrs/0036-workspace-id-suffix-and-rename.md)).

The name is kept as given, with surrounding whitespace trimmed: unlike
`--name`, which also derives the id, any characters are allowed except
control characters. A workspace created without `--name` can be given a
name too.

`WORKSPACE` is its id, path, or agent session id, as for other commands;
not its name, since names needn't be unique.

## Options

| Option   | Description                                      |
| -------- | ------------------------------------------------ |
| `--json` | Print `{"id", "name", "path", "warnings"}`       |

## Errors

| Situation                                      | Error code            | Exit |
| ---------------------------------------------- | --------------------- | ---- |
| Not exactly a workspace and a name             | `usage`               | 2    |
| The name is empty or holds a control character | `name_invalid`        | 2    |
| Workspace not found                            | `workspace_not_found` | 3    |

## Examples

```console
$ ws claude --name "This is a Test" buff-bear -- "Fix the login bug"
this-is-a-test-d8efb24f  e8cdf5d8  /Users/me/Library/Application Support/workspaces/workspaces/this-is-a-test-d8efb24f

$ ws rename this-is-a-test-d8efb24f "Login fix"
this-is-a-test-d8efb24f  Login fix

$ ws rename --json e8cdf5d8 "Login fix, take two"
{"id":"this-is-a-test-d8efb24f","name":"Login fix, take two","path":"/Users/me/Library/Application Support/workspaces/workspaces/this-is-a-test-d8efb24f","warnings":[]}
```

## See also

[`ws list`](ws-list.md), [0012](../adrs/0012-workspace-layout.md),
[0036](../adrs/0036-workspace-id-suffix-and-rename.md)
