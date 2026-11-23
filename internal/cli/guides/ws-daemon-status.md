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
  "environments": {
    "listening": true,
    "listen": "0.0.0.0:8883",
    "enroll_listen": "0.0.0.0:8884",
    "environments": [
      {"id": "env-1a2b3c4d", "name": "linux-server", "state": "enrolled",
       "status": "online", "last_seen": "2026-11-22T14:02:00Z",
       "hello": {"ws_version": "v0.31.0", "os": "linux", "arch": "amd64",
                 "claude": "present", "claude_version": "2.4.1",
                 "cert_not_after": "2027-02-20T14:05:11Z"}}
    ]
  },
  "warnings": []
}
```

When no daemon runs, `running` is `false`, `pid`, `origin`, `started_at`,
`ws_version` and `environments` are `null`, `socket` is where it would
listen, and `runners` is `[]`.

`environments` says whether the daemon listens for remote environments
(`listening`, with the `listen` and `enroll_listen` addresses), and each
environment with its `state` (`pending` or `enrolled`), its `status`
(`pending`, `online` or `offline`), when it was last heard from, and its
last hello (`null` until one). With no `[environments]` `public_name`,
`listening` is `false` and `warnings` is `[]`. With a `public_name` but no
broker, `listening` is `false` and `warnings` says why.

`origin` is `managed`, `transient` or `app` (started by the desktop app).
`adopted` is `true` for a runner an earlier daemon started. A warning says when
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
