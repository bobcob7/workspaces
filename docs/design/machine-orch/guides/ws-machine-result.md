# ws machine result

```
ws machine result <VERDICT> [--file <path> | --file -]
```

Records your own verdict, and a report, for the agent that started you.
It waits on you with `ws machine wait`, which returns your verdict and the
report's path. Run it as your **last** step, after your work is pushed.

The verdict is letters, digits, and `_`, and is stored in upper case. If
you were started with a role, it must be one of the role's verdicts; your
prompt says which. Otherwise use `PASS` or `FAIL`, or `APPROVED` or
`CHANGES_REQUESTED` for a review; any other verdict counts as a failure.

The report is copied, so it's kept after your workspace is removed. Write
it outside your repo, such as `../report.md`, so it's never committed, or
pass it on stdin with `--file -`.

Running `result` again replaces your verdict and report.

## Flags

| Flag            | Meaning                                       |
| --------------- | --------------------------------------------- |
| `--file <path>` | Copy this file as the report; 1 byte to 1 MiB |
| `--file -`      | Read the report from stdin                    |

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{"id": "t1-impl-d8efb24f", "root": "orch-1a2b3c4d", "role": "implementer",
 "result": {"verdict": "PASS", "outcome": "pass",
            "report": "/…/state/orch-1a2b3c4d/machine/t1-impl-d8efb24f/report.md",
            "recorded_at": "2026-11-06T18:04:05Z"},
 "warnings": []}
```

`id` is your workspace, `root` the top of your tree, under which the
result is kept, and `role` your role or `null`. `outcome` is `pass` or
`fail`.

## Exit codes

| Situation                                                   | Error code          | Exit |
| ----------------------------------------------------------- | ------------------- | ---- |
| Run outside a workspace                                     | `outside_workspace` | 2    |
| Not exactly one verdict                                     | `usage`             | 2    |
| A malformed verdict, one your role doesn't allow, or a report that is empty, over 1 MiB, or unreadable | `result_invalid` | 2 |

## Examples

```console
$ ws machine result PASS --file ../report.md
$ ws machine result CHANGES_REQUESTED --file ../review.md
$ ws machine result FAIL
```
