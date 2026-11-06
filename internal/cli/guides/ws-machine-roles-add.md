# ws machine roles add

```
ws machine roles add <name> --description <d> [--model <m>] [--pass <V>]... [--fail <V>]... [--vars <K>]... [--claude-arg <A>]... (--prompt-file <f> | -)
```

Adds a role: writes `$CONFIG_DIR/machine/roles/<name>.md`. Run it only
when a person asks you to. The name is lowercase letters, digits, and
hyphens, and can't be any role's already, built in or the user's; to
change one, use `ws machine roles edit`.

The prompt template comes from `--prompt-file` or, with `-`, from stdin.
It is a Go template: `{{.Vars.KEY}}` for a required var,
`{{index .Vars "KEY"}}` for an optional one, `{{.Role}}` for the role's
name. Every var it uses must be listed with `--vars`. It's checked by
rendering it with every var set, and the file is written to a temp file,
parsed back, and renamed into place, so a role that wouldn't load is
never left behind.

## Flags

| Flag                | Meaning                                                    |
| ------------------- | ---------------------------------------------------------- |
| `--description <d>` | Required. One line, at most 120 characters                 |
| `--model <m>`       | Passed as `claude --model`; default: `claude`'s            |
| `--pass <V>`        | A verdict that means pass; repeatable or comma-separated; default `PASS` |
| `--fail <V>`        | A verdict that means fail; repeatable or comma-separated; default `FAIL` |
| `--vars <K>`        | A var name the prompt uses; repeatable or comma-separated  |
| `--claude-arg <A>`  | One more `claude` argument; repeatable, kept in order; not `--model` or `--settings` |
| `--prompt-file <f>` | Read the prompt template from this file                    |
| `-`                 | After the name: read the prompt template from stdin        |

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{"name": "docs-writer", "description": "Writes docs for one task", "model": "sonnet",
 "pass": ["PASS"], "fail": ["FAIL"], "vars": ["TASK", "TASKS"],
 "claude_args": ["--permission-mode", "auto"], "source": "user",
 "prompt": "Write the docs for {{.Vars.TASK}} in {{.Vars.TASKS}}. …",
 "path": "/home/me/.config/ws/machine/roles/docs-writer.md", "warnings": []}
```

The role, as `ws machine roles show` prints it.

## Exit codes

| Situation                                        | Error code     | Exit |
| ------------------------------------------------ | -------------- | ---- |
| No `--description`, no prompt, or both `--prompt-file` and `-` | `usage` | 2 |
| An invalid name, description, verdict, var name, or argument; a verdict in both lists; or a prompt that is blank, doesn't parse, or uses a var not in `--vars` | `role_invalid` | 2 |
| A role already has the name                      | `role_exists`  | 4    |

## Examples

```console
$ ws machine roles add docs-writer --description "Writes docs for one task" --model sonnet \
    --vars TASK,TASKS --claude-arg --permission-mode --claude-arg auto --prompt-file docs-writer.md
```
