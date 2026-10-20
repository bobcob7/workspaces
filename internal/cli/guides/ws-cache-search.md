# ws cache search

```
ws cache search [--limit <n>] [--refs] [--json] <query>
```

Case-insensitive substring search over cached repo ids, ranked by match
quality (exact name, then name prefix, then name substring, then owner or
host), then by id. Unlike spec resolution, it never fails on several
matches. Only cached repos are searched.

## When to use it

To find the spec for a repo when you know part of its name, or which
repo has a branch or tag.

## Flags

| Flag          | Meaning                                         |
| ------------- | ----------------------------------------------- |
| `--limit <n>` | At most n results (default 20; at least 1)      |
| `--refs`      | Also search branch and tag names; ref matches follow repo matches |
| `--json`      | Print the result as JSON                        |

## JSON output

```json
{"matches": [{"id": "github.com/bobcob7/buff-bear", "ref": null},
             {"id": "github.com/bobcob7/buff-bear", "ref": "feat/new-thing"}],
 "warnings": []}
```

`ref` is `null` for a repo match. Without `--json`, `<id>` or `<id>@<ref>`
per line, usable as a spec.

## Exit codes

`usage` for no query, more than one, or `--limit` under 1. See
`ws llm cache`.

## Examples

```console
$ ws cache search --json bear
$ ws cache search --refs --json new-thing
```
