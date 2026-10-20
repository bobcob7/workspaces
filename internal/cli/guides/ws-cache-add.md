# ws cache add

```
ws cache add [--json] <spec>...
```

Resolves each spec and clones the repo into the cache if it isn't there.
Refs in specs are ignored. Every spec is resolved before anything is
cloned, so one that doesn't resolve fails the command and clones nothing.
A failed clone doesn't stop the others.

## When to use it

To prepare repos ahead of time, for example before `ws claude --offline`.
`ws claude` and `ws shell` clone missing repos on their own.

## JSON output

```json
{"repos": [{"id": "github.com/bobcob7/buff-bear", "path": "/…/cache/repos/github.com/bobcob7/buff-bear.git",
            "upstream": "https://github.com/bobcob7/buff-bear.git", "added": true, "error": null}],
 "warnings": []}
```

`added` is `false` for a repo that was already cached. `error` is `null`
or `{"code", "message"}`; the exit code is the first failure's. Without
`--json`, one id per line.

## Exit codes

See `ws llm cache`: `spec_invalid`, `repo_not_found`, `repo_ambiguous`,
`fetch_failed`, `clone_failed`, `git_failed`, and `usage` without a spec.

## Examples

```console
$ ws cache add --json bobcob7/buff-bear gitlab.example.com/team/svc
```
