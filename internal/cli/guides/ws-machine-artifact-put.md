# ws machine artifact put

```
ws machine artifact put <ID> <PATH|-> [--base-rev <n>] [--note <text>]
```

Adds a revision to an artifact from the file at `PATH`, which must be the same
kind as the artifact: a Markdown artifact takes Markdown, and so on. Earlier
revisions are kept. Comment anchors move to the new revision (ADR 0079); the
output says how many stayed anchored and how many detached.

Pass `--base-rev` with the revision your change is based on, which is the
latest one a comment or `show` told you about. If a person added another
revision since, `put` refuses with `artifact_conflict` and the latest revision
in `details`; read it, merge your change, and put again.

## Options

| Option       | Description |
| ------------ | ----------- |
| `--base-rev` | the revision this change is based on; conflict unless it is still the latest |
| `--note`     | a note on the new revision, one line of up to 200 characters |

## JSON output

Output is always JSON; `--json` changes nothing.

```json
{"artifact": {"id": "art-1a2b3c4d", "reference": "@artifact:art-1a2b3c4d", "name": "pitch.md",
  "title": "Pitch: cache sharing", "kind": "markdown", "content_type": "text/markdown; charset=utf-8",
  "latest": 2, "size": 2311, "created_at": "2026-11-23T18:00:00Z", "updated_at": "2026-11-23T19:10:00Z",
  "created_by": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"},
  "threads": {"open": 2, "resolved": 1, "detached": 1}, "revisions": []},
 "anchors": {"anchored": 3, "detached": 1}, "warnings": []}
```

The `revisions` list in `artifact` is every revision, as in `show`.

## Exit codes

| Situation                                      | Error code             | Exit |
| ---------------------------------------------- | ---------------------- | ---- |
| Run outside a workspace                        | `outside_workspace`    | 2    |
| Empty, a type or size that isn't allowed, the wrong kind, or a bad note | `artifact_invalid` | 2 |
| `--base-rev` under 1, or other flag errors     | `usage`                | 2    |
| No artifact with that id                       | `artifact_unknown`     | 3    |
| `--base-rev` isn't the latest revision         | `artifact_conflict`    | 4    |
| The artifact has 100 revisions, or the store is full | `artifact_full`  | 4    |
| Run on an enrolled environment's agent machine | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact put art-1a2b3c4d pitch.md --base-rev 1 --note "Done in r2"
$ cat pitch.md | ws machine artifact put art-1a2b3c4d -
```
