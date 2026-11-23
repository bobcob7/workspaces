# ws env rotate

```
ws env rotate <NAME> [--json]
```

Gives environment `<NAME>` a new enrollment code, for when the old one was
lost or its certificate has expired. An enrolled environment's certificates
are revoked first: the current one and up to 5 earlier ones that renewal
superseded. The machine has to enroll again with the new code.

The new code is printed once, as `ws env add` prints it, and the earlier
code stops working.

## Flags

| Flag     | Meaning                  |
| -------- | ------------------------ |
| `--json` | Print the result as JSON |

## JSON output

```json
{
  "id": "env-1a2b3c4d",
  "name": "linux-server",
  "state": "pending",
  "code": "ws-enroll-v1.…",
  "revoked_serials": ["5e0c…", "4b7a…"],
  "warnings": []
}
```

`revoked_serials` is empty for an environment that wasn't enrolled. Otherwise
it lists up to 6 serials, the current certificate first, as `ws env rm` does.

## Exit codes

| Situation                                         | Error code            | Exit |
| ------------------------------------------------- | --------------------- | ---- |
| No name given, a flag error, or `environments.public_name` isn't set | `usage` | 2 |
| No environment has that name                      | `environment_unknown` | 3    |

## Examples

```console
$ ws env rotate linux-server
$ ws env rotate linux-server --json
```
