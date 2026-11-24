# ws machine artifact rm

```
ws machine artifact rm <ID>...
```

Removes each artifact your workspace tree created, with its revisions and
comments. Each argument is one item in the output: an artifact another tree
created, or one that doesn't exist, fails on its own and the others still go.
Removing an artifact can't be undone. A person removes an artifact another
tree made.

## Options

None.

## JSON output

Output is always JSON; `--json` changes nothing.

```json
{"artifacts": [{"id": "art-1a2b3c4d", "removed": true, "error": null},
  {"id": "art-5e6f7a8b", "removed": false,
   "error": {"code": "artifact_forbidden", "message": "art-5e6f7a8b was created by another workspace tree; ask a person to remove it"}}],
 "warnings": []}
```

The exit code is the first failure's.

## Exit codes

| Situation                                      | Error code             | Exit |
| ---------------------------------------------- | ---------------------- | ---- |
| Run outside a workspace                        | `outside_workspace`    | 2    |
| No artifact with that id                       | `artifact_unknown`     | 3    |
| An artifact another workspace tree created     | `artifact_forbidden`   | 4    |
| Run on an enrolled environment's agent machine | `artifact_unreachable` | 1    |

## Examples

```console
$ ws machine artifact rm art-1a2b3c4d
$ ws machine artifact rm art-1a2b3c4d art-5e6f7a8b
```
