# ws loop

```
ws loop add [--json] --every <PERIOD> --prompt <TEXT> [--title <TITLE>] [--keep <N>] [--model <M>] [--permission-mode <P>] [--paused] <NAME> [<SPEC>...]
ws loop edit [--json] <NAME> [--title <TITLE>] [--every <PERIOD>] [--prompt <TEXT>] [--keep <N>] [--model <M>] [--permission-mode <P>] [--script <TEXT>] [--script-timeout <DUR>] [--var <KEY>=<VALUE>]... [--no-script] [--no-vars] [--no-specs] [<SPEC>...]
ws loop history [--json] [--limit <N>] [--run <SEQ> | --clear] <NAME>
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

## Scripts and variables

`ws loop add --script` gives a loop a gate: a shell script that must exit 0
for a run to start. Its stdout and stderr can be used in the prompt as
`{{.Stdout}}` and `{{.Stderr}}`. `--var KEY=VALUE` sets a variable for the
prompt (`{{.Vars.KEY}}`) and for the script (`$WS_LOOP_VAR_KEY`). See
`ws llm loop add` for the template values, the limits, and how to change a
loop: remove it and add it again.

## History

Each run is recorded in the loop's history, and a run's verdict is the one it
recorded with `ws machine result`. So a loop's prompt should end with "finish
with `ws machine result PASS|FAIL --file <report>`". `ws loop history` shows
the runs, and `ws llm loop history` has the details.

## Subcommands

- `ws loop add`: add a loop; see `ws llm loop add`.
- `ws loop history`: show a loop's runs and their verdicts; see `ws llm loop history`.
- `ws loop list`: list every loop; see `ws llm loop list`.
- `ws loop pause`: stop a loop from starting runs; see `ws llm loop pause`.
- `ws loop resume`: start a paused loop again; see `ws llm loop resume`.
- `ws loop rm`: delete a loop; see `ws llm loop rm`.

A name is 1 to 40 characters of `a-z`, `0-9`, and `-`, starting with a letter
or digit. It is the loop's ID. A loop may also have a title, up to 80
characters with upper case and spaces, which is shown in place of the name
when it is set.

## Exit codes

| Situation                                   | Error code       | Exit |
| ------------------------------------------- | ---------------- | ---- |
| Bad name, title, period, prompt, `--keep`, `--script-timeout`, or `--var`; a prompt template that doesn't parse or uses a missing value | `loop_invalid` | 2 |
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
