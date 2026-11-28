# ws machine artifact comment resolve

```
ws machine artifact comment resolve <ID> <THREAD> [--reopen]
```

Marks a comment thread resolved, or with `--reopen`, open again. Resolving a
thread that is already resolved, or reopening an open one, changes nothing and
is not an error. The thread's `resolved_at` and `resolved_by` are set when it
is resolved and cleared when it is reopened.

## Options

| Option     | Description |
| ---------- | ----------- |
| `--reopen` | open the thread again |

## JSON output

Output is always JSON; `--json` changes nothing.

```json
{"artifact_id": "art-1a2b3c4d",
 "thread": {"id": "t1",
   "anchor": {"rev": 2, "start": 120, "end": 139, "line_start": 7, "line_end": 7,
              "quote": "the cache is shared", "prefix": "Today ", "suffix": " across workspaces.",
              "state": "anchored"},
   "resolved": true, "resolved_at": "2026-11-23T19:20:00Z",
   "resolved_by": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"},
   "comments": [
    {"id": "c1", "author": {"kind": "person", "workspace": null, "name": null},
     "body": "Shared how? Per repo or per user?", "created_at": "2026-11-23T18:20:00Z"}]},
 "warnings": []}
```

## Exit codes

| Situation                                      | Error code             | Exit |
| ---------------------------------------------- | ---------------------- | ---- |
| Run outside a workspace                        | `outside_workspace`    | 2    |
| No artifact with that id, or no thread with that id | `artifact_unknown` | 3 |
| Run on an enrolled environment's agent machine, if it can't reach the controller's artifact API | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact comment resolve art-1a2b3c4d t1
$ ws machine artifact comment resolve @artifact:art-1a2b3c4d t1 --reopen
```
</content>
