# ws loop

```
ws loop add [--json] --every <PERIOD> --prompt <TEXT> [--keep <N>] [--model <M>] [--permission-mode <P>] [--paused] <NAME> [<SPEC>...]
ws loop list [--json]
ws loop pause [--json] <NAME>
ws loop resume [--json] <NAME>
ws loop rm [--json] <NAME>
```

A loop is a named prompt with a period. Every period, a run starts a new
Claude Code agent in its own workspace, with the same prompt, as `ws claude`
would. Runs happen only while something runs `ws loop tick`, which the
desktop app or a launchd agent does; this guide covers the records.

`ws loop` is for people and the desktop app. Agents don't run it.

## When to use it

- To have an agent do the same recurring job, such as bumping dependencies
  or checking a status page, without starting it by hand each time.
- A loop's runs are ordinary workspaces: `ws list`, `ws rm`, and
  `ws continue` work on them.

## Subcommands

- `ws loop add`: add a loop; see `ws llm loop add`.
- `ws loop list`: list every loop; see `ws llm loop list`.
- `ws loop pause`: stop a loop from starting runs; see `ws llm loop pause`.
- `ws loop resume`: start a paused loop again; see `ws llm loop resume`.
- `ws loop rm`: delete a loop; see `ws llm loop rm`.

A name is 1 to 40 characters of `a-z`, `0-9`, and `-`, starting with a letter
or digit.

## Exit codes

| Situation                                   | Error code       | Exit |
| ------------------------------------------- | ---------------- | ---- |
| Bad name, period, prompt, or `--keep`       | `loop_invalid`   | 2    |
| A spec that can't be parsed                 | `spec_invalid`   | 2    |
| Missing or extra arguments, or a flag error | `usage`          | 2    |
| No loop has that name                       | `loop_not_found` | 3    |
| `add` with a name already used              | `loop_exists`    | 4    |

## Examples

```console
$ ws loop add deps-bump --every 24h --permission-mode acceptEdits \
    --prompt "Update the Go and npm dependencies, run the tests, and open a PR" buff-bear
$ ws loop list
$ ws loop pause deps-bump
$ ws loop resume deps-bump
$ ws loop rm deps-bump
```
