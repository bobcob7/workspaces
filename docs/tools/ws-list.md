# ws list

List workspaces and their sessions.

## Synopsis

```
ws list [OPTIONS]
```

## Description

Reads `$CONFIG_DIR/state/*/` and prints one row per workspace, like
`docker ps`. Hidden workspaces ([`ws hide`](ws-hide.md) and
`ws claude --hidden`) are left out unless `-a` is given, and a line on
stderr says how many were (none with `--quiet`); with `-a`, the NAME
column marks them `(hidden)` ([0043](../adrs/0043-hidden-workspaces.md)). For agent workspaces, the session's state comes from the
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

A hidden loop run ([`ws loop`](ws-loop.md)) that is waiting for a person is
listed even without `-a`, and isn't counted in `hidden_count`. It is waiting
when its `loop_run.attention` is `blocked` or `needs_attention`
([0057](../adrs/0057-loop-visibility-and-blocked-runs.md)). Its NAME column
reads `<name> (blocked)` or `<name> (needs attention)` in place of
`<name> (hidden)`.

Without `--env`, `ws list` lists this machine's workspaces only, as it always
has. `--env <name>` lists the workspaces of those environments instead
(`local` is a name, and `ws env list` shows the others). A remote
environment's workspaces come from its last snapshot, and are read-only:
they can be listed, not acted on from here. Until the desktop app shows
environments (PR 4), remote workspaces appear only with `--env`, because the
desktop runs `ws list --all` and acts on ids with local commands, and a
remote id equal to a local one would make it act on the wrong workspace
([0072](../adrs/0072-remote-environments.md), deviation 1).

Each entry names its environment: `environment` is `local` or the
environment's name. `environment_status` is `online` or `offline` for a
remote one, and `online` for local. An environment is `online` when the
daemon reports its agent connected; with no daemon running, or no answer
within 2 s, every remote environment is `offline`, without starting one.
`stale` is `true` for a remote entry whose environment isn't `online`, or
whose snapshot is more than 90 s old (three refresh periods), and `false`
otherwise. The table adds an `ENV` column after `ID` when some listed
workspace runs on an environment other than `local`.

## Options

| Option          | Description                                    |
| --------------- | ---------------------------------------------- |
| `-a`, `--all`   | Include hidden workspaces                      |
| `-q`, `--ids`   | Print only workspace IDs (like `docker ps -q`); not with `--json` |
| `--parent <WORKSPACE>` | Only the workspaces it started, its direct children ([0044](../adrs/0044-workspace-trees.md)); with `--env`, one environment only |
| `--repo <SPEC>` | Only workspaces containing this repo, matched by the parts the spec gives (`api` matches any owner and host, `o/api` any host) and its ref, if any, against the ref checked out; no cache lookup |
| `--env <NAME>`  | Only the workspaces of this environment: `local` or a name from `ws env list`. Repeatable; default `local` |
| `--json`        | Print `{"workspaces": [...], "hidden_count", "warnings"}`, with `environment`, `environment_status` and `stale` on each entry |

## Examples

```console
$ ws list
ID                   NAME        CREATED   SESSION                   REPOS
fix-login-d8efb24f   Fix login   2h ago    agent e8cdf5d8: stopped   buff-bear@main
ws-20261018-k3f9qa   -           20m ago   agent 5d8d652c: working   buff-bear@feat/new-thing, api@v2.1.0
api-bump-5c01e7a2    api-bump    5m ago    shell                     buff-bear@main, api@v2.1.0
2 hidden workspaces not shown; ws list -a shows them

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
      ],
      "hidden": false,
      "parent": null,
      "review": null,
      "loop_run": null,
      "environment": "local",
      "environment_status": "online",
      "stale": false
    }
  ],
  "hidden_count": 2,
  "warnings": []
}
```

`name` is the display name, from `--name` or [`ws rename`](ws-rename.md),
or `null` (`-` in the table) for a workspace created without one
([0036](../adrs/0036-workspace-id-suffix-and-rename.md)).

`parent` is the workspace this one was started from and is removed with,
or `null` ([0044](../adrs/0044-workspace-trees.md)). The table lists each
workspace right after its parent, its id indented with `└ `, one level
more per generation; one whose parent isn't listed is top-level.

`hidden` is whether the workspace is hidden; `false` for directories
that aren't workspaces. `hidden_count` is how many hidden workspaces were
left out: 0 with `-a`, and counting only those `--repo` matches.

`review` is a review workspace's record, `state/<id>/review.toml`, as
[`ws review start --json`](ws-review.md) prints it, or `null` for any other
workspace and for a record that can't be read, which is a warning
([0039](../adrs/0039-ws-review-command.md)). The desktop app matches
reviews to pull requests by its `pr.number` and `ref`.

`loop_run` is `{"loop": <name>, "attention": …}` for a loop run's
workspace, else `null`, including for an unreadable run mark, which is a
warning. `attention` is `"blocked"` or `"needs_attention"` while the run is
waiting for a person, else `null`. It is computed on each call, from the
session's state and the run's recorded verdict, so it changes as soon as the
agent records a verdict or carries on. An unreadable machine record counts
as no verdict.

`session.state` is `working`, `idle`, `starting`, `stopped`, `unknown`, or
`running` for a shell. It is `ws`'s reading, not Claude Code's own `state`
field, which stays `working` on an idle session. `session.runner` is `claude`
or `ws` for an agent (`ws` for a runner session, ADR 0063) and `null` for a
shell. `session.model` is the model the agent uses, or `null` until a Claude
agent has answered. `session.ws_runner` is `null` unless `runner` is `ws`; then
it is `{"model_alias", "endpoint", "step", "max_steps", "restarts", "usage",
"mcp", "exit", "endpoint_changes"}`, where `endpoint` and `model` are the ones
the run uses now. `stale` is `true`
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
[`ws restart`](ws-restart.md), [`ws hide`](ws-hide.md), [0012](../adrs/0012-workspace-layout.md),
[0015](../adrs/0015-workspace-lifetime-follows-session.md),
[0024](../adrs/0024-claude-code-client.md),
[0026](../adrs/0026-removal-pass-timing.md)
