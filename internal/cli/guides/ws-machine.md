# ws machine

```
ws machine spawn [--runner claude|ws] [--endpoint <name>] [--name <name>] [--offline] [--hidden] [--role <name> [--var KEY=VALUE]...] [<spec>...] -- "<prompt>" [<claude args>... | --model <m>]
ws machine wait [--any] [--timeout <duration>] <workspace>...
ws machine result <VERDICT> [--file <path> | --file -]
ws machine children [--all]
ws machine envs
ws machine logs [--tail <n>] <workspace>
ws machine rm [--dry-run] [--keep-children] (<workspace>... | --all)
ws machine roles [show | add | edit | rm] …
ws machine memory [list | get | set | rm] …
ws machine progress [set | step | show] …
ws machine artifact [add | list | show | get | put | rm] …
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
- `ws machine progress` on any task of more than a few steps, and always on a
  long or orchestrated run: `set --name <short name> --total <n>` once you know
  the plan, then `step` as each step finishes. A person watches it as a progress
  bar, and children's progress nests under yours.
- `ws machine artifact` to share a file with a person, such as a design
  document, and to read a screenshot or log a person uploaded: `get` copies it
  into your workspace and prints its path; `add` and `put` store your file for
  them to read and comment on.
- `ws machine envs` to see which environments your children may start on,
  before `ws machine spawn --env <name>`.
- `ws machine children` to find your helpers and reviews, and check on
  their sessions and results.
- `ws machine logs` to see what a child did and why it stopped, once
  `wait` reports it `fail` or `missing`.
- `ws machine rm` to remove each helper as soon as you've used its
  result. Its result stays readable until your tree's root is removed.

Run `ws llm machine <op>` for each one's guide.

## Exit codes

| Situation                                       | Error code            | Exit |
| ----------------------------------------------- | --------------------- | ---- |
| Run outside a workspace                         | `outside_workspace`   | 2    |
| An unknown operation, or wrong arguments or flags; `--runner` isn't claude or ws; `--endpoint` with claude; after a ws child's prompt, anything but `--model <m>` | `usage` | 2    |
| `result`: a malformed or disallowed verdict, or a bad report | `result_invalid` | 2 |
| `roles add`, `edit`: an invalid role            | `role_invalid`        | 2    |
| `spawn --role`, `roles`: no role has the name   | `role_unknown`        | 3    |
| `spawn --runner ws`: no model, a broken `[runner]` table | `config_invalid` | 1    |
| `spawn --runner ws`: the endpoint isn't in config.toml, or the model isn't on it | `endpoint_unknown`, `model_not_served` | 3    |
| `spawn --runner ws`: the endpoint is unreachable, or the model has no tools | `endpoint_unreachable`, `model_without_tools` | 1    |
| `spawn --runner ws`: no daemon answers, or the runner didn't start | `daemon_unavailable`, `runner_failed` | 1    |
| `rm`, `wait`: a workspace that isn't one you started, or theirs | `not_descendant` | 4 |
| `roles add`: the name is taken; `roles rm`: a built-in | `role_exists`, `role_builtin` | 4 |
| `wait`: a child failed, or had no verdict       | `child_failed`        | 1    |
| `wait`: a child hadn't finished at the timeout  | `wait_timeout`        | 1    |
| `memory`: a bad key, or a value over the limits | `memory_invalid`, `memory_full` | 2, 4 |
| `memory`: your workspace isn't a loop run       | `not_loop_run`        | 4    |
| `progress`: a bad name, note or step count        | `progress_invalid`    | 2    |
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
