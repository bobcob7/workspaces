# ws machine

Commands for agents: start, list, and remove the workspaces your own
workspace started.

## Synopsis

```
ws machine spawn [OPTIONS] [<spec>...] -- "<prompt>" [<claude args>...]
ws machine children [--all]
ws machine rm [--dry-run] [--keep-children] (<WORKSPACE>... | --all)
```

## Description

`ws machine` is for agents running in a workspace, and is the only part of
`ws` they run, apart from [`ws review`](ws-review.md) to get their work
reviewed. People and the desktop app use [`ws claude`](ws-claude.md),
[`ws list`](ws-list.md), [`ws rm`](ws-rm.md), and the other commands
([0046](../adrs/0046-ws-machine.md)). `ws llm` tells agents not to run
those, and `ws --help` lists them under "Commands for people and the
desktop app".

Every `ws machine` command acts for the **caller**: the workspace
containing the working directory. Outside a workspace, it fails with
`outside_workspace`. Each command acts only on the caller's
**descendants**, the workspaces it started and theirs
([0044](../adrs/0044-workspace-trees.md)).

Output is always JSON, as if `--json` were given; `--json` is accepted
and changes nothing. Errors are one JSON line on stderr.

## ws machine spawn

Creates a workspace and starts a Claude Code background agent in it, as
the caller's child: `ws claude --json --parent <caller>`. Everything
after `--` goes to `claude`; the first argument is the prompt, which is
required. `spawn` never attaches a terminal.

| Option          | Description                                                  |
| --------------- | ------------------------------------------------------------ |
| `--name <name>` | Name the workspace and derive its id from the name, as for `ws claude` |
| `--offline`     | Don't fetch; use the cache as-is                             |
| `--hidden`      | Hide the workspace from `ws list` and the desktop app's default views |

Prints `ws claude --json`'s object:
`{"id", "session", "path", "parent", "repos", "warnings"}`.

## ws machine children

Lists the caller's direct children, or every descendant with `--all`,
hidden ones included, oldest first. Prints `ws list --json`'s object,
`{"workspaces": [...], "hidden_count": 0, "warnings": []}`, with each
workspace's `parent`.

| Option  | Description                               |
| ------- | ----------------------------------------- |
| `--all` | Every descendant, not only direct children |

## ws machine rm

Removes workspaces as [`ws rm`](ws-rm.md) does, with their descendants,
deepest first. Each `WORKSPACE` must be a descendant of the caller: any
other, the caller itself included, is refused as its item's error,
`not_descendant`, and the rest are still removed. Prints `ws rm --json`'s
object, `{"workspaces": [{"id", "parent", "removed", "error"}], "warnings"}`,
one item per workspace removed or refused. `parent` is set on a
descendant removed with a target, and `null` on a workspace named.

| Option            | Description                                               |
| ----------------- | --------------------------------------------------------- |
| `--all`           | Remove every direct child, and with them their descendants |
| `--dry-run`       | Print what would be removed, change nothing                |
| `--keep-children` | Make the target's children children of the caller instead of removing them |

`WORKSPACE` is an id, path, or agent session id, as for other commands.

## Errors

| Situation                                       | Error code          | Exit |
| ----------------------------------------------- | ------------------- | ---- |
| Run outside a workspace                         | `outside_workspace` | 2    |
| `spawn` without a prompt; `rm` with neither workspaces nor `--all`, or both | `usage` | 2 |
| Workspace not found                             | `workspace_not_found` | 3  |
| `rm` of a workspace that isn't the caller's descendant | `not_descendant` | 4 |

`spawn` fails as [`ws claude`](ws-claude.md) does, and `rm`'s items as
[`ws rm`](ws-rm.md)'s do.

## Examples

```console
$ ws machine spawn --name fix-login buff-bear -- "Fix the login bug, push a branch, and open a PR"
{"id":"fix-login-d8efb24f","session":"e8cdf5d8","path":"/…/workspaces/fix-login-d8efb24f","parent":"plan-auth-1a2b3c4d","repos":[…],"warnings":[]}

$ ws machine children
{"workspaces":[{"id":"fix-login-d8efb24f","parent":"plan-auth-1a2b3c4d",…}],"hidden_count":0,"warnings":[]}

$ ws machine rm fix-login-d8efb24f
{"workspaces":[{"id":"fix-login-d8efb24f","parent":null,"removed":true,"error":null}],"warnings":[]}

$ ws machine rm plan-auth-1a2b3c4d
{"workspaces":[{"id":"plan-auth-1a2b3c4d","parent":null,"removed":false,"error":{"code":"not_descendant","message":"plan-auth-1a2b3c4d is not a workspace this one started"}}],"warnings":[]}
```

## See also

[`ws claude`](ws-claude.md), [`ws list`](ws-list.md), [`ws rm`](ws-rm.md),
[0044](../adrs/0044-workspace-trees.md), [0046](../adrs/0046-ws-machine.md)
