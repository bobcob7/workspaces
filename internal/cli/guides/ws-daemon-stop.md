# ws daemon stop

```
ws daemon stop [--keep-runners] [--json]
```

Stops the `ws` daemon. Without `--keep-runners`, it first interrupts every
runner it watches: their workspaces stay, they read `stopped`, and the
next daemon resumes them where they were. With `--keep-runners`, they keep
running, and the next daemon adopts them.

The next command that needs a daemon (`ws machine spawn --runner ws`,
`ws restore`) starts a new one from its own shell, so stopping with
`--keep-runners` is how to give the daemon a new environment. Nothing is
stopped when no daemon runs.

## Flags

| Flag             | Meaning                                              |
| ---------------- | ---------------------------------------------------- |
| `--keep-runners` | Leave runners running for the next daemon to adopt   |
| `--json`         | Print the result as JSON                             |

## JSON output

```json
{
  "stopped": true,
  "pid": 40211,
  "runners_stopped": [],
  "runners_kept": ["rn-5d2e8b10"],
  "warnings": []
}
```

`stopped` is `false`, and `pid` `null`, when no daemon was running.
`runners_stopped` lists the runner sessions it interrupted,
`runners_kept` those left running.

## Exit codes

| Situation                                  | Error code           | Exit |
| ------------------------------------------ | -------------------- | ---- |
| Arguments given, or a flag error           | `usage`              | 2    |
| The daemon didn't stop within 30 s         | `daemon_unavailable` | 1    |

## Examples

```console
$ ws daemon stop
$ ws daemon stop --keep-runners --json
```
