# ws env ca status

```
ws env ca status [--json]
```

Shows the environments CA: its fingerprint and when it expires, the old CA
if a rotation hasn't been finished, and how many environments hold a
certificate from each. Before the first `ws env add` there is no CA, and
the status says so; the command doesn't create one.

A warning names the CA's expiry once it is within `[environments]
renew_before_days` (default 30). Rotate before then, with `ws env ca rotate`.

## Flags

| Flag     | Meaning                  |
| -------- | ------------------------ |
| `--json` | Print the result as JSON |

## JSON output

```json
{
  "current": {"fingerprint": "3b1f…", "not_after": "2027-11-22T00:00:00Z"},
  "old": null,
  "environments_on_current": 3,
  "environments_on_old": 0,
  "warnings": []
}
```

With no CA, `current` and `old` are `null` and both counts are `0`.

## Exit codes

| Situation                 | Error code | Exit |
| ------------------------- | ---------- | ---- |
| Arguments or a flag error | `usage`    | 2    |

## Examples

```console
$ ws env ca status
$ ws env ca status --json
```
