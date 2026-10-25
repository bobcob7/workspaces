# ws cache refs

```
ws cache refs [--json] <spec>
```

Lists a cached repo's branches and tags, each with the commit it points at
and that commit's subject, author, and date. Branches come first, the
default branch leading and the rest newest first, then tags newest first.
It reads the cache as it is: run `ws cache update <spec>` first for
upstream's latest. A ref in the spec is ignored.

## When to use it

To pick a branch or tag for `ws claude <repo>@<ref>`, or to see what was
last committed on each branch, without a workspace.

## JSON output

```json
{"id": "github.com/bobcob7/buff-bear",
 "default_branch": "main",
 "refs": [{"name": "main", "kind": "branch",
           "commit": "9c04e2a7…", "subject": "Merge pull request #39",
           "author": "bobcob7", "date": "2026-10-24T09:12:00Z"},
          {"name": "v1.2.0", "kind": "tag", "commit": "…", "subject": "…",
           "author": "…", "date": "…"}],
 "warnings": []}
```

`kind` is `branch` or `tag`. For an annotated tag, `commit`, `subject`,
`author`, and `date` are the tagged commit's. `date` is the committer date,
or `null` if git gives none. Without `--json`, one line per ref: kind,
short commit, name, and subject.

## Exit codes

`usage` without exactly one spec; `repo_not_found` or `repo_ambiguous` if
the spec names no cached repo or several; `git_failed` if the repo can't be
read. See `ws llm cache`.

## Examples

```console
$ ws cache refs --json buff-bear
```
