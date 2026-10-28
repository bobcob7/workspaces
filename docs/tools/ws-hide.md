# ws hide, ws unhide

Hide a workspace from `ws list` and the desktop app's default views, or
show it again.

## Synopsis

```
ws hide [OPTIONS] <WORKSPACE>
ws unhide [OPTIONS] <WORKSPACE>
```

## Description

A hidden workspace is left out of `ws list` unless it's given `-a`, and
the desktop app keeps it in a collapsed Hidden group of the Agents list,
where it doesn't count toward "waiting for you"
([0043](../adrs/0043-hidden-workspaces.md)). Nothing else changes: its
session keeps running, and `ws rm`, `ws restore`, `ws restart`,
`ws rename`, and the removal pass treat it like any other workspace.

`ws hide` sets `hidden = true` in the workspace's record
(`state/<id>/workspace.toml`, which becomes `version = 2`); `ws unhide`
removes it again. Hiding a hidden workspace, or unhiding a visible one,
changes nothing and succeeds.

Workspaces can also start hidden: [`ws review start`](ws-review.md)
always hides the review's workspace, and [`ws claude --hidden`](ws-claude.md)
hides a new agent's.

`WORKSPACE` is its id, path, or agent session id, as for other commands.

## Options

| Option   | Description                                           |
| -------- | ----------------------------------------------------- |
| `--json` | Print `{"id", "name", "path", "hidden", "warnings"}`  |

## Errors

| Situation                 | Error code            | Exit |
| ------------------------- | --------------------- | ---- |
| Not exactly one workspace | `usage`               | 2    |
| Workspace not found       | `workspace_not_found` | 3    |

## Examples

```console
$ ws hide lint-sweep-5b1e93aa
lint-sweep-5b1e93aa  hidden

$ ws unhide --json review-buff-bear-41-9c04e2a-1f0c33d8
{"id":"review-buff-bear-41-9c04e2a-1f0c33d8","name":"review-buff-bear-41-9c04e2a","path":"/Users/me/Library/Application Support/workspaces/workspaces/review-buff-bear-41-9c04e2a-1f0c33d8","hidden":false,"warnings":[]}
```

## See also

[`ws list`](ws-list.md), [0043](../adrs/0043-hidden-workspaces.md)
