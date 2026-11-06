# ws machine roles show

```
ws machine roles show <name>
```

Prints one role with its prompt template, so you can see which vars to
pass to `ws machine spawn --role <name> --var KEY=VALUE`, and which
verdicts its children record.

In the prompt, `{{.Vars.KEY}}` is required: spawning without
`--var KEY=…` fails. `{{index .Vars "KEY"}}` is optional and renders
empty when not given.

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{"name": "implementer", "description": "Implements one task from a tasks file, pushes its branch, and records PASS or FAIL",
 "model": "haiku", "pass": ["PASS"], "fail": ["FAIL"], "vars": ["TASK", "TASKS", "BASE", "BRANCH"],
 "claude_args": ["--permission-mode", "auto"], "source": "builtin",
 "prompt": "You implement exactly one task, {{.Vars.TASK}}, in this repo. …",
 "path": null, "warnings": []}
```

`path` is the role's file, or `null` for a built-in.

## Exit codes

| Situation                     | Error code     | Exit |
| ----------------------------- | -------------- | ---- |
| Not exactly one name          | `usage`        | 2    |
| No role has the name          | `role_unknown` | 3    |

## Examples

```console
$ ws machine roles show implementer
```
