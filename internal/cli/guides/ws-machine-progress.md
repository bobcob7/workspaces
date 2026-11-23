# ws machine progress

```
ws machine progress set [--name <name>] [--total <n>] [--done <n>] [--note <text>]
ws machine progress step [--by <n>] [--note <text>]
ws machine progress show
```

Reports how many steps your task has and has finished, with a short name, so
a person watching a long run can see a progress bar. Report the steps you do
yourself; work you hand to children is theirs to report, and `show` adds it
up the tree. The children you spawn are nested under you automatically.

Start with `set --name <short name> --total <n>` as soon as you know the
plan, then `step` as each step finishes. Change `--total` when the plan
changes. Output is always JSON; `--json` changes nothing.

## Subcommands

- `set`: change your name, total, done, or note. Only the flags you give change.
- `step`: add `--by` (default 1) to done.
- `show`: your progress and the tree of your descendants, with a rollup per workspace.

## Rules

- A name is up to 40 characters, a note up to 200, both one line.
- `0 <= done <= total <= 1000000`. To go past total, raise `--total` first.
- A rollup is the steps of that workspace and everything below it; `fraction`
  is `null` while no step is planned.
- You write only your own record, and read yours and your descendants'.

## Exit codes

| Situation                                          | Error code          | Exit |
| -------------------------------------------------- | ------------------- | ---- |
| Run outside a workspace                            | `outside_workspace` | 2    |
| A bad name, note or step count; `done` over `total` | `progress_invalid`  | 2    |
| No flags for `set`, `--by` under 1, other flag errors | `usage`           | 2    |

## Examples

```console
$ ws machine progress set --name "add login" --total 4
$ ws machine progress step --note "tests written"
$ ws machine progress set --total 5
$ ws machine progress show
```
