# ws loop

```
ws loop add [--json] --every <PERIOD> --prompt <TEXT> [--title <TITLE>] [--keep <N>] [--model <M>] [--permission-mode <P>] [--paused] [--hidden[=BOOL] | --show] <NAME> [<SPEC>...]
ws loop edit [--json] <NAME> [--title <TITLE>] [--every <PERIOD>] [--prompt <TEXT>] [--keep <N>] [--model <M>] [--permission-mode <P>] [--script <TEXT>] [--script-timeout <DUR>] [--var <KEY>=<VALUE>]... [--no-script] [--no-vars] [--no-specs] [--hidden[=BOOL] | --show] [<SPEC>...]
ws loop history [--json] [--limit <N>] [--run <SEQ> | --clear] <NAME>
ws loop memory list|get|set|rm|clear [--json] <NAME> …
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

## Ending a run, and asking for help

A run's agent should end by recording a verdict with `ws machine result`,
so the run's history says how it went:

- `ws machine result PASS --file ../report.md` when it did its job, or
  `FAIL` when it couldn't.
- To ask a person, write the question to a file and run
  `ws machine result BLOCKED --file ../question.md`, then stop. The run
  is shown in the Agents list, even when its loop hides runs, and the
  app notifies once. When the person answers in the conversation, the
  agent carries on and records `PASS`, `FAIL` or `BLOCKED` again; until
  it does, the run stays blocked.

A run that goes idle without recording a verdict needs attention: it is
shown and notified the same way. End the prompt with the verdict step,
for example:

> When you're done, write a short report to ../report.md and run
> `ws machine result PASS --file ../report.md`, or `FAIL` if you
> couldn't finish. If you need a decision from a person, write your
> question to ../question.md, run
> `ws machine result BLOCKED --file ../question.md`, and stop.

Runs start hidden unless the loop was added with `--show`; see `--hidden`.

## History

Each run is recorded in the loop's history, and a run's verdict is the one it
recorded with `ws machine result`. So a loop's prompt should end with "finish
with `ws machine result PASS|FAIL --file <report>`". `ws loop history` shows
the runs, and `ws llm loop history` has the details.

## Memory

A loop's runs can remember values between runs. A prompt reads one with
`{{memory "KEY"}}`, which is the value or an empty string, and a gate script
reads the file at `$WS_LOOP_MEMORY`. A run writes one with `ws machine memory
set`. People use `ws loop memory`; see `ws llm loop memory`. A loop keeps at most
100 keys, 64 KiB per value, and 256 KiB in all.

## Subcommands

- `ws loop add`: add a loop; see `ws llm loop add`.
- `ws loop history`: show a loop's runs and their verdicts; see `ws llm loop history`.
- `ws loop memory`: read and change a loop's memory; see `ws llm loop memory`.
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
