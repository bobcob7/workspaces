# ws machine envs

```
ws machine envs
```

Lists the environments your workspace may start children on: your scope.
`local` is this machine; the others are environments a person granted with
`ws claude --env-scope` or `[environments] default_scope`. Pass one of them
to `ws machine spawn --env <name>`.

## When to use it

- Before `ws machine spawn --env`, to see which environments you may use, and
  whether each is online.
- To check where you are: `current` marks the environment this workspace runs on.
- Not to find out about environments outside your scope: `ws machine envs`
  lists only what you may use.

Output is always JSON; `--json` is accepted and changes nothing.

## JSON output

```json
{
  "environments": [
    {"name": "local", "id": "local", "state": "online", "current": true},
    {"name": "win-rig", "id": "env-2b3c4d5e", "state": "offline", "current": false}
  ],
  "warnings": []
}
```

`state` is `online`, `offline` or `removed` (in your scope, but `ws env rm`
removed it). `local` is `online` when this machine is the controller. On a
remote environment whose controller can't be reached, each entry comes from
your scope file with `state` `unknown`, and `warnings` says so. `current` is
the entry for this environment. The list is `current` first, then by name.

## Exit codes

| Situation               | Error code          | Exit |
| ----------------------- | ------------------- | ---- |
| Run outside a workspace | `outside_workspace` | 2    |
| Internal failure        | `internal`          | 1    |

## Examples

```console
$ ws machine envs
$ ws machine spawn --env win-rig -- "Run the Windows build and report"
```
