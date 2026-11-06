# ws machine roles edit

```
ws machine roles edit <name> [--description <d>] [--model <m>] [--pass <V>]... [--fail <V>]... [--vars <K>]... [--no-vars] [--claude-arg <A>]... [--no-claude-args] [--prompt-file <f> | -]
```

Changes a role's fields; those not given are kept. Run it only when a
person asks you to. `--pass`, `--fail`, `--vars`, and `--claude-arg`
replace the whole list; `--no-vars` and `--no-claude-args` empty it;
`--model ""` clears the model.

Editing a built-in writes a full copy to
`$CONFIG_DIR/machine/roles/<name>.md` first, so it becomes an `override`;
`ws machine roles rm <name>` undoes that. Children already started keep
the prompt, model, and verdicts they were started with.

## Flags

As for `ws machine roles add`, all optional, plus:

| Flag               | Meaning                       |
| ------------------ | ----------------------------- |
| `--no-vars`        | Remove every var name         |
| `--no-claude-args` | Remove every `claude` argument |

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{"name": "reviewer", "description": "Reviews one task's branch against its tasks file, changes no code, and records APPROVED or CHANGES_REQUESTED",
 "model": "opus", "pass": ["APPROVED"], "fail": ["CHANGES_REQUESTED"], "vars": ["TASK", "TASKS", "BASE", "KNOWN_ISSUES"],
 "claude_args": ["--permission-mode", "auto"], "source": "override",
 "prompt": "You are the reviewer who must sign off on task {{.Vars.TASK}}. …",
 "path": "/home/me/.config/ws/machine/roles/reviewer.md", "warnings": []}
```

The role, as `ws machine roles show` prints it.

## Exit codes

| Situation                                        | Error code       | Exit |
| ------------------------------------------------ | ---------------- | ---- |
| Nothing to change, `--vars` with `--no-vars`, `--claude-arg` with `--no-claude-args`, both `--prompt-file` and `-` | `usage` | 2 |
| A result that wouldn't be a valid role           | `role_invalid`   | 2    |
| No role has the name                             | `role_unknown`   | 3    |
| The user's file for the role can't be parsed; fix or remove it | `config_invalid` | 1 |

## Examples

```console
$ ws machine roles edit reviewer --model opus
$ ws machine roles edit implementer --prompt-file implementer.md
```
