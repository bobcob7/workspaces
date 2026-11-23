# ws env rm

```
ws env rm <NAME> [--json]
```

Removes environment `<NAME>`: revokes its certificate, if it has one, and
deletes its record and its snapshot. The machine it runs on is offline from
then on, and can't reconnect until it's added again with `ws env add`.

`revoked_serials` lists the serials that can no longer be used: the current
certificate first, then up to 5 earlier ones that renewal superseded. Deleting a pending environment just cancels its code.

Its files on the remote machine aren't touched. Run `ws env agent unenroll`
there to delete them.

## Flags

| Flag     | Meaning                  |
| -------- | ------------------------ |
| `--json` | Print the result as JSON |

## JSON output

```json
{
  "id": "env-1a2b3c4d",
  "name": "linux-server",
  "revoked_serials": ["5e0c…", "4b7a…"],
  "warnings": []
}
```

## Exit codes

| Situation                               | Error code            | Exit |
| --------------------------------------- | --------------------- | ---- |
| No name given, or a flag error          | `usage`               | 2    |
| No environment has that name            | `environment_unknown` | 3    |

## Examples

```console
$ ws env rm linux-server
$ ws env rm linux-server --json
```
