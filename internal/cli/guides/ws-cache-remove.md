# ws cache remove

```
ws cache remove [--json] <spec>...
```

Deletes cached repos. Existing workspaces aren't affected. Every spec
must name a cached repo before anything is deleted; a failed removal
doesn't stop the others. Removing a repo that another `ws` is using waits
for it.

## When to use it

Only when told to. The next workspace that uses the repo clones it again.

## JSON output

```json
{"repos": [{"id": "github.com/bobcob7/buff-bear", "path": "/…/cache/repos/github.com/bobcob7/buff-bear.git", "error": null}],
 "warnings": []}
```

`error` is `null` or `{"code", "message"}`; the exit code is the first
failure's. Without `--json`, one id per line.

## Exit codes

`usage` without a spec; `spec_invalid`; `repo_not_found` or
`repo_ambiguous` when a spec names no cached repo, or several. See
`ws llm cache`.

## Examples

```console
$ ws cache remove --json gitlab.example.com/team/svc
```
