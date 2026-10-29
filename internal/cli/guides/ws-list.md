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
| `-q`, `--ids`   | Print only workspace ids, one per line; not with `--json`   |
| `--json`        | Print the result as JSON                                    |

## JSON output

```json
{
  "workspaces": [
    {
      "id": "fix-login-d8efb24f",
      "name": "Fix login",
      "path": "/…/workspaces/fix-login-d8efb24f",
      "created_at": "2026-10-18T08:12:00Z",
      "session": {"kind": "agent", "id": "e8cdf5d8", "state": "idle", "pid": 4242, "stale": false},
      "hidden": false,
      "parent": null,
      "repos": [{"id": "github.com/bobcob7/buff-bear", "path": "buff-bear", "ref": "main", "ref_kind": "branch"}],
      "review": null
    }
  ],
  "hidden_count": 2,
  "warnings": []
}
```

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
- `hidden`: whether the workspace is hidden; see `ws llm hide`. Only
  `true` with `-a`.
- `parent`: the workspace this one was started from, and is removed
  with; `null` for a top-level one. The table lists a child under its
  parent, its id indented with `└ `.
- `hidden_count`: how many hidden workspaces were left out; 0 with `-a`.
- `review`: a review workspace's record, as `ws review start --json`
  prints it, or `null` for any other workspace.
- `session` is `null` while `ws claude` or `ws shell` is still handing the
  workspace to its session.
- A directory under the workspace root that isn't a workspace is listed
  last with `id`, `created_at`, and `session` `null`. `ws` never deletes
  it. `--repo` leaves such directories out.

If `claude agents` fails, agent sessions are `unknown`, nothing is removed
but shell workspaces whose shell has exited, and a warning says why.

## Exit codes

| Situation                       | Error code     | Exit |
| ------------------------------- | -------------- | ---- |
| An argument, or `-q` with `--json` | `usage`     | 2    |
| `--repo` isn't a valid spec     | `spec_invalid` | 2    |

## Examples

```console
$ ws list --json
$ ws list --json --repo buff-bear@main
$ ws list -q
$ ws list --json -a   # including hidden workspaces, such as reviews
```
