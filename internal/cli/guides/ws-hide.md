# ws hide

```
ws hide [--json] <workspace>
```

Hides a workspace: `ws list` leaves it out unless given `-a`, and the
desktop app moves it to its collapsed Hidden group, where it doesn't count
as waiting for input. Nothing else changes: the session keeps running,
and `ws rm`, `ws restore`, `ws restart`, and `ws rename` work on it as
before. `ws unhide` shows it again. Hiding a hidden workspace does
nothing.

`ws hide` is for people and the desktop app. Agents don't run it: use `ws machine` (see `ws llm machine`), or tell a person what you need.

## When to use it

- To hide a workspace you no longer need to follow. To hide one from the
  start, use `ws claude --hidden`; an agent uses
  `ws machine spawn --hidden`.
- Review workspaces from `ws review start` are already hidden.

A workspace is named by its id, path, or agent session id.

## JSON output

```json
{"id": "lint-sweep-5b1e93aa", "name": "lint-sweep", "path": "/…/workspaces/lint-sweep-5b1e93aa", "hidden": true, "warnings": []}
```

`name` is `null` for a workspace created without one.

## Exit codes

| Situation                    | Error code            | Exit |
| ---------------------------- | --------------------- | ---- |
| Not exactly one workspace    | `usage`               | 2    |
| Workspace not found          | `workspace_not_found` | 3    |

## Examples

```console
$ ws hide --json lint-sweep-5b1e93aa
$ ws hide --json 0c9e1a7b   # by session id
```
