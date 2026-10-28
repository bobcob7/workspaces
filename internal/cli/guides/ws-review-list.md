# ws review list

```
ws review list [--repo <repo>] [--pr <n>] [--json]
```

Lists reviews, newest first, including those whose agent and workspace
were removed after every reviewer reported. Each review's record is kept
in the review store when it starts, next to its findings, so it outlives
the workspace.

## When to use it

- To find a review of a pull request or commit, finished or not, and its
  id for `ws review report`.
- Not to wait for one you started: run its `wait_command`, which works
  after the review is removed too.

## Flags

| Flag            | Meaning                                                  |
| --------------- | -------------------------------------------------------- |
| `--repo <repo>` | Only reviews of this repo id, `host/owner/name`          |
| `--pr <n>`      | Only reviews of this pull request                        |
| `--json`        | Print the result as JSON                                 |

## JSON output

```json
{"reviews": [
  {"id": "review-buff-bear-51-7d20c1e-1f0c33d8", "review": {"repo": "github.com/bobcob7/buff-bear", "ref": "7d20c1e…", "pr": {"number": 51, "…": "…"}, "…": "the record"},
   "agent": null},
  {"id": "review-buff-bear-41-9c04e2a-1f0c33d8", "review": {"…": "the record"},
   "agent": {"workspace": "review-buff-bear-41-9c04e2a-1f0c33d8", "status": "working"}}
], "warnings": []}
```

- `review`: the review's record, as `ws review start --json` prints it.
- `agent`: the review agent's workspace and status while it exists;
  `null` once the review was removed. Its findings are still in
  `ws review report <id>`.

## Exit codes

| Situation                          | Error code | Exit |
| ---------------------------------- | ---------- | ---- |
| An argument, or `--repo` isn't an id | `usage`  | 2    |

## Examples

```console
$ ws review list --json --repo github.com/bobcob7/buff-bear --pr 51
$ ws review report --json review-buff-bear-51-7d20c1e-1f0c33d8
```
