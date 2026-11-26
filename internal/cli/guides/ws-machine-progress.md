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
plan, then `step` as each step finishes. A step is one unit of your own
planned work, counted in the same unit as `--total`: `--total 10` means ten
`step` calls. Output is always JSON; `--json` changes nothing.

End with `ws machine result PASS`: a passing verdict sets `done` to `total` if
you declared a total, so you don't need to step to the end first, and a
failing one leaves `done` where it was. `show` is optional and not needed
before `result`; it is mainly for seeing your descendants' trees.

## Subcommands

- `set`: change your name, total, done, or note. Only the flags you give change:
  raising or lowering `--total` leaves `done` alone, and `--done` leaves `total` alone.
- `step`: add `--by` (default 1) to done.
- `show`: your progress and the tree of your descendants, with a rollup per workspace.

`set` and `step` print a small object, `{"id", "progress", "rollup", "warnings"}`:
your own record and your rollup. Only `show` adds the `tree` of descendants.

## Rules

- A name is up to 40 characters, a note up to 200, both one line.
- `0 <= done <= total <= 1000000`. To go past total, raise `--total` first.
- A rollup is the steps of that workspace and everything below it; `fraction`
  is `null` while no step is planned.
- `set` and `step` print a local-only rollup: children on other environments
  are added only by `show`.
- You write only your own record, and read yours and your descendants'.

## Exit codes

| Situation                                          | Error code          | Exit |
| -------------------------------------------------- | ------------------- | ---- |
| Run outside a workspace                            | `outside_workspace` | 2    |
| A bad name, note or step count; `done` over `total` | `progress_invalid`  | 2    |
| No flags for `set`, `--by` under 1, other flag errors | `usage`           | 2    |

## Examples

A whole task, from plan to verdict:

```console
$ ws machine progress set --name "count to 10" --total 10
$ ws machine progress step
$ ws machine progress step --note "halfway"
$ ws machine progress step --by 3
$ ws machine progress set --total 12
$ ws machine progress step --by 7
$ printf 'Counted to 10\n' | ws machine result PASS --file -
```
