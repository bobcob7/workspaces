# ws machine artifact add

```
ws machine artifact add <PATH|-> [--name <name>] [--title <text>] [--note <text>]
```

Stores the file at `PATH` as a new artifact, and prints it. `-` reads stdin,
which needs `--name`, since stdin has no file name. Without `--name`, the
artifact is named after the file's base name. The artifact belongs to your
workspace tree, so only it can `rm` the artifact; people can always read it.

## Options

| Option    | Description |
| --------- | ----------- |
| `--name`  | the name to store it under: one path element, 1 to 128 characters; needed for `-` |
| `--title` | a title to show instead of the name, one line of up to 200 characters |
| `--note`  | a note on the first revision, one line of up to 200 characters |

## JSON output

Output is always JSON; `--json` changes nothing.

```json
{"artifact": {"id": "art-1a2b3c4d", "reference": "@artifact:art-1a2b3c4d", "name": "pitch.md",
  "title": "Pitch: cache sharing", "kind": "markdown", "content_type": "text/markdown; charset=utf-8",
  "latest": 1, "size": 2048, "created_at": "2026-11-23T18:00:00Z", "updated_at": "2026-11-23T18:00:00Z",
  "created_by": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"},
  "threads": {"open": 0, "resolved": 0, "detached": 0}, "conversation_count": 1, "conversation": null,
  "revisions": [{"rev": 1, "content_type": "text/markdown; charset=utf-8", "size": 2048,
    "sha256": "9f86d0…", "created_at": "2026-11-23T18:00:00Z",
    "created_by": {"kind": "agent", "workspace": "pitch-ab12cd34", "name": "pitch"}, "note": null}],
  "conversations": [{"workspace": "pitch-ab12cd34", "name": "pitch", "environment": null, "removed": false,
    "actions": ["created"], "revs": [1], "first_at": "2026-11-23T18:00:00Z", "last_at": "2026-11-23T18:00:00Z"}]},
 "warnings": []}
```

`conversations` lists, one row per workspace and latest first, the agents' conversations the artifact was part of: `actions` from `created`, `revised`, `commented`, `resolved`, `read` (a `get`) and `cited` (a prompt or message named it), the revisions involved, and `removed` (`null` for a workspace on another environment). People's actions aren't listed. The rows are self-reported and may lag: citations in transcripts are recorded by the desktop app (ADR 0084).

## Exit codes

| Situation                                              | Error code             | Exit |
| ------------------------------------------------------ | ---------------------- | ---- |
| Run outside a workspace                                | `outside_workspace`    | 2    |
| Empty, a type that isn't allowed, over its size, a bad name, title or note | `artifact_invalid` | 2 |
| `-` without `--name`, or other flag errors             | `usage`                | 2    |
| The store or the tree is at a limit                    | `artifact_full`        | 4    |
| Run on an enrolled environment's agent machine         | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact add pitch.md --title "Pitch: cache sharing"
$ printf 'hi' | ws machine artifact add - --name notes.txt
```
