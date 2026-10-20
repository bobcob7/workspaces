# ws llm

Print guidance for AI agents on how to use `ws`.

## Synopsis

```
ws llm [<COMMAND>...]
ws llm --all
```

## Description

Prints Markdown written for agents to stdout. It is meant to be read by an
agent before it uses `ws`, or pasted into an agent's instructions.

- `ws llm` prints the overview: what `ws` is for, its use cases, the rules
  agents must follow, the repo spec syntax, and one line per command
  pointing to its guide.
- `ws llm <COMMAND>` prints one command's guide: when to use it, its flags,
  its JSON output, its exit codes, and examples. `COMMAND` is a command path
  such as `claude`, `rm`, or `cache search`.
- `ws llm --all` prints the overview followed by every command's guide.

The text is built into the binary, so it always matches the installed
version. Unlike `--help`, which lists flags, these guides explain when and
how to use each command.

The overview's rules for agents:

- To hand work to another agent, use `ws claude --json <spec>... -- "<prompt>"`
  and keep the returned session id. Follow it with `claude logs <id>`.
- Don't remove, stop, or work in a workspace you didn't create unless told
  to.
- Parse `--json` output, never the tables.
- Never run `ws restart` unless told to: it interrupts every background
  agent on the machine, not only yours.
- Removing a workspace discards everything in it, whatever its git state.
  Push, or copy out, anything worth keeping before `ws rm` or `claude rm`.

## How agents find it

When `ws` first creates `$CONFIG_DIR/workspaces/`, it also writes
`$CONFIG_DIR/workspaces/CLAUDE.md`, which tells agents to run `ws llm`
before using `ws`. Claude Code loads `CLAUDE.md` files from the directories
above where it starts, so every Claude Code session in a workspace sees it.
The file is written through a temporary file linked into place, as
`config.toml` is, and is never overwritten, so it can be edited. If it
can't be written, `ws` adds a warning and goes on. Other agents need to be
told to run `ws llm`, for example from their own instruction files.

## Examples

```console
$ ws llm
# ws: workspaces for agents
...

$ ws llm claude
# ws claude
...

# Give a project's agents the full guide
$ ws llm --all > docs/ws-for-agents.md
```

## Errors

| Situation        | Error code | Exit |
| ---------------- | ---------- | ---- |
| Unknown command  | `usage`    | 2    |

## See also

[0014](../adrs/0014-command-set.md)
