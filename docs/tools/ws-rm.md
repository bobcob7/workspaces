# ws rm

Remove workspaces and their sessions now.

## Synopsis

```
ws rm [OPTIONS] <WORKSPACE>...
```

## Description

For an **agent workspace**, first checks that the recorded session is the
workspace's: if `claude agents --json --all` lists it, its working
directory must be the workspace's directory, or, for a workspace with one
repo, that repo's directory, with symlinks resolved
([0028](../adrs/0028-single-repo-session-directory.md)).
If it's listed in another directory, `ws rm` touches neither the session
nor the workspace and fails that item with `session_mismatch`. Otherwise
it runs `claude stop <id>` and `claude rm <id>`, then deletes the workspace
and its state; a session Claude Code no longer has counts as already
gone. The conversation transcript stays available through
`claude --resume`. `--dry-run` makes the same check.

For a **shell workspace**, refuses while the shell, or anything started from
it, is running. Exit the shell instead; that removes it.

For a workspace whose session **isn't recorded**, refuses while `ws claude`
or `ws shell` is still handing it over (its lock is held); otherwise runs
`claude rm` on each Claude Code session whose working directory is exactly
the workspace, or its one repo, then deletes it
([0025](../adrs/0025-ws-claude-session-hand-off.md)).

If `ws claude` trusted the workspace's repo in Claude Code's config, that
trust is removed with the workspace; if it can't be, `ws rm` warns and
removes the workspace anyway
([0028](../adrs/0028-single-repo-session-directory.md)).

Git state is ignored: uncommitted changes, unpushed commits, and stashes go
with the workspace ([0015](../adrs/0015-workspace-lifetime-follows-session.md)).
Only directories recorded in a valid `$CONFIG_DIR/state/<id>/workspace.toml`,
directly under the workspace root, are ever deleted
([0012](../adrs/0012-workspace-layout.md)).

You don't need `ws rm` after `claude rm` or deleting the session in agent
view: the workspace is removed at the start of a later `ws` command (checked
at most once a minute). If that check, at the start of `ws rm` itself,
removes a workspace named on the command line, it is reported as removed
([0026](../adrs/0026-removal-pass-timing.md), proposed).

## Options

| Option      | Description                                   |
| ----------- | --------------------------------------------- |
| `--dry-run` | Print what would be removed; `ws rm` itself changes nothing |
| `--json`    | Print `{"workspaces": [{"id", "removed", "error"}], "warnings"}` |

## Examples

```console
$ ws rm fix-login-d8efb24f
removed  fix-login-d8efb24f  e8cdf5d8

$ ws rm e8cdf5d8          # by Claude session id

$ ws rm --dry-run api-bump-5c01e7a2
would remove  api-bump-5c01e7a2
```

With `--dry-run`, `removed` is `false` for a workspace that would be
removed, and `error` says why one wouldn't. `--dry-run` doesn't stop the
removal pass that every command runs first: a named workspace that pass
removes is reported as removed, with `removed` `true`.

## Errors

| Situation                                     | Error code            | Exit |
| --------------------------------------------- | --------------------- | ---- |
| Workspace not found                           | `workspace_not_found` | 3    |
| Shell workspace with a running shell          | `shell_running`       | 4    |
| Workspace still being handed to its session   | `workspace_busy`      | 4    |
| Its session is listed in another directory; nothing is touched | `session_mismatch` | 4 |
| `claude rm` refused or failed; the workspace is kept | `claude_failed` | 1    |
| Its recorded directory isn't its own under the workspace root; nothing is deleted ([0012](../adrs/0012-workspace-layout.md)) | `internal` | 1 |

With several workspaces, the others are still removed, each result carries
its own `error`, and the exit code is that of the first failure.

## See also

[`ws list`](ws-list.md),
[0012](../adrs/0012-workspace-layout.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md),
[0026](../adrs/0026-removal-pass-timing.md)
