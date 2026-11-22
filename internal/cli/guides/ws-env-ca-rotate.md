# ws env ca rotate

```
ws env ca rotate [--finish] [--force] [--json]
```

Without flags, makes a new environments CA. The previous CA stays trusted
until `--finish`, and the environments' next certificate renewal is signed
by the new one. Connected environments stay connected.

With `--finish`, drops the old CA once no environment holds a certificate
from it. If some still do, the command is refused with `ca_in_use`; `ws env
ca status` lists how many. `--force`, which needs `--finish`, drops it
anyway: the environments still on it are listed as `stranded` and need
`ws env rotate`.

If an interruption left a rotation unfinished, the rotation is finished
first, so `ws env ca rotate` never fails because of it.

## Flags

| Flag       | Meaning                                                                 |
| ---------- | ----------------------------------------------------------------------- |
| `--finish` | Drop the old CA once no environment holds its certificates              |
| `--force`  | With `--finish`, drop the old CA even while environments still hold it |
| `--json`   | Print the result as JSON                                                |

## JSON output

```json
{
  "current": {"fingerprint": "3b1f…", "not_after": "2027-11-22T00:00:00Z"},
  "old": {"fingerprint": "9a0c…", "not_after": "2027-03-01T00:00:00Z"},
  "dropped": null,
  "stranded": [],
  "warnings": []
}
```

With `--finish`, `old` is `null`, `dropped` is the CA that was removed, and
`stranded` names the environments left on it:

```json
{
  "current": {"fingerprint": "3b1f…", "not_after": "2027-11-22T00:00:00Z"},
  "old": null,
  "dropped": {"fingerprint": "9a0c…", "not_after": "2027-03-01T00:00:00Z"},
  "stranded": ["win-rig"],
  "warnings": []
}
```

## Exit codes

| Situation                                                    | Error code   | Exit |
| ------------------------------------------------------------ | ------------ | ---- |
| `--force` without `--finish`, a flag error, no CA yet, or `--finish` with no rotation to finish | `usage` | 2 |
| `--finish` while environments still hold old-CA certificates | `ca_in_use`  | 4    |

## Examples

```console
$ ws env ca rotate
$ ws env ca rotate --finish
$ ws env ca rotate --finish --force --json
```
