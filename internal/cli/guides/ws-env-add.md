# ws env add

```
ws env add <NAME> [--json]
```

Makes a pending environment called `<NAME>` and prints its enrollment code,
which works once and is shown once. The code names `environments.public_name`
and the port of the enrollment listener, and pins the CA's fingerprint, so
the remote machine checks it's talking to this `ws`. Run the two lines it
prints on the remote machine:

```
Code (works once, shown once):
  ws-enroll-v1.…

On linux-server:
  ws env agent enroll ws-enroll-v1.…
  ws env agent run            # or install the service: see docs/tools/ws-env.md
```

The first add creates the environments CA. A name takes 1 to 32 of `a-z`,
`0-9` and `-`, starts with a letter, and isn't `local`. `public_name` must
be set first: `ws config set environments.public_name <dns name or IP>`.

The daemon, if one runs, reloads at once; with none, the environment
connects once a daemon runs.

## Flags

| Flag     | Meaning                    |
| -------- | -------------------------- |
| `--json` | Print the result as JSON   |

## JSON output

```json
{
  "id": "env-1a2b3c4d",
  "name": "linux-server",
  "state": "pending",
  "code": "ws-enroll-v1.…",
  "ca_created": true,
  "warnings": []
}
```

`ca_created` is `true` when this add created the CA. The code is the only
place the secret appears: it's never logged, not even with `-v`.

## Exit codes

| Situation                                                   | Error code           | Exit |
| ----------------------------------------------------------- | -------------------- | ---- |
| No name given, a flag error, or `environments.public_name` isn't set | `usage`     | 2    |
| The name isn't valid, or is `local`                         | `name_invalid`       | 2    |
| An environment with that name exists                        | `environment_exists` | 4    |

## Examples

```console
$ ws env add linux-server
$ ws env add linux-server --json
```
