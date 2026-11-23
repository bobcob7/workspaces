# ws artifact

Shares files with agents and reads their comments. `ws artifact` is hidden
from `ws --help` and `ws llm`: the desktop app's Artifacts section runs it,
and a person can run it from a terminal. Agents don't use it; they run
[`ws machine artifact`](ws-machine.md#ws-machine-artifact), which is
documented in `ws llm` and in `ws-machine.md`. Decided in
[0078](../adrs/0078-artifacts.md) and
[0079](../adrs/0079-artifact-comments-and-anchors.md).

```
ws artifact add <PATH|-> [--name <name>] [--title <text>] [--note <text>] [--json]
ws artifact list [--kind image|markdown|text] [--workspace <id>] [--limit <n>] [--json]
ws artifact show <ID> [--json]
ws artifact cat <ID> [--rev <n>]
ws artifact rm <ID>... [--json]
ws artifact comment list <ID> [--open] [--json]
ws artifact comment add <ID> (--body <text> | --body-file <path|->) [--reply <thread> | --quote <text> [--line <n>] [--prefix <text>] [--suffix <text>]] [--json]
ws artifact comment resolve <ID> <THREAD> [--reopen] [--json]
```

## Description

An artifact is one file, a screenshot, a Markdown document or a text file,
that a person shares with agents. Each change to it is a new **revision**;
agents read the revisions and reply to comment threads on them. A person
adds an artifact in the desktop app (drop, paste or pick) or with `ws
artifact add`, and attaches it to a prompt as `@artifact:<id>`. The author
of anything a person adds is the person; agents' revisions come from
`ws machine artifact`.

`--json` works as for every people command; without it the output is a
short table or message. `cat` writes the revision's raw bytes to stdout even
with `--json`; errors are still JSON on stderr.

## Kinds and limits

| Kind | `content_type` | Detected by | Max size | Preview |
| --- | --- | --- | --- | --- |
| `image` | `image/png`, `image/jpeg`, `image/gif`, `image/webp` | `http.DetectContentType` of the first 512 bytes | 10 MiB | `<img>` |
| `image` | `image/svg+xml` | name ends `.svg` (any case), valid UTF-8, first element is `<svg` after an optional `<?xml?>`, comments, whitespace and `<!DOCTYPE>` | 1 MiB | `<img>` only |
| `markdown` | `text/markdown; charset=utf-8` | name ends `.md` or `.markdown` (any case), valid UTF-8, no NUL byte | 1 MiB | rendered, comments in the margin |
| `text` | `text/plain; charset=utf-8` | any other valid UTF-8 with no NUL byte | 1 MiB | `<pre>` |

Refused with `artifact_invalid`: empty files; anything else (other binaries,
PDF, archives, non-UTF-8 text); a size over the kind's limit; a revision
whose kind differs from the artifact's.

| Limit | Value | Error |
| --- | --- | --- |
| Name | 1-128 characters, one path element, no `/`, `\`, control characters, no leading `.` | `artifact_invalid` |
| Title, revision note | up to 200 characters, one line; empty means none | `artifact_invalid` |
| Revisions an artifact | 100 | `artifact_full` |
| Store | 2 GiB, the sum of every revision's `size` | `artifact_full` |
| Threads an artifact | 200 | `artifact_full` |
| Comments a thread | 50 | `artifact_full` |
| Comment body | 1-10,000 characters, not only whitespace | `artifact_invalid` |
| Anchor quote | 1-2,000 characters, not only whitespace | `artifact_invalid` |
| `list --limit` | 1-1,000, default 100 | `usage` |

## Ids and references

- An artifact id is `art-` and 8 lowercase hex digits: `art-1a2b3c4d`.
- A reference is `@artifact:<id>` (latest revision) or `@artifact:<id>/r<n>`.
  Regex: `@artifact:(art-[0-9a-f]{8})(?:/r([1-9][0-9]{0,2}))?`.
- Every command that takes an `<ID>` also takes a reference. A reference
  with `/r<n>` and a `--rev` that differs is `usage`.
- `ws claude`, `ws machine spawn` and the desktop's reply box append a hint
  line to a prompt that has references, after one blank line:

  ```
  (Artifacts art-1a2b3c4d, art-5e6f7a8b: run "ws machine artifact get <id>" for each, then read the file at the "path" it prints.)
  ```

  The ids are in first-seen order, without duplicates. The line is appended
  once: a prompt that already contains `run "ws machine artifact get <id>"`
  gets nothing.

## On disk

Under `CONFIG_DIR` (`artifacts/`, see [config](../config.md#layout)); private to
`ws`. The JSON below is the contract, not the files.

```
artifacts/                    0700
  .tmp/                       temp files; a write deletes leftovers older than 1 h
  art-1a2b3c4d/               0700
    .lock                     flock, exclusive for every write; readers take none
    artifact.json             0600, the record
    r1  r2 …                  0600, each revision's bytes; never rewritten
    comments.json             0600, threads; absent until the first comment
```

Writes go to `.tmp/` and are renamed into place under `<id>/.lock`. `rm`
renames `<id>/` into `.tmp/` under the lock and then deletes it, so a reader
sees a whole artifact or none. A record or comments file with a `version`
above 1 is refused with `artifact_invalid` naming the file, and the artifact
is listed with a warning and never written. Comments and threads are never
deleted, so ids are never reused.

`ws machine artifact get` copies a revision into the workspace of the agent
that asked, under `.ws-artifacts/`; see [state](../state.md#layout).

## Output

All output follows [0016](../adrs/0016-output-contract.md): `snake_case`,
RFC 3339 UTC times, `null` for no value, `[]` for an empty list, and a
`warnings` array on every top-level object.

**Actor** (`created_by`, `author`, `resolved_by`):

```json
{"kind": "person", "workspace": null, "name": null}
{"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"}
```

**Artifact summary** (`list`, and `artifact` in `show` and `get`):

```json
{"id": "art-1a2b3c4d", "reference": "@artifact:art-1a2b3c4d", "name": "pitch.md",
 "title": "Pitch: cache sharing", "kind": "markdown", "content_type": "text/markdown; charset=utf-8",
 "latest": 2, "size": 2311, "created_at": "2026-11-23T18:00:00Z", "updated_at": "2026-11-23T19:10:00Z",
 "created_by": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"},
 "threads": {"open": 2, "resolved": 1, "detached": 1}}
```

`size` and `content_type` are the latest revision's; `updated_at` is the
latest revision's `created_at`. `threads.open` counts unresolved threads;
`threads.detached` counts threads whose anchor is `detached`, open or not.

**Artifact** (`add`, `show`): the summary plus `revisions`, oldest first:
`[{"rev", "content_type", "size", "sha256", "created_at", "created_by",
"note"}]`. A revision may change type (PNG to JPEG) but never kind.

**Thread**:

```json
{"id": "t1",
 "anchor": {"rev": 2, "start": 120, "end": 139, "line_start": 7, "line_end": 7,
            "quote": "the cache is shared", "prefix": "Today ", "suffix": " across workspaces.",
            "state": "anchored"},
 "resolved": false, "resolved_at": null, "resolved_by": null,
 "comments": [
   {"id": "c1", "author": {"kind": "person", "workspace": null, "name": null},
    "body": "Shared how? Per repo or per user?", "created_at": "2026-11-23T18:20:00Z"}]}
```

`anchor` is `null` for a thread about the whole artifact. `start` and `end`
are UTF-8 byte offsets into revision `rev`; `state` is `anchored` when `rev`
is the latest revision, else `detached`.

## ws artifact add

Stores a file as a new artifact. `<PATH|->` reads a file or stdin; stdin
needs `--name`. `--name` defaults to the file's base name.

Prints `{"artifact": <artifact>, "warnings": []}`.

## ws artifact list

Lists artifacts, latest `updated_at` first. `--kind` keeps one kind;
`--workspace <id>` keeps those whose `created_by.workspace` or `root` is
`<id>`; `--limit` caps the count.

Prints `{"artifacts": [<summary>…], "warnings": []}`.

## ws artifact show

Prints one artifact with its revisions.

Prints `{"artifact": <artifact>, "warnings": []}`.

## ws artifact cat

Writes revision `--rev` (default latest) to stdout as raw bytes. Nothing else
is printed on success, so pipe it to a file: `ws artifact cat art-1a2b3c4d >
login-error.png`.

## ws artifact rm

Removes each artifact, any artifact, including one an agent created. Each
item is reported; the exit code is the first failure's.

Prints `{"artifacts": [{"id": "art-…", "removed": true, "error": null}], "warnings": []}`.

## ws artifact comment list

Lists threads in creation order. `--open` drops resolved ones.

Prints `{"artifact_id": "art-…", "latest": 2, "threads": [<thread>…], "warnings": []}`.

## ws artifact comment add

Adds a thread, anchored to the text `--quote` names (or to the whole artifact
without it), or, with `--reply <thread>`, a comment on an existing thread.
`--prefix` and `--suffix` are the context the desktop sends with a selection
([0079](../adrs/0079-artifact-comments-and-anchors.md)); people rarely need
them. `--body-file -` reads the body from stdin.

`--quote` with `--reply` is `usage`; `--line` without `--quote` is `usage`;
`--body` and `--body-file` together, or neither, is `usage`. A quote on an
`image` is `artifact_invalid`.

Prints `{"artifact_id": "art-…", "thread": <thread>, "comment": <comment>, "warnings": []}`.

## ws artifact comment resolve

Marks the thread resolved, or with `--reopen` open again.

Prints `{"artifact_id": "art-…", "thread": <thread>, "warnings": []}`.

## Errors

| Situation | Error code | Exit |
| --- | --- | --- |
| `--body` with `--body-file`, `--line` without `--quote`, `--quote` with `--reply`, `--limit` out of range, a `--rev` that differs from a reference's `/r<n>` | `usage` | 2 |
| A type that isn't allowed, a size over the limit, an empty file, a kind change, a bad name, title, note, body or quote, an anchored comment on an image, a record from a newer `ws` | `artifact_invalid` | 2 |
| No artifact, revision or thread with that id | `artifact_unknown` | 3 |
| The quote isn't in the latest revision, or occurs more than once and `--line` doesn't pick one; `details: {"occurrences": <n>, "lines": [<n>…]}` | `anchor_not_found` | 3 |
| A revision, thread, comment or store limit | `artifact_full` | 4 |
| `ws machine artifact` on an enrolled environment agent's machine | `artifact_unreachable` | 1 |

On a remote agent machine `ws artifact` is not refused: a person there sees
their own store, which isn't the controller's.

## Examples

```console
$ ws artifact add login-error.png --title "Login error" --json
{"artifact": {"id": "art-1a2b3c4d", "reference": "@artifact:art-1a2b3c4d", …}, "warnings": []}

$ ws artifact comment add art-1a2b3c4d --quote "the cache is shared" --body "Per repo or per user?"

$ ws artifact comment resolve art-1a2b3c4d t1
```

## See also

- [`ws machine artifact`](ws-machine.md#ws-machine-artifact): what agents run
- [0078](../adrs/0078-artifacts.md), [0079](../adrs/0079-artifact-comments-and-anchors.md)
