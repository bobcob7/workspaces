# ws env list

```
ws env list [--json]
```

Lists the remote environments, with the state each is in and what the
running daemon has heard from it.

`STATUS` is `pending` (a code not yet used), `online`, `offline`, or
`unknown` when no daemon runs. `WS`, `PLATFORM` and `CLAUDE` come from the
environment's last hello; `WORKSPACES` counts the workspaces in its last
snapshot. Listing reads the daemon and never starts one.

## Flags

| Flag     | Meaning                  |
| -------- | ------------------------ |
| `--json` | Print the result as JSON |

## JSON output

```json
{
  "environments": [
    {
      "id": "env-1a2b3c4d",
      "name": "linux-server",
      "state": "enrolled",
      "status": "online",
      "last_seen": "2026-11-22T18:04:11Z",
      "created_at": "2026-11-20T09:12:40Z",
      "enrolled_at": "2026-11-20T09:14:02Z",
      "cert_not_after": "2027-02-18T09:14:02Z",
      "cert_ca": "current",
      "hello": {"ws_version": "v0.31.0", "os": "linux", "arch": "amd64", "claude": "present", "claude_version": "2.1.0", "cert_not_after": "2027-02-18T09:14:02Z"},
      "workspaces": 2
    }
  ],
  "warnings": []
}
```

`cert_ca` is `current` or `old` for the CA that signed the certificate, and
`null` for a pending environment. Times are RFC 3339 UTC, or `null`.

## Exit codes

| Situation                    | Error code | Exit |
| ---------------------------- | ---------- | ---- |
| Arguments or a flag error    | `usage`    | 2    |

Without a daemon the list still prints, with `unknown` statuses and a
warning.

## Examples

```console
$ ws env list
$ ws env list --json
```
