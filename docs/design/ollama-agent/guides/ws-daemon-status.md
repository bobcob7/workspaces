# ws daemon status

```
ws daemon status [--json]
```

Says whether the `ws` daemon runs for this config directory, since when,
how it was started, and which runners it watches. It never starts one.

## Flags

| Flag     | Meaning                     |
| -------- | --------------------------- |
| `--json` | Print the result as JSON    |

## JSON output

```json
{
  "running": true,
  "pid": 40211,
  "origin": "managed",
  "started_at": "2026-11-14T08:00:03Z",
  "ws_version": "v0.31.0",
  "socket": "/run/user/1000/ws/3be1f0a94c21.sock",
  "config_dir": "/data/ws",
  "runners": [
    {"workspace": "x-t1-impl-41aa0b7c", "session": "rn-5d2e8b10", "pid": 51234,
     "state": "working", "restarts": 0, "adopted": false}
  ],
  "warnings": []
}
```

When no daemon runs, `running` is `false`, `pid`, `origin`, `started_at`
and `ws_version` are `null`, `socket` is where it would listen, and
`runners` is `[]`. `origin` is `managed` or `transient`. `adopted` is
`true` for a runner an earlier daemon started. A warning says when
`secrets.env` is ignored because its mode is looser than 0600, or when the
daemon is an older `ws` than this one.

## Exit codes

| Situation                                  | Error code           | Exit |
| ------------------------------------------ | -------------------- | ---- |
| Arguments given, or a flag error           | `usage`              | 2    |
| The socket answered with an error          | `daemon_unavailable` | 1    |

## Examples

```console
$ ws daemon status
$ ws daemon status --json | jq '.runners[] | {session, state, restarts}'
```
