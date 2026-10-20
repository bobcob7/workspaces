# ws cache list

```
ws cache list [--json]
```

Lists cached repos.

## JSON output

```json
{"repos": [{"id": "github.com/bobcob7/buff-bear",
            "path": "/…/cache/repos/github.com/bobcob7/buff-bear.git",
            "upstream": "https://github.com/bobcob7/buff-bear.git",
            "default_branch": "main",
            "last_fetched_at": "2026-10-17T15:48:00Z",
            "size_bytes": 18432000}],
 "warnings": []}
```

Without `--json`, one id per line.

## Exit codes

`usage` for any argument; `git_failed` if a repo can't be read. See
`ws llm cache`.

## Examples

```console
$ ws cache list --json
```
