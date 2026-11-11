# ws loop memory clear

```
ws loop memory clear [--json] <NAME>
```

Removes every key from a loop's memory, and prints how many were removed.

Output is text by default, or JSON with `--json`.

## JSON output

```json
{"loop": "deps-bump", "removed": 4, "warnings": []}
```

## Exit codes

| Situation                         | Error code       | Exit |
| --------------------------------- | ---------------- | ---- |
| Not exactly one loop name         | `usage`          | 2    |
| No loop has that name             | `loop_not_found` | 3    |

## Examples

```console
$ ws loop memory clear deps-bump
```
