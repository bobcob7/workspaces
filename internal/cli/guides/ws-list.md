# ws list

```
ws list [-a] [--repo <spec>] [--parent <id>] [--json | -q]
```

Lists workspaces and their sessions, oldest first. Before listing, it
removes every workspace whose session is gone. Hidden workspaces, such as
reviews from `ws review start` and agents started with `ws claude
--hidden`, are left out unless given `-a`.

`ws list` is for people and the desktop app. Agents use
`ws machine children` to list their helpers; see `ws llm machine children`.

## When to use it

- To find workspaces, check on agents, or find the workspace a session
  id or repo belongs to.
- To make `ws` notice now that a session was deleted with `claude rm`:
  `ws list` always runs the removal check, other commands at most once a
  minute.

## Flags

| Flag            | Meaning                                                     |
| --------------- | ----------------------------------------------------------- |
| `-a`, `--all`   | Include hidden workspaces                                   |
| `--parent <id>` | Only the workspaces that workspace started, its direct children |
| `--repo <spec>` | Only workspaces holding this repo, matched by the parts the spec gives: `api` matches any owner and host, `o/api` any host, `@ref` the ref checked out. No cache lookup |
| `--env <name>`  | Only these environments, repeatable: `local` or a name from `ws env list`. Default: every environment, `local` first, then enrolled ones by name. A remote environment lists its last snapshot. `-q` defaults to `local`; `--parent` without `--env` finds the environment its parent is in; `--parent` takes one `--env` |
| `-q`, `--ids`   | Print only workspace ids, one per line; not with `--json`   |
| `--json`        | Print the result as JSON                                    |

A remote environment's workspaces come from the snapshot its agent last
sent, so they can be out of date; see `stale` below.

## JSON output

```json
{
  "workspaces": [
    {
      "id": "fix-login-d8efb24f",
      "name": "Fix login",
      "path": "/…/workspaces/fix-login-d8efb24f",
      "created_at": "2026-10-18T08:12:00Z",
      "session": {"kind": "agent", "id": "e8cdf5d8", "state": "idle", "pid": 4242, "stale": false, "runner": "claude", "model": "claude-sonnet-5-5", "ws_runner": null},
      "hidden": false,
      "parent": null,
      "repos": [{"id": "github.com/bobcob7/buff-bear", "path": "buff-bear", "ref": "main", "ref_kind": "branch"}],
      "review": null,
      "loop_run": null,
      "agent_state": null,
      "scope_parent": null,
      "scope": null,
      "environment": "local",
      "environment_status": "online",
      "stale": false
    }
  ],
  "hidden_count": 2,
  "warnings": []
}
```

- `environment`: `local` for a workspace on this machine, or the name of
  the environment it runs in (`ws env list`).
- `environment_status`: `online` or `offline`, whether the environment has
  reported to the running daemon. Always `online` for local.
- `stale`: `true` when a remote entry can't be trusted as current: its
  environment is offline, or its snapshot is more than 90 seconds old.
  Always `false` for local.
- `agent_state`: the state of the remote agent that runs the entry's
  session: `working`, `blocked`, `done` or `stopped`. `null` for local
  entries, shells, and remote entries with no matching agent.
- `scope_parent`: for a child another environment started, its parent:
  `environment` (`local` or the parent's environment name) and `workspace`.
  `null` otherwise.
- `scope`: the environments the workspace may start children on, when wider
  than its own environment: each `name`, `id`, `state` (`online`, `offline`
  or `removed`) and `current`. `null` otherwise.
- `name`: the display name, from `--name` or `ws rename`; `null` for a
  workspace created without one.
- `session.kind`: `agent` or `shell`. `session.id`: the Claude Code session
  id, `null` for a shell.
- `session.state` for an agent: `working` (in the middle of a turn),
  `idle` (has a process and is waiting: for a prompt, a permission, or
  review after finishing), `starting` (no process yet, started under
  10 s ago), `stopped` (no process, for example after a reboot; see
  `ws restore`), or `unknown` (Claude Code doesn't list it, or couldn't be
  asked). For a shell: `running`, or `unknown` when its lock can't be
  checked.
- `session.pid`: the process id, or `null`.
- `session.stale`: `true` when the agent's process is older than Claude
  Code's background service, so it runs with an old environment; `null`
  when that can't be told, and for shells.
- `session.runner`: `claude` or `ws` for an agent, `null` for a shell.
- `session.model`: the agent's model (`claude-sonnet-5-5`, `qwen/qwen3.5-9b`),
  `null` for a shell or an agent that hasn't answered yet.
- `session.ws_runner`: details for a `ws` child (see `ws llm machine
  wait`), otherwise `null`.
- `hidden`: whether the workspace is hidden; see `ws llm hide`. Only
  `true` with `-a`.
- `parent`: the workspace this one was started from, and is removed
  with; `null` for a top-level one. The table lists a child under its
  parent, its id indented with `└ `.
- `hidden_count`: how many hidden workspaces were left out; 0 with `-a`.
- `review`: a review workspace's record, as `ws review start --json`
  prints it, or `null` for any other workspace.
- `loop_run`: the loop a workspace runs, and what its run asks of a person,
  or `null` for any other workspace. `attention` is `blocked` (the run
  recorded `BLOCKED`), `needs_attention` (it went idle without a verdict),
  or `null`. For example `{"loop": "deps-bump", "attention": "blocked"}`.
  A hidden loop run with attention is listed without `-a` and is not
  counted in `hidden_count`; the table shows it as `(blocked)` or
  `(needs attention)` in place of `(hidden)`.
- `progress`: the workspace's own progress (`name`, `total`, `done`, `note`,
  `updated_at`), or `null` when it reported none. `rollup`: `total`, `done`,
  and `fraction` for its steps and those of everything it started, or `null`
  when no step is planned. A started workspace with a result but no record
  counts as one step, done only if it passed.
- `session` is `null` while `ws claude` or `ws shell` is still handing the
  workspace to its session.
- A directory under the workspace root that isn't a workspace is listed
  last with `id`, `created_at`, and `session` `null`. `ws` never deletes
  it. `--repo` leaves such directories out.

The table has an `ENV` column after `ID` only when a listed workspace runs
in a remote environment; a stale remote entry shows as `name (offline)`.

If `claude agents` fails, agent sessions are `unknown`, nothing is removed
but shell workspaces whose shell has exited, and a warning says why.

## Exit codes

| Situation                       | Error code     | Exit |
| ------------------------------- | -------------- | ---- |
| An argument, or `-q` with `--json` | `usage`     | 2    |
| `--parent` with more than one `--env` | `usage` | 2    |
| `--repo` isn't a valid spec     | `spec_invalid` | 2    |
| `--env` names no environment    | `environment_unknown` | 3 |

## Examples

```console
$ ws list --json
$ ws list --json --repo buff-bear@main
$ ws list -q
$ ws list --json -a   # including hidden workspaces
$ ws list --env linux-server   # a remote environment's workspaces
```
