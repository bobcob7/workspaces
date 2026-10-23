# ws claude

Create a workspace and start a Claude Code background agent in it.

## Synopsis

```
ws claude [OPTIONS] [<SPEC>...] [-- <CLAUDE ARGS>...]
```

## Description

Creates a workspace from the repo specs ([Creating a
workspace](README.md#creating-a-workspace)), or an empty one when there are
none, then runs, in the workspace, or in its repo's directory when it has
exactly one repo:

```
claude --bg -n <workspace id> --settings '{"worktree":{"bgIsolation":"none"}}' <CLAUDE ARGS>...
```

With exactly one repo, the agent starts in that repo, so Claude Code loads
the repo's own `CLAUDE.md` and `.claude/` settings as a project. Claude Code
keys trust on a repository's root and doesn't extend a parent's trust into
a clone, so first `ws` marks the repo's directory trusted in Claude Code's
global config (`~/.claude.json`, or `$CLAUDE_CONFIG_DIR/.claude.json`), as
Claude Code's docs describe doing by hand. It does so only when the
workspace root is already trusted there, under Claude Code's own lock on
that file, and changes nothing else in it. Trusting the repo lets its
`.claude/settings.json` allow rules apply, so start agents only on repos you
trust. `ws` records the trust it adds in `state/<id>/trust`, and takes it
back when the workspace is removed, however that happens; a repo that was
already trusted is left alone. If the root isn't trusted, the agent starts in the workspace and
fails with `claude_untrusted` as before; if the file can't be updated,
`ws` warns and starts the agent in the workspace
([0028](../adrs/0028-single-repo-session-directory.md), proposed).

Everything after `--` goes to `claude` unchanged: the prompt, and options
such as `--model` or `--permission-mode`. The `bgIsolation` setting makes
Claude edit the workspace's repos directly instead of trying to create its
own worktree ([0015](../adrs/0015-workspace-lifetime-follows-session.md)).
`--settings` can't be among them: Claude Code keeps only the last
`--settings`, so yours would silently replace `ws`'s. Put your settings in a
settings file, such as `~/.claude/settings.json`, instead
([0024](../adrs/0024-claude-code-client.md)).

- **With a prompt**, `ws` waits only until Claude has accepted the session,
  then prints the workspace id, the Claude session id, and the workspace
  path, and exits. The agent keeps running in the background.
- **Without a prompt**, when Claude reports the session as idle and stdin
  and stdout are a terminal, `ws` prints one line on stderr with the
  workspace id and path (none with `--quiet`), then becomes
  `claude attach <id>` in this terminal; stdout stays empty. Leaving it
  (Ctrl+Z) keeps the session running, and `ws` exits with
  `claude attach`'s status. Without a terminal, with `--json`, or after
  Ctrl-C or a hangup during the start, `ws` prints the result as it does
  with a prompt ([0025](../adrs/0025-ws-claude-session-hand-off.md),
  proposed).

If Claude can't start the session, `ws` removes the workspace and exits
non-zero, so nothing is left behind. Once `claude --bg` has started, Ctrl-C
doesn't stop it, and closing the terminal (SIGHUP) doesn't stop `ws` until
the session is recorded: `ws` waits for its answer, so it never loses track
of a session Claude accepted, then prints the result instead of attaching.
Ctrl-\\ still stops `ws` if `claude --bg` hangs; a session left that way is
removed with its workspace once the workspace counts as abandoned
([0025](../adrs/0025-ws-claude-session-hand-off.md)).

The agent shows up in `claude agents` under the workspace id (`--name "Fix
Login"` lists as `fix-login`). Follow it
with `claude logs <id>`, talk to it with `claude attach <id>`, and stop it
with `claude stop <id>`. The workspace stays until the session is deleted
(`claude rm <id>`, agent view, or [`ws rm`](ws-rm.md)), even after the agent
has finished, so the conversation can be reviewed next to the code.

Environment variables set when running `ws claude` don't reach the agent:
Claude Code runs it under its own background service, which has the
environment of whatever started it. To give agents a new environment, run
[`ws restart`](ws-restart.md). The agent's working directory is the
workspace.

## Options

| Option          | Description                                                 |
| --------------- | ----------------------------------------------------------- |
| `--name <name>` | Workspace name; the id derived from it also names the session |
| `--offline`     | Don't fetch; use the cache as-is                            |
| `--json`        | Print `{"id", "session", "path", "repos", "warnings"}`      |

## Trust

Claude Code refuses to start in a directory you haven't trusted. Trusting
the workspace root once covers every workspace below it:

```console
# $CONFIG_DIR/workspaces; this is the macOS default
$ cd "$HOME/Library/Application Support/workspaces/workspaces" && claude
# accept the trust prompt, then exit
```

Until then, `ws claude` fails with `claude_untrusted` (exit 1), removes the
workspace, and prints the directory to trust, which is also in the error's
`details.directory` with `--json`.

## Examples

```console
# Hand a task to an agent
$ ws claude --name fix-login buff-bear -- "Find why login fails on empty passwords and fix it"
fix-login  e8cdf5d8  /Users/me/Library/Application Support/workspaces/workspaces/fix-login

# Several repos, a model choice, JSON for a calling agent
$ ws claude --json buff-bear@feat/new-thing bobcob7/api@v2.1.0 -- --model opus "Update the client for the new API"
{
  "id": "ws-20261018-k3f9qa",
  "session": "5d8d652c",
  "path": "/Users/me/Library/Application Support/workspaces/workspaces/ws-20261018-k3f9qa",
  "repos": [
    { "id": "github.com/bobcob7/buff-bear", "path": "buff-bear",
      "ref": "feat/new-thing", "ref_kind": "branch", "commit": "3f2a9c1e4b..." },
    { "id": "github.com/bobcob7/api", "path": "api",
      "ref": "v2.1.0", "ref_kind": "tag", "commit": "9b1d22a0c7..." }
  ],
  "warnings": []
}

# One repo: the agent starts in .../fix-login/buff-bear
$ ws claude --name fix-login buff-bear -- "Fix the login bug"
fix-login  e8cdf5d8  /Users/me/Library/Application Support/workspaces/workspaces/fix-login

# An agent in an empty workspace, for work that needs no repo
$ ws claude --name notes -- "Draft release notes from the issues I paste in"
notes  7c41b0e2  /Users/me/Library/Application Support/workspaces/workspaces/notes

# Start a session and work with it interactively; Ctrl+Z leaves it running
$ ws claude --name try-it buff-bear
ws: workspace try-it at /Users/me/Library/Application Support/workspaces/workspaces/try-it; attaching to Claude Code session 2af7e501

# Review and remove a finished agent
$ claude attach e8cdf5d8
$ ws rm fix-login
```

## Errors

See also the [workspace creation errors](README.md#creating-a-workspace).

| Situation                                        | Error code         | Exit |
| ------------------------------------------------ | ------------------ | ---- |
| `--settings` among the arguments after `--`      | `usage`            | 2    |
| `claude` not on `PATH`                           | `claude_missing`   | 1    |
| `claude` older than 2.1.302                      | `claude_failed`    | 1    |
| Workspace root not trusted by Claude Code        | `claude_untrusted` | 1    |
| `claude --bg` failed for another reason; its output is shown | `claude_failed` | 1 |

## See also

[`ws rm`](ws-rm.md), [`ws restore`](ws-restore.md),
[0014](../adrs/0014-command-set.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md),
[0024](../adrs/0024-claude-code-client.md),
[0025](../adrs/0025-ws-claude-session-hand-off.md)
