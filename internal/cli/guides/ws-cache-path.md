# ws cache path

```
ws cache path [--json] <spec>
```

Prints the cache directory of a cached repo: a bare clone whose branches
and tags mirror upstream's as of the last fetch.

## When to use it

To read a repo's history without creating a workspace:
`git -C "$(ws cache path buff-bear)" log main`. Don't change
anything in it.

## JSON output

```json
{"id": "github.com/bobcob7/buff-bear", "path": "/…/cache/repos/github.com/bobcob7/buff-bear.git", "warnings": []}
```

## Exit codes

`usage` without exactly one spec; `spec_invalid`; `repo_not_found` or
`repo_ambiguous` when no cached repo, or several, match. See
`ws llm cache`.

## Examples

```console
$ ws cache path --json buff-bear
```
