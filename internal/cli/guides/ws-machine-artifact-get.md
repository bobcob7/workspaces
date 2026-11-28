# ws machine artifact get

```
ws machine artifact get <ID> [--rev <n>] [--out <dir>]
```

Copies a revision of an artifact (the latest, unless `--rev`) into a file in
your workspace, and prints its absolute `path`. Read that path with your Read
tool. The default directory is `<workspace>/.ws-artifacts/<id>/r<n>`. The file
keeps the artifact's name and is written 0600 in a 0700 directory; an existing
file is replaced. On an enrolled remote environment the file is written on
that machine, at the same path under your workspace, and the path printed is
the remote one.

A reference's revision works too: `@artifact:art-1a2b3c4d/r1` is the same as
`--rev 1`. Giving both a reference revision and a different `--rev` is `usage`.

Each `get` records that your workspace read that revision, so the artifact's
`conversations` (`show`) list your conversation. If that record can't be
written, the file is still copied and the failure is a `warnings` entry.

## Options

| Option  | Description |
| ------- | ----------- |
| `--rev` | the revision to copy, from 1; default the latest |
| `--out` | the directory to write into, made absolute; default `<workspace>/.ws-artifacts/<id>/r<n>` |

## JSON output

Output is always JSON; `--json` changes nothing.

```json
{"artifact": {"id": "art-1a2b3c4d", "reference": "@artifact:art-1a2b3c4d", "name": "login-error.png",
  "title": null, "kind": "image", "content_type": "image/png", "latest": 1, "size": 48213,
  "created_at": "2026-11-23T18:00:00Z", "updated_at": "2026-11-23T18:00:00Z",
  "created_by": {"kind": "person", "workspace": null, "name": null},
  "threads": {"open": 1, "resolved": 0, "detached": 0}, "conversation_count": 1, "conversation": null},
 "rev": 1, "path": "/home/me/work/buff-bear/.ws-artifacts/art-1a2b3c4d/r1/login-error.png",
 "content_type": "image/png", "size": 48213, "warnings": []}
```

## Exit codes

| Situation                                      | Error code             | Exit |
| ---------------------------------------------- | ---------------------- | ---- |
| Run outside a workspace                        | `outside_workspace`    | 2    |
| `--rev` under 1, or a reference and `--rev` disagree | `usage`          | 2    |
| No artifact with that id, or no such revision  | `artifact_unknown`     | 3    |
| Run on an enrolled environment's agent machine, if it can't reach the controller's artifact API | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact get art-1a2b3c4d
$ ws machine artifact get @artifact:art-1a2b3c4d/r1 --out ./shots
```
