# ws review profiles rm

```
ws review profiles rm [--json] <name>
```

Removes your file for a review profile. For a profile you added, the
profile is gone; for an override, the built-in is back, as `restored`
says. A built-in you haven't edited can't be removed: set it to `off`
instead, so it joins only reviews that ask for it by name.

Reviews already running aren't affected.

## JSON output

```json
{"name": "go", "path": "/home/me/.config/ws/review/profiles/go.md", "restored": true, "warnings": []}
```

## Exit codes

| Situation                                        | Error code        | Exit |
| ------------------------------------------------ | ----------------- | ---- |
| Not exactly one name                             | `usage`           | 2    |
| An invalid or reserved name                      | `profile_invalid` | 2    |
| No profile has the name                          | `profile_unknown` | 2    |
| A built-in with no override                      | `profile_builtin` | 4    |

## Examples

```console
$ ws review profiles rm --json go
```
