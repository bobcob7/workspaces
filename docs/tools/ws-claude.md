# ws claude

Create a workspace and start a Claude Code background agent in it.

## Synopsis

```
ws claude [OPTIONS] <SPEC>... [-- <CLAUDE ARGS>...]
```

## Description

Creates a workspace from the repo specs ([Creating a
workspace](README.md#creating-a-workspace)), then runs, in the workspace:

```
claude --bg -n <workspace name> --settings '{"worktree":{"bgIsolation":"none"}}' <CLAUDE ARGS>...
```

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
- **Without a prompt**, `ws` attaches this terminal to the new session, as
  `claude attach` does. Leaving it keeps the session running.

If Claude can't start the session, `ws` removes the workspace and exits
non-zero, so nothing is left behind.

The agent shows up in `claude agents` under the workspace's name. Follow it
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
| `--name <name>` | Workspace name; also the session's name in `claude agents`  |
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
workspace, and prints the directory to trust.

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

# Start a session and work with it interactively
$ ws claude buff-bear

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
| Workspace root not trusted by Claude Code        | `claude_untrusted` | 1    |
| `claude --bg` failed for another reason; its output is shown | `claude_failed` | 1 |

## See also

[`ws rm`](ws-rm.md), [`ws restore`](ws-restore.md),
[0014](../adrs/0014-command-set.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md),
[0024](../adrs/0024-claude-code-client.md)
