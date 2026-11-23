# ws env ca

```
ws env ca rotate [--finish] [--force] [--json]
ws env ca status [--json]
```

Shows and rotates the environments CA, which signs every environment's
certificate. Rotating keeps the old CA trusted beside the new one, so no
environment is disconnected:

1. `ws env ca rotate` makes a new CA. The broker trusts both, and serves a
   certificate signed by the new one.
2. Connected environments get the new CA bundle at once.
3. Each environment's next certificate renewal is signed by the new CA.
   `ws env ca status` counts how many still hold one from the old CA.
4. `ws env ca rotate --finish` drops the old CA once none do. `--force`
   drops it anyway: the environments still on it are disconnected and need
   `ws env rotate`.

Both subcommands first finish a rotation that an interruption left
unfinished, so the CA is never left half-replaced.

## Subcommands

- `ws env ca rotate`: make a new CA, or with `--finish` drop the old one
- `ws env ca status`: show the current and old CA, and how many environments hold each

## JSON output

`ws env ca status --json`, with an old CA still trusted:

```json
{
  "current": {"fingerprint": "3b1f…", "not_after": "2027-11-22T00:00:00Z"},
  "old": {"fingerprint": "9a0c…", "not_after": "2027-03-01T00:00:00Z"},
  "environments_on_current": 2,
  "environments_on_old": 1,
  "warnings": []
}
```

Each subcommand with `--json` prints its own object, as its guide shows.

## Exit codes

| Situation                                                  | Error code   | Exit |
| ---------------------------------------------------------- | ------------ | ---- |
| Arguments or a flag error, `--force` without `--finish`, no CA yet, or nothing to finish | `usage` | 2 |
| `--finish` while environments still hold old-CA certificates | `ca_in_use` | 4 |

## Examples

```console
$ ws env ca status
$ ws env ca rotate
$ ws env ca rotate --finish
$ ws env ca rotate --finish --force
```
