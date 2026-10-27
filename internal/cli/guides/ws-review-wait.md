# ws review wait

```
ws review wait [--timeout <duration>] [--json] <id>
```

Waits for a review to finish, then prints its report, exactly as `ws
review report` does. It reads the review agent's session every 5 seconds,
as `ws list` does, and is done when the agent is idle or stopped, or its
workspace is gone. For a finished review it returns at once.

Run it as a background command and act on its output when Claude Code
tells you it exited (see `ws llm review`).

## Flags

| Flag                   | Meaning                                                      |
| ---------------------- | ------------------------------------------------------------ |
| `--timeout <duration>` | Give up after this long, such as `90m`; default `[review] timeout` in `config.toml`, 60 minutes |
| `--json`               | Print the report as JSON                                     |

## JSON output

```json
{"repo": "github.com/bobcob7/buff-bear", "ref": "3f2a9c1e…", "review": {"...": "the record"},
 "agent": {"workspace": "review-buff-bear-42-3f2a9c1-d8efb24f", "status": "idle"},
 "profiles": [], "comments": [], "superseded": [], "warnings": []}
```

The same as `ws review report --json`; see `ws llm review report`.

## Exit codes

| Situation                                         | Error code         | Exit |
| ------------------------------------------------- | ------------------ | ---- |
| Not exactly one workspace, or `--timeout` not positive | `usage`       | 2    |
| No such workspace, or it isn't a review           | `review_not_found` | 3    |
| Still running after the timeout; run `wait` again | `review_timeout`   | 1    |

On `review_timeout`, `details` has `workspace` and `status`.

## Examples

```console
$ ws review wait --json review-buff-bear-42-3f2a9c1-d8efb24f
$ ws review wait --json --timeout 2h review-buff-bear-42-3f2a9c1-d8efb24f
```
