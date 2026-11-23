# ws machine artifact comment

```
ws machine artifact comment list <ID> [--open]
ws machine artifact comment add <ID> (--body <text> | --body-file <path|->) [--reply <thread> | --quote <text> [--line <n>]]
ws machine artifact comment resolve <ID> <THREAD> [--reopen]
```

Reads and answers the comments people leave on an artifact you shared. A
thread is one comment and its replies. A thread is either about the whole
artifact, or anchored to a quote in its latest revision. When you `put` a new
revision, anchors move with the text they quote; a quote that is gone leaves
its thread detached, and `put` reports how many stayed anchored.

The loop: `comment list <id> --open` to see what is still open, change the
file, `put` it with `--base-rev`, then `comment add --reply <thread>` for each
thread you addressed, and `comment resolve` once it is done.

Output is always JSON; `--json` changes nothing. Errors are one JSON line on
stderr. Run inside your workspace: outside one, every command fails with
`outside_workspace`.

## Subcommands

- `list`: the artifact's threads, in order. `--open` keeps the unresolved ones.
- `add`: start a thread, about the whole artifact or about a `--quote`, or reply
  to a thread with `--reply`.
- `resolve`: mark a thread resolved, or open it again with `--reopen`.

## Rules

- A comment is 1 to 10,000 characters and is not only whitespace.
- A quote is 1 to 2,000 characters. It must occur once in the latest revision,
  or `--line` picks one of the places it occurs.
- A quote can't anchor a comment on an image; an image takes whole-artifact
  comments only.
- An artifact keeps at most 200 threads, and a thread at most 50 comments.

## Exit codes

| Situation                                                        | Error code             | Exit |
| ---------------------------------------------------------------- | ---------------------- | ---- |
| Run outside a workspace                                          | `outside_workspace`    | 2    |
| Flags that don't agree, or a body given two ways or none         | `usage`                | 2    |
| A body, quote or anchor that isn't allowed                       | `artifact_invalid`     | 2    |
| No artifact, or no thread with that id                           | `artifact_unknown`     | 3    |
| A quote that isn't in the latest revision, or is ambiguous       | `anchor_not_found`     | 3    |
| The artifact or thread is at a limit                             | `artifact_full`        | 4    |
| Run on an enrolled environment's agent machine                   | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact comment list art-1a2b3c4d --open
$ ws machine artifact comment add art-1a2b3c4d --quote "the cache is shared" --body "Done in r2"
$ ws machine artifact comment add art-1a2b3c4d --reply t1 --body-file reply.md
$ ws machine artifact comment resolve art-1a2b3c4d t1
```
</content>
