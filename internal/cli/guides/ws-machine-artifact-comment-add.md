# ws machine artifact comment add

```
ws machine artifact comment add <ID> (--body <text> | --body-file <path|->) [--reply <thread> | --quote <text> [--line <n>]]
```

Starts a comment thread on an artifact, or with `--reply`, adds a comment to
an existing thread. Without `--quote` the thread is about the whole artifact.
With `--quote` it is anchored to that text in the artifact's latest revision.

The quote must be found exactly once in that revision, after whitespace is
collapsed. If it occurs more than once, pass `--line` with the line it starts
on; the error names the lines it occurs on (`anchor_not_found`, with
`occurrences` and `lines` in `details`).

Give the body with `--body`, or with `--body-file` to read it from a file, or
from stdin with `-`. Give exactly one of them.

## Options

| Option        | Description |
| ------------- | ----------- |
| `--body`      | the comment's text, 1 to 10,000 characters |
| `--body-file` | read the comment's text from this file, or `-` for stdin |
| `--reply`     | reply to this thread, such as `t1`; not with `--quote` |
| `--quote`     | anchor a new thread to this text in the latest revision |
| `--line`      | the line the quote starts on, when it occurs more than once; needs `--quote` |

## JSON output

Output is always JSON; `--json` changes nothing.

```json
{"artifact_id": "art-1a2b3c4d",
 "thread": {"id": "t3",
   "anchor": {"rev": 2, "start": 40, "end": 58, "line_start": 3, "line_end": 3,
              "quote": "Done in r2", "prefix": "", "suffix": "",
              "state": "anchored"},
   "resolved": false, "resolved_at": null, "resolved_by": null,
   "comments": [
    {"id": "c1", "author": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"},
     "body": "Reworded in r2.", "created_at": "2026-11-23T19:10:00Z"}]},
 "comment": {"id": "c1", "author": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"},
   "body": "Reworded in r2.", "created_at": "2026-11-23T19:10:00Z"},
 "warnings": []}
```

With `--reply`, `thread` is the thread and `comment` is the new comment in it.

## Exit codes

| Situation                                                        | Error code             | Exit |
| ---------------------------------------------------------------- | ---------------------- | ---- |
| Run outside a workspace                                          | `outside_workspace`    | 2    |
| `--reply` with `--quote`, `--line` without `--quote`, `--line` under 1, a body given twice or not at all | `usage` | 2 |
| An empty or too long body, a quote that is empty or too long, or a quote on an image | `artifact_invalid` | 2 |
| No artifact with that id, or no thread `--reply` names          | `artifact_unknown`     | 3    |
| The quote isn't in the latest revision, or occurs more than once without a `--line` that picks one | `anchor_not_found` | 3 |
| The artifact has 200 threads, or the thread 50 comments         | `artifact_full`        | 4    |
| Run on an enrolled environment's agent machine                   | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact comment add art-1a2b3c4d --body "Looks good overall"
$ ws machine artifact comment add art-1a2b3c4d --quote "the cache is shared" --body "Shared how?"
$ ws machine artifact comment add art-1a2b3c4d --quote "Done" --line 9 --body "Which section?"
$ ws machine artifact comment add art-1a2b3c4d --reply t1 --body-file reply.md
```
</content>
