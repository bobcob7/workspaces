# ws machine artifact list

```
ws machine artifact list [--kind image|markdown|text] [--mine] [--limit <n>]
```

Lists artifacts, latest revision first. `--mine` keeps only those your
workspace tree created. Each entry is a summary: the latest revision's type
and size, and its comment thread counts.

## Options

| Option   | Description |
| -------- | ----------- |
| `--kind` | only artifacts of this kind: `image`, `markdown` or `text` |
| `--mine` | only artifacts your workspace tree created |
| `--limit` | how many to list, from 1 to 1000 (default 100) |

## JSON output

Output is always JSON; `--json` changes nothing.

```json
{"artifacts": [{"id": "art-1a2b3c4d", "reference": "@artifact:art-1a2b3c4d", "name": "login-error.png",
  "title": null, "kind": "image", "content_type": "image/png", "latest": 1, "size": 48213,
  "created_at": "2026-11-23T18:00:00Z", "updated_at": "2026-11-23T18:00:00Z",
  "created_by": {"kind": "person", "workspace": null, "name": null},
  "threads": {"open": 1, "resolved": 0, "detached": 0}, "conversation_count": 2, "conversation": null}],
 "warnings": []}
```

## Exit codes

| Situation                                      | Error code             | Exit |
| ---------------------------------------------- | ---------------------- | ---- |
| Run outside a workspace                        | `outside_workspace`    | 2    |
| An unknown `--kind`, or `--limit` outside 1 to 1000 | `usage`           | 2    |
| Run on an enrolled environment's agent machine | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact list --mine
$ ws machine artifact list --kind markdown --limit 10
```
