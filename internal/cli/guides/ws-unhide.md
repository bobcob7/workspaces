# ws unhide

```
ws unhide [--json] <workspace>
```

Shows a hidden workspace again: `ws list` lists it without `-a`, and the
desktop app's Agents list shows it in its usual group. Unhiding a visible
workspace does nothing. See `ws llm hide`.

`ws unhide` is for people and the desktop app. Agents don't run it: use `ws machine` (see `ws llm machine`), or tell a person what you need.

## When to use it

- When the user should follow a hidden workspace, such as a review they
  want to watch in the Agents list.

A workspace is named by its id, path, or agent session id.

## JSON output

```json
{"id": "lint-sweep-5b1e93aa", "name": "lint-sweep", "path": "/…/workspaces/lint-sweep-5b1e93aa", "hidden": false, "warnings": []}
```

## Exit codes

| Situation                    | Error code            | Exit |
| ---------------------------- | --------------------- | ---- |
| Not exactly one workspace    | `usage`               | 2    |
| Workspace not found          | `workspace_not_found` | 3    |

## Examples

```console
$ ws unhide --json review-buff-bear-41-9c04e2a-1f0c33d8
```
