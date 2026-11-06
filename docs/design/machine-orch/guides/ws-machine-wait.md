# ws machine wait

```
ws machine wait [--any] [--timeout <duration>] <workspace>...
```

Waits for children you started to finish, then prints each one's status,
outcome, and result. Run it as a **background** command and act on its
output when Claude Code tells you it exited; don't poll.

A workspace is named by its id, path, or agent session id, and must be
one of your descendants. A child already removed with `ws machine rm` can
still be named by its id while its result is kept: its status is `gone`.

Every 5 seconds it reads each child's session, its descendants' sessions,
and the results. A child is finished when its workspace is gone, or when
it is `idle` or `stopped`, none of its own children is `working` or
`starting`, and it has recorded a result or was already quiet at the
previous read. So a child waiting on its own children isn't finished.

## When to use it

- After `ws machine spawn`, to wait for one child or several in one call.
- `--any` to act on whichever child finishes first; run `wait` again for
  the rest.
- Then read each `result.report` file with your file tools, and remove
  the child with `ws machine rm` once you've used it. The result stays
  readable after the child is removed, until your tree's root is removed.

## Flags

| Flag                   | Meaning                                                      |
| ---------------------- | ------------------------------------------------------------ |
| `--any`                | Return when any one child has finished                       |
| `--timeout <duration>` | Give up after this long, such as `90m`; default `60m`        |

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{
  "children": [
    {"id": "t1-impl-d8efb24f", "session": "e8cdf5d8", "role": "implementer",
     "status": "idle", "finished": true, "outcome": "pass",
     "result": {"verdict": "PASS", "outcome": "pass",
                "report": "/…/state/orch-1a2b3c4d/machine/t1-impl-d8efb24f/report.md",
                "recorded_at": "2026-11-06T18:04:05Z"},
     "error": null}
  ],
  "warnings": []
}
```

One item per workspace named, in order. `status` is `working`, `idle`,
`starting`, `stopped`, `unknown`, `running`, or `gone`. `outcome` is
`pass` or `fail` from the child's verdict, `missing` when it finished
without recording one, or `pending` when it hasn't finished. `role` is
the role it was spawned with, or `null`; `result` is `null` until it
records one.

An item's `error` is `child_failed` when it finished with outcome `fail`
or `missing`, and `wait_timeout` when it hadn't finished at the timeout
(with `--any`, only when no child finished). Exit 0 means every child
finished and passed.

## Exit codes

| Situation                                                | Error code            | Exit |
| -------------------------------------------------------- | --------------------- | ---- |
| Run outside a workspace                                  | `outside_workspace`   | 2    |
| No workspace, or `--timeout` not positive                | `usage`               | 2    |
| Not a workspace, nor a removed child with a kept result  | `workspace_not_found` | 3    |
| Not one you started, or theirs                           | `not_descendant`      | 4    |
| An item: finished with a failing verdict, or none        | `child_failed`        | 1    |
| An item: still not finished at the timeout; run `wait` again | `wait_timeout`    | 1    |

On `child_failed` with outcome `missing`, read the child's log with
`claude logs <session>` to see why it stopped; if it hit a usage limit,
tell a person.

## Examples

```console
$ ws machine wait t1-impl-d8efb24f
$ ws machine wait t1-impl-d8efb24f t2-impl-3be0a9c1
$ ws machine wait --any --timeout 2h t1-impl-d8efb24f t2-impl-3be0a9c1
```
