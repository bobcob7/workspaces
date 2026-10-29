# ws llm

```
ws llm [<command>...]
ws llm --all
```

Prints guides for agents, as Markdown on stdout. The text is built into
`ws`, so it matches the installed version. It reads and writes nothing
on disk.

- `ws llm`: the overview: rules, use cases, lifetime, specs, output, and
  the list of commands.
- `ws llm <command>`: one command's guide, such as `ws llm machine` or
  `ws llm machine spawn`.
- `ws llm --all`: the overview followed by every command's guide, for
  example to save into a project's agent instructions.

## Flags

| Flag    | Meaning                                         |
| ------- | ----------------------------------------------- |
| `--all` | Print every guide; not with a command           |

`--json` is refused (`usage`): the guides are Markdown.

## Exit codes

| Situation                                | Error code | Exit |
| ---------------------------------------- | ---------- | ---- |
| Unknown command, or a command with `--all` | `usage`  | 2    |

## Examples

```console
$ ws llm
$ ws llm machine
$ ws llm --all > docs/ws-for-agents.md
```
