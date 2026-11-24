# ws shell

```
ws shell [--env <name>] [--name <name>] [--offline] [<spec>...]
```

Creates a workspace from the repo specs, or an empty one with none, and
runs an interactive shell (`$SHELL -i`, or `/bin/sh -i`) in it, in the
foreground. When the shell
exits, the workspace is removed, whatever its git state.

## When to use it

It's for people at a terminal. An agent shouldn't use it: it needs an
interactive terminal, blocks until the shell exits, and everything in the
workspace is lost then. To give work to an agent, a person uses
`ws claude`, and an agent `ws machine spawn`.

The shell starts in the workspace, or in the repo's directory when there is
exactly one repo. Inside the shell, `WS_ID` is the workspace id and
`WS_WORKSPACE` its path.
While the shell, or anything started from it, runs, `ws rm` refuses to
remove the workspace (`shell_running`). If `ws` is killed, the workspace
is removed by a later `ws` command once nothing from the shell runs.

## Flags

| Flag            | Meaning                                                     |
| --------------- | ----------------------------------------------------------- |
| `--name <name>` | Name the workspace and make its id from the name, as `ws claude --name` does |
| `--offline`     | Don't fetch; use the cache as it is                         |
| `--env <name>`  | Run the shell on that environment (`ws env list`); default: here |

## On an environment

With `--env`, the shell runs on that environment's machine, in a workspace
that lives for as long as this terminal is open. `ws` puts the terminal in
raw mode, sends keystrokes and size changes to the shell, and shows its
output; it needs a terminal on stdin (`usage` otherwise). When the shell
exits, `ws shell` exits with its status. The local terminal is restored on
every exit. If the environment goes offline, the shell ends and `ws shell`
exits 1 (`environment_offline`). The agent allows 8 terminals at once
(`terminal_limit`).

`WS_ENVIRONMENT` is set by the environment agent in the shell it starts, so
the `ws shell` that runs there records the workspace's environment. It is not
for people; an invalid value is ignored with a warning.

`--json` is refused (`usage`): there is no result to print.

## Exit codes

Before the shell starts, the workspace creation errors of `ws claude`
apply (`usage`, `spec_invalid`, `name_invalid`, `repo_not_found`, `repo_ambiguous`,
`ref_not_found`, `workspace_exists`, `fetch_failed`, `clone_failed`,
`git_failed`), and `internal` (exit 1) if the shell can't be started.
With `--env`: `environment_unknown` (exit 3), `environment_offline` and
`environment_outdated` and `platform_unsupported` (exit 1, when the environment
runs Windows: terminals aren't supported yet), and `terminal_limit` (exit 4).
Once the shell has started, `ws shell` exits with the shell's exit code, or
128+N when signal N killed it.

## Examples

```console
# Try a branch, then throw it away
$ ws shell buff-bear@feat/new-thing
$ exit

# Two repos side by side
$ ws shell --name api-bump buff-bear bobcob7/api@v2.1.0

# An empty workspace
$ ws shell
```
