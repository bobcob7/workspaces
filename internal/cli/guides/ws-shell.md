# ws shell

```
ws shell [--name <name>] [--offline] <spec>...
```

Creates a workspace from the repo specs and runs an interactive shell
(`$SHELL -i`, or `/bin/sh -i`) in it, in the foreground. When the shell
exits, the workspace is removed, whatever its git state.

## When to use it

It's for people at a terminal. An agent shouldn't use it: it needs an
interactive terminal, blocks until the shell exits, and everything in the
workspace is lost then. To give work to an agent, use `ws claude`.

Inside the shell, `WS_ID` is the workspace id and `WS_WORKSPACE` its path.
While the shell, or anything started from it, runs, `ws rm` refuses to
remove the workspace (`shell_running`). If `ws` is killed, the workspace
is removed by a later `ws` command once nothing from the shell runs.

## Flags

| Flag            | Meaning                                                     |
| --------------- | ----------------------------------------------------------- |
| `--name <name>` | Make the workspace id from this name, as `ws claude --name` does |
| `--offline`     | Don't fetch; use the cache as it is                         |

`--json` is refused (`usage`): there is no result to print.

## Exit codes

Before the shell starts, the workspace creation errors of `ws claude`
apply (`usage`, `spec_invalid`, `name_invalid`, `repo_not_found`, `repo_ambiguous`,
`ref_not_found`, `workspace_exists`, `fetch_failed`, `clone_failed`,
`git_failed`), and `internal` (exit 1) if the shell can't be started.
Once the shell has started, `ws shell` exits with the shell's exit code, or
128+N when signal N killed it.

## Examples

```console
# Try a branch, then throw it away
$ ws shell buff-bear@feat/new-thing
$ exit

# Two repos side by side
$ ws shell --name api-bump buff-bear bobcob7/api@v2.1.0
```
