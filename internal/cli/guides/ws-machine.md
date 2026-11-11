# ws machine

```
ws machine spawn [--name <name>] [--offline] [--hidden] [--role <name> [--var KEY=VALUE]...] [<spec>...] -- "<prompt>" [<claude args>...]
ws machine wait [--any] [--timeout <duration>] <workspace>...
ws machine result <VERDICT> [--file <path> | --file -]
ws machine children [--all]
ws machine rm [--dry-run] [--keep-children] (<workspace>... | --all)
ws machine roles [show | add | edit | rm] …
ws machine memory [list | get | set | rm] …
```

Commands for agents: start, wait for, list, and remove the workspaces your
own workspace started, record your own verdict, and see the roles children
can be started as. These are the only `ws` commands an agent runs, apart
from `ws review` to get its work reviewed. People and the desktop app use
`ws claude`, `ws list`, and `ws rm` instead; don't run those, or any other
`ws` command, even when one seems to fit. If `ws machine` can't do what
you need, tell a person.

Every `ws machine` command acts for the **caller**: the workspace
containing the working directory, which is yours. There is no flag to act
for another workspace. Run outside a workspace, for example after
`cd /tmp`, every command but `roles` fails with `outside_workspace`; `cd`
back into your workspace and retry.

Each command acts only on your **descendants**: the workspaces you started,
and the ones they started. You can't wait on, list, or remove anything
else, including your own workspace.

Output is always JSON, as if `--json` were given; `--json` is accepted and
changes nothing. Errors are one JSON line on stderr.

## When to use it

- `ws machine spawn` to hand a task to a helper agent in its own
  workspace. It's your child, and is removed with you. `--role` starts it
  from a stored prompt and model, such as `implementer`, `reviewer`, or
  `verifier`; `ws machine roles` lists them.
- `ws machine wait`, as a background command, to wait for children to
  finish and get each one's verdict and report.
- `ws machine result` as your own last step, when an agent started you
  and waits on you: record your verdict and report.
- `ws machine memory` as a loop run, to keep notes for the loop's next runs:
  `set` a value, then `get` or `list` it; use `--expect-rev` when runs overlap.
- `ws machine children` to find your helpers and reviews, and check on
  their sessions and results.
- `ws machine rm` to remove each helper as soon as you've used its
  result. Its result stays readable until your tree's root is removed.

Run `ws llm machine <op>` for each one's guide.

## Exit codes

| Situation                                       | Error code            | Exit |
| ----------------------------------------------- | --------------------- | ---- |
| Run outside a workspace                         | `outside_workspace`   | 2    |
| An unknown operation, or wrong arguments or flags | `usage`             | 2    |
| `result`: a malformed or disallowed verdict, or a bad report | `result_invalid` | 2 |
| `roles add`, `edit`: an invalid role            | `role_invalid`        | 2    |
| `spawn --role`, `roles`: no role has the name   | `role_unknown`        | 3    |
| `rm`, `wait`: a workspace that isn't one you started, or theirs | `not_descendant` | 4 |
| `roles add`: the name is taken; `roles rm`: a built-in | `role_exists`, `role_builtin` | 4 |
| `wait`: a child failed, or had no verdict       | `child_failed`        | 1    |
| `wait`: a child hadn't finished at the timeout  | `wait_timeout`        | 1    |
| `memory`: a bad key, or a value over the limits | `memory_invalid`, `memory_full` | 2, 4 |
| `memory`: your workspace isn't a loop run       | `not_loop_run`        | 4    |
| `memory`: a key that isn't set, or `--expect-rev` didn't match | `memory_not_found`, `memory_conflict` | 3, 4 |

## Examples

```console
$ ws machine spawn --name fix-login buff-bear -- "Fix the login bug, push a branch, and open a PR"
$ ws machine spawn --name t1-impl --role implementer --var TASK=T1 --var TASKS=docs/tasks.md --var BASE=feat/x --var BRANCH=feat/x-t1 buff-bear@feat/x
$ ws machine wait t1-impl-d8efb24f
$ ws machine result PASS --file ../report.md
$ ws machine children
$ ws machine rm fix-login-d8efb24f
$ ws machine roles
```
