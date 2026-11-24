# ws machine artifact show

```
ws machine artifact show <ID>
```

Shows one artifact with every revision, oldest first. `<ID>` may be a
reference such as `@artifact:art-1a2b3c4d/r2`; the `/r2` is accepted and
ignored, since `show` always prints every revision.

## Options

None.

## JSON output

Output is always JSON; `--json` changes nothing.

```json
{"artifact": {"id": "art-1a2b3c4d", "reference": "@artifact:art-1a2b3c4d", "name": "pitch.md",
  "title": "Pitch: cache sharing", "kind": "markdown", "content_type": "text/markdown; charset=utf-8",
  "latest": 2, "size": 2311, "created_at": "2026-11-23T18:00:00Z", "updated_at": "2026-11-23T19:10:00Z",
  "created_by": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"},
  "threads": {"open": 2, "resolved": 1, "detached": 1},
  "revisions": [
    {"rev": 1, "content_type": "text/markdown; charset=utf-8", "size": 2048, "sha256": "9f86d0…",
     "created_at": "2026-11-23T18:00:00Z", "created_by": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"}, "note": null},
    {"rev": 2, "content_type": "text/markdown; charset=utf-8", "size": 2311, "sha256": "2c26b4…",
     "created_at": "2026-11-23T19:10:00Z", "created_by": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"}, "note": "Done in r2"}]},
 "warnings": []}
```

## Exit codes

| Situation                                      | Error code             | Exit |
| ---------------------------------------------- | ---------------------- | ---- |
| Run outside a workspace                        | `outside_workspace`    | 2    |
| No artifact with that id                       | `artifact_unknown`     | 3    |
| `<ID>` is not an id or reference               | `artifact_unknown`     | 3    |
| Run on an enrolled environment's agent machine | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact show art-1a2b3c4d
```
