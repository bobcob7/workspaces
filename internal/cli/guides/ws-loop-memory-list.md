# ws loop memory list

```
ws loop memory list [--json] <NAME>
```

Lists a loop's memory, sorted by key. Text output is one line per key: the
key, its revision, when it was last changed, and the first line of its value,
cut to 60 characters.

Output is text by default, or JSON with `--json`.

## JSON output

```json
{"loop": "deps-bump", "entries": [{"key": "seen", "value": "#412 #415", "rev": 3, "updated_at": "2026-11-10T08:14:00Z", "updated_by": "loop-deps-bump-1a2b3c4d"}], "bytes": 9, "warnings": []}
```

`bytes` is the total length of all values. `updated_by` is `null` for a
change made by `ws loop memory`, and the caller's workspace for one made by
`ws machine memory`.

## Exit codes

| Situation           | Error code       | Exit |
| ------------------- | ---------------- | ---- |
| Not exactly one loop name | `usage`    | 2    |
| No loop has that name | `loop_not_found` | 3  |

## Examples

```console
$ ws loop memory list deps-bump
$ ws loop memory list --json deps-bump
```
