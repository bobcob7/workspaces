# ws machine artifact comment list

```
ws machine artifact comment list <ID> [--open]
```

Prints the comment threads on an artifact, in the order they were started,
with the latest revision number. Each thread shows its anchor: the quote it is
about, the revision it was made on, and whether that revision is still the
latest (`anchored`) or an older one (`detached`). A thread with a `null`
anchor is about the whole artifact.

Reference the artifact by its id or as `@artifact:<id>`.

## Options

| Option   | Description |
| -------- | ----------- |
| `--open` | only the threads that aren't resolved |

## JSON output

Output is always JSON; `--json` changes nothing.

```json
{"artifact_id": "art-1a2b3c4d", "latest": 2,
 "threads": [
  {"id": "t1",
   "anchor": {"rev": 2, "start": 120, "end": 139, "line_start": 7, "line_end": 7,
              "quote": "the cache is shared", "prefix": "Today ", "suffix": " across workspaces.",
              "state": "anchored"},
   "resolved": false, "resolved_at": null, "resolved_by": null,
   "comments": [
    {"id": "c1", "author": {"kind": "person", "workspace": null, "name": null},
     "body": "Shared how? Per repo or per user?", "created_at": "2026-11-23T18:20:00Z"}]}],
 "warnings": []}
```

## Exit codes

| Situation                                      | Error code             | Exit |
| ---------------------------------------------- | ---------------------- | ---- |
| Run outside a workspace                        | `outside_workspace`    | 2    |
| No artifact with that id                       | `artifact_unknown`     | 3    |
| Run on an enrolled environment's agent machine, if it can't reach the controller's artifact API | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact comment list art-1a2b3c4d
$ ws machine artifact comment list @artifact:art-1a2b3c4d --open
```
</content>
