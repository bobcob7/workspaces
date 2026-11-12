# ws machine children

```
ws machine children [--all]
```

Lists the workspaces your workspace started, its direct children, or with
`--all` every descendant, oldest first. Hidden ones are
included. Before listing, it removes every workspace whose session is
gone, as `ws list` does.

## When to use it

- To find your helpers and reviews, and check each one's session state,
  role, and result.
- Before you finish, to see what's left to remove with `ws machine rm`.
- To wait for a child to finish, use `ws machine wait`, not a loop over
  `children`.

## Flags

| Flag    | Meaning                                    |
| ------- | ------------------------------------------ |
| `--all` | Every descendant, not only direct children |

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{
  "workspaces": [
    {
      "id": "fix-login-d8efb24f",
      "name": "fix-login",
      "path": "/…/workspaces/fix-login-d8efb24f",
      "created_at": "2026-10-28T08:12:00Z",
      "session": {"kind": "agent", "id": "e8cdf5d8", "state": "working", "pid": 4242, "stale": false},
      "hidden": false,
      "parent": "plan-auth-1a2b3c4d",
      "repos": [{"id": "github.com/bobcob7/buff-bear", "path": "buff-bear", "ref": "main", "ref_kind": "branch"}],
      "review": null,
      "role": "implementer",
      "result": {"verdict": "PASS", "outcome": "pass",
                 "report": "/…/state/plan-auth-1a2b3c4d/machine/fix-login-d8efb24f/report.md",
                 "recorded_at": "2026-10-28T08:40:00Z"}
    }
  ],
  "hidden_count": 0,
  "warnings": []
}
```

`ws list --json -a`'s object, with only your descendants; see
`ws llm list` for each field. `hidden_count` is always 0. `role` is the
role the workspace was spawned with, or `null`. `result` is its verdict
from `ws machine result`, or `null` until it records one: `outcome` is
`pass` or `fail`, and `report` the kept report's path, or `null`.

## Exit codes

| Situation                 | Error code          | Exit |
| ------------------------- | ------------------- | ---- |
| Run outside a workspace   | `outside_workspace` | 2    |
| An argument               | `usage`             | 2    |

## Examples

```console
$ ws machine children
$ ws machine children --all
```
