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
  "threads": {"open": 2, "resolved": 1, "detached": 1}, "conversation_count": 2, "conversation": null,
  "revisions": [
    {"rev": 1, "content_type": "text/markdown; charset=utf-8", "size": 2048, "sha256": "9f86d0…",
     "created_at": "2026-11-23T18:00:00Z", "created_by": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"}, "note": null},
    {"rev": 2, "content_type": "text/markdown; charset=utf-8", "size": 2311, "sha256": "2c26b4…",
     "created_at": "2026-11-23T19:10:00Z", "created_by": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"}, "note": "Done in r2"}],
  "conversations": [
    {"workspace": "fix-cache-1a2b3c4d", "name": "fix-cache", "environment": null, "removed": false,
     "actions": ["read", "cited"], "revs": [2], "first_at": "2026-11-23T19:00:00Z", "last_at": "2026-11-23T19:20:00Z"},
    {"workspace": "pitch-ab12cd34", "name": "pitch", "environment": null, "removed": false,
     "actions": ["created", "revised"], "revs": [1, 2], "first_at": "2026-11-23T18:00:00Z", "last_at": "2026-11-23T19:10:00Z"}]},
 "warnings": []}
```

`conversations` lists, one row per workspace and latest first, the agents' conversations the artifact was part of: `actions` from `created`, `revised`, `commented`, `resolved`, `read` (a `get`) and `cited` (a prompt or message named it), the revisions involved, and `removed` (`null` for a workspace on another environment). People's actions aren't listed. The rows are self-reported and may lag: citations in transcripts are recorded by the desktop app (ADR 0084).

## Exit codes

| Situation                                      | Error code             | Exit |
| ---------------------------------------------- | ---------------------- | ---- |
| Run outside a workspace                        | `outside_workspace`    | 2    |
| No artifact with that id                       | `artifact_unknown`     | 3    |
| `<ID>` is not an id or reference               | `artifact_unknown`     | 3    |
| Run on an enrolled environment's agent machine, if it can't reach the controller's artifact API | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact show art-1a2b3c4d
```
