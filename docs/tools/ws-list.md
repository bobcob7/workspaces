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
| `agent <id>: … stale` | the agent's process started before the current background service's process, so it has the old environment; see [`ws restart`](ws-restart.md) |
| `shell`               | the shell is running                                 |

Workspaces whose session is gone are removed before listing
([0015](../adrs/0015-workspace-lifetime-follows-session.md)), so they don't
appear; one whose removal fails is still listed, since it is still on
disk, and the failure is reported in `warnings`. `ws list` runs that check every time, not at most once a minute as
other commands do ([0026](../adrs/0026-removal-pass-timing.md), proposed).
A workspace whose session isn't recorded yet, because `ws claude` or
`ws shell` is still handing it over, shows `-` as its session (`null` in
JSON); one left that way for over a minute with nothing holding its lock is
abandoned, and is removed with any Claude Code session started in exactly
that directory, or in its one repo's directory ([0023](../adrs/0023-shell-session-keeps-creation-lock.md),
[0025](../adrs/0025-ws-claude-session-hand-off.md)).
Directories under `workspaces/` without state are shown last with
id `unknown` (in JSON, `id`, `name`, `created_at`, and `session` are `null`).
`ws` never deletes them ([0012](../adrs/0012-workspace-layout.md)).

## Options

| Option          | Description                                    |
| --------------- | ---------------------------------------------- |
| `-q`, `--ids`   | Print only workspace IDs (like `docker ps -q`); not with `--json` |
| `--repo <SPEC>` | Only workspaces containing this repo, matched by the parts the spec gives (`api` matches any owner and host, `o/api` any host) and its ref, if any, against the ref checked out; no cache lookup |
| `--json`        | Print `{"workspaces": [...], "warnings"}`       |

## Examples

```console
$ ws list
ID                   NAME        CREATED   SESSION                   REPOS
fix-login-d8efb24f   Fix login   2h ago    agent e8cdf5d8: stopped   buff-bear@main
ws-20261018-k3f9qa   -           20m ago   agent 5d8d652c: working   buff-bear@feat/new-thing, api@v2.1.0
api-bump-5c01e7a2    api-bump    5m ago    shell                     buff-bear@main, api@v2.1.0

$ ws list --json
{
  "workspaces": [
    {
      "id": "fix-login-d8efb24f",
      "name": "Fix login",
      "path": "/Users/me/Library/Application Support/workspaces/workspaces/fix-login-d8efb24f",
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

`name` is the display name, from `--name` or [`ws rename`](ws-rename.md),
or `null` (`-` in the table) for a workspace created without one
([0036](../adrs/0036-workspace-id-suffix-and-rename.md)).

`session.state` is `working`, `idle`, `starting`, `stopped`, `unknown`, or
`running` for a shell. It is `ws`'s reading, not Claude Code's own `state`
field, which stays `working` on an idle session. `stale` is `true`
when the session's process started before the background service's
process, both start times read from the operating system; the service's
pid comes from `claude daemon status`. It is `false` for an agent without
a process, and `null` for shells and for agents when no service is
running, `claude daemon status` can't be read, or either start time can't
be read.

If `claude agents` fails, agent sessions show as `agent <id>: unknown`, no
agent or abandoned workspace is removed, and the failure is reported in
`warnings`. Shell workspaces don't depend on Claude Code and are still
removed once their shell has exited.

## See also

[`ws rm`](ws-rm.md), [`ws restore`](ws-restore.md),
[`ws restart`](ws-restart.md), [0012](../adrs/0012-workspace-layout.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md),
[0024](../adrs/0024-claude-code-client.md),
[0026](../adrs/0026-removal-pass-timing.md)
