# ws machine

```
ws machine spawn [--name <name>] [--offline] [--hidden] [<spec>...] -- "<prompt>" [<claude args>...]
ws machine children [--all]
ws machine rm [--dry-run] [--keep-children] (<workspace>... | --all)
```

Commands for agents: start, list, and remove the workspaces your own
workspace started. People and the desktop app use `ws claude`, `ws list`,
and `ws rm` instead.

Every `ws machine` command acts for the **caller**: the workspace
containing the working directory, which is yours. There is no flag to act
for another workspace. Run outside a workspace, for example after
`cd /tmp`, every command fails with `outside_workspace`; `cd` back into
your workspace and retry.

Each command acts only on your **descendants**: the workspaces you started,
and the ones they started. You can't list or remove anything else,
including your own workspace.

Output is always JSON, as if `--json` were given; `--json` is accepted and
changes nothing. Errors are one JSON line on stderr.

## When to use it

- `ws machine spawn` to hand a task to a helper agent in its own
  workspace. It's your child, and is removed with you.
- `ws machine children` to find your helpers and reviews, and check on
  their sessions.
- `ws machine rm` to remove each helper as soon as you've used its
  result.

Run `ws llm machine <op>` for each one's guide.

## Exit codes

| Situation                                       | Error code            | Exit |
| ----------------------------------------------- | --------------------- | ---- |
| Run outside a workspace                         | `outside_workspace`   | 2    |
| An unknown operation, or wrong arguments or flags | `usage`             | 2    |
| `rm`: a workspace that isn't one you started, or theirs | `not_descendant` | 4  |

## Examples

```console
$ ws machine spawn --name fix-login buff-bear -- "Fix the login bug, push a branch, and open a PR"
$ ws machine children
$ ws machine rm fix-login-d8efb24f
```
