# ws review profiles show

```
ws review profiles show [--json] <name>
```

Prints one review profile as `ws review profiles` lists it, with its
guide: the reviewer's instructions, the Markdown after the file's front
matter. `path` is the file it's read from, or null for a built-in.

## JSON output

```json
{"name": "go", "description": "Go: concurrency, error wrapping, …", "when": "auto",
 "match": ["*.go", "go.mod"], "model": "", "builtin": false, "source": "override",
 "guide": "Anchor a concurrency finding at…", "path": "/home/me/.config/ws/review/profiles/go.md",
 "warnings": []}
```

Without `--json`, the fields one per line, then the guide.

## Exit codes

| Situation                                        | Error code        | Exit |
| ------------------------------------------------ | ----------------- | ---- |
| Not exactly one name                             | `usage`           | 2    |
| No profile has the name                          | `profile_unknown` | 2    |
| Your file for the profile can't be parsed        | `config_invalid`  | 1    |

## Examples

```console
$ ws review profiles show --json security
```
