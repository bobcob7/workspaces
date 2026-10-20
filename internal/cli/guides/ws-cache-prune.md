# ws cache prune

```
ws cache prune [-n | --dry-run] [--json]
```

Deletes `refs/ws/commits/*` refs that no workspace has checked out. They
are made when a workspace asks for a commit only upstream has, such as a
pull request's. A ref is kept while any workspace's record names its repo
and commit, and every ref is kept while a workspace is being created. If
a workspace record can't be read, nothing is pruned.

## When to use it

Only when told to, to free cache space.

## Flags

| Flag              | Meaning                                  |
| ----------------- | ---------------------------------------- |
| `-n`, `--dry-run` | List what would be deleted; delete nothing |
| `--json`          | Print the result as JSON                 |

## JSON output

```json
{"refs": [{"id": "github.com/bobcob7/buff-bear", "commit": "9b1d22a0c7e1f3a8b2d4c6e8f0a1b3c5d7e9f1a2"}], "warnings": []}
```

Without `--json`, `pruned  <id>  <commit>` per ref, or `would prune` with
`-n`.

## Exit codes

`usage` for any argument; `git_failed` or `internal` when a repo or a
workspace record can't be read. See `ws llm cache`.

## Examples

```console
$ ws cache prune -n --json
```
