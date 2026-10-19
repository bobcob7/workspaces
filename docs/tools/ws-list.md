# ws list

List workspaces and their sessions.

## Synopsis

```
ws list [OPTIONS]
```

## Description

Reads `$CONFIG_DIR/state/*/` and prints one row per workspace, like
`docker ps`. For agent workspaces, the session's state comes from the
`pid`, `status`, and `startedAt` that `claude agents --json --all` reports
([0024](../adrs/0024-claude-code-client.md)):

| SESSION                | Meaning                                              |
| ---------------------- | ---------------------------------------------------- |
| `agent <id>: working`  | the agent has a process and is in the middle of a turn (`status` `busy`) |
| `agent <id>: idle`     | the agent has a process and is waiting: for a prompt, a permission, or review after finishing |
| `agent <id>: starting` | no process yet, started less than about 10 s ago     |
| `agent <id>: stopped`  | no process, for example after a reboot; see [`ws restore`](ws-restore.md) |
| `agent <id>: … stale` | the agent's process predates the current background service, so it has the old environment; see [`ws restart`](ws-restart.md) |
| `shell`               | the shell is running                                 |

Workspaces whose session is gone are removed before listing
([0015](../adrs/0015-workspace-lifetime-follows-session.md)), so they don't
appear. Directories under `workspaces/` without state are shown last with
id `unknown`. `ws` never deletes them ([0012](../adrs/0012-workspace-layout.md)).

## Options

| Option          | Description                                    |
| --------------- | ---------------------------------------------- |
| `-q`            | Print only workspace IDs (like `docker ps -q`) |
| `--repo <SPEC>` | Only workspaces containing this repo           |
| `--json`        | Print `{"workspaces": [...], "warnings"}`       |

## Examples

```console
$ ws list
ID                   CREATED   SESSION                   REPOS
fix-login            2h ago    agent e8cdf5d8: stopped   buff-bear@main
ws-20261018-k3f9qa   20m ago   agent 5d8d652c: working   buff-bear@feat/new-thing, api@v2.1.0
api-bump             5m ago    shell                     buff-bear@main, api@v2.1.0

$ ws list --json
{
  "workspaces": [
    {
      "id": "fix-login",
      "path": "/Users/me/Library/Application Support/workspaces/workspaces/fix-login",
      "created_at": "2026-10-18T08:12:00Z",
      "session": { "kind": "agent", "id": "e8cdf5d8", "state": "stopped", "pid": null, "stale": false },
      "repos": [
        { "id": "github.com/bobcob7/buff-bear", "path": "buff-bear",
          "ref": "main", "ref_kind": "branch" }
      ]
    }
  ],
  "warnings": []
}
```

`session.state` is `working`, `idle`, `starting`, `stopped`, `unknown`, or
`running` for a shell. It is `ws`'s reading, not Claude Code's own `state`
field, which stays `working` on an idle session. `stale` is `null` for
shells.

If `claude agents` fails, agent sessions show as `agent <id>: unknown`,
nothing is removed, and the failure is reported in `warnings`.

## See also

[`ws rm`](ws-rm.md), [`ws restore`](ws-restore.md),
[`ws restart`](ws-restart.md), [0012](../adrs/0012-workspace-layout.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md),
[0024](../adrs/0024-claude-code-client.md)
