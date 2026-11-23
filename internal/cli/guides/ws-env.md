# ws env

```
ws env add <NAME> [--json]
ws env list [--json]
ws env rm <NAME> [--json]
ws env rotate <NAME> [--json]
ws env ca <OP>
```

Adds, lists, rotates and removes remote environments: other machines that
run `ws env agent run` and connect to this `ws`. `ws env add` makes one and
prints the code that enrolls it; the code is shown once. The first add
creates the environments CA, which signs every environment's certificate;
`ws env ca` rotates it.

For people. Agents don't run these: tell a person what you need.

## Subcommands

- `ws env add <NAME>`: make a pending environment and print its enrollment code
- `ws env list`: list the environments, and whether each is online
- `ws env rm <NAME>`: remove an environment and revoke its certificate
- `ws env rotate <NAME>`: give an environment a new code, revoking its certificate
- `ws env ca`: rotate the environments CA, and show its status

## JSON output

`--json` prints each subcommand's result as one object, as its guide shows.
`ws env list --json` looks like this:

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

## Exit codes

| Situation                                                  | Error code           | Exit |
| ---------------------------------------------------------- | -------------------- | ---- |
| Arguments or a flag error, or `environments.public_name` isn't set | `usage`              | 2    |
| The environment name isn't valid                           | `name_invalid`       | 2    |
| The environment is unknown                                 | `environment_unknown` | 3   |
| The name is already taken                                  | `environment_exists` | 4    |
| The CA still has environments on its old certificates      | `ca_in_use`          | 4    |

## Examples

```console
$ ws env add linux-server
$ ws env list
$ ws env rotate linux-server
$ ws env ca status
```
