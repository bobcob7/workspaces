# ws cache update

```
ws cache update [--json] (<spec>... | --all)
```

Fetches cached repos from upstream, pruning deleted branches and tags and
following a change of upstream's default branch. Specs must name cached
repos; nothing is cloned. Up to 8 repos are fetched at once, and one
failing doesn't stop the others.

## When to use it

Before `ws claude --offline` or `ws cache search --refs`, to see upstream's
current branches. `ws claude` and `ws shell` fetch the repos they use on
their own.

## Flags

| Flag     | Meaning                                         |
| -------- | ----------------------------------------------- |
| `--all`  | Update every cached repo; not with specs        |
| `--json` | Print the result as JSON                        |

## JSON output

```json
{"repos": [{"id": "github.com/bobcob7/api", "error": null}], "warnings": []}
```

Results keep the order of the repos. `error` is `null` or
`{"code", "message"}`; the exit code is the first failure's. Without
`--json`, `updated  <id>` per repo.

## Exit codes

`usage` for both specs and `--all`, or neither; `spec_invalid`;
`repo_not_found` or `repo_ambiguous` for a spec that names no cached repo,
or several; `fetch_failed` or `git_failed` per repo. See
`ws llm cache`.

## Examples

```console
$ ws cache update --json buff-bear
$ ws cache update --all --json
```
