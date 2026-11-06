# ws machine roles

```
ws machine roles
ws machine roles show <name>
ws machine roles add <name> --description <d> [--model <m>] [--pass <V>]... [--fail <V>]... [--vars <K>]... [--claude-arg <A>]... (--prompt-file <f> | -)
ws machine roles edit <name> [--description <d>] [--model <m>] [--pass <V>]... [--fail <V>]... [--vars <K>]... [--no-vars] [--claude-arg <A>]... [--no-claude-args] [--prompt-file <f> | -]
ws machine roles rm <name>
```

Lists the roles children can be started as with `ws machine spawn
--role`. A role is a prompt template, a model, more `claude` arguments,
the var names its prompt uses, and the verdicts that mean pass and fail
for `ws machine result` and `ws machine wait`.

Three are built in: `implementer` (haiku: implements one task and pushes
its branch; vars `TASK`, `TASKS`, `BASE`, `BRANCH`), `reviewer` (sonnet:
reviews one task's branch; vars `TASK`, `TASKS`, `BASE`, and optional
`KNOWN_ISSUES`), and `verifier` (haiku: runs whole-branch checks; vars
`BRANCH`, `CHECKS`). The user's own roles, and overrides of the built-ins,
are files in `$CONFIG_DIR/machine/roles/<name>.md`.

`source` says where each comes from: `builtin`, `user` (a role the user
added), or `override` (the user's file replacing the built-in of that
name).

Unlike the other `ws machine` commands, `roles` works outside a
workspace.

## When to use it

- `ws machine roles` and `show` to choose a role and see the vars its
  prompt needs before `ws machine spawn --role`.
- `add`, `edit`, and `rm` change the user's configuration: run them only
  when a person asks you to. Run `ws llm machine roles <op>` for each
  one's guide.

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{"roles": [{"name": "implementer", "description": "Implements one task from a tasks file, pushes its branch, and records PASS or FAIL",
            "model": "haiku", "pass": ["PASS"], "fail": ["FAIL"], "vars": ["TASK", "TASKS", "BASE", "BRANCH"],
            "claude_args": ["--permission-mode", "auto"], "source": "builtin"}],
 "warnings": []}
```

Sorted by name. A role file that can't be parsed is skipped with a
warning.

## Exit codes

| Situation                                   | Error code       | Exit |
| ------------------------------------------- | ---------------- | ---- |
| An unknown operation, or an argument        | `usage`          | 2    |

## Examples

```console
$ ws machine roles
$ ws machine roles show reviewer
```
