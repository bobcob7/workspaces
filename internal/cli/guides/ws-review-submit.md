# ws review submit

```
ws review submit [--json] [<path> | -]
```

Checks a review document, from a file or stdin, and stores it. The review
workspace's record supplies the repo and commit: leave `ref` out and it's
filled in, or give the reviewed commit. `profile` must be one of the
review's profiles. Every anchor is verified against the workspace's repo
at that commit.

A stored review is `$CONFIG_DIR/reviews/<host>/<owner>/<name>/<sha>/<profile>.json`.
Submitting again under the same profile supersedes the earlier one, which
is kept as `<profile>.<n>.json`.

## JSON output

```json
{"workspace": "review-buff-bear-42-3f2a9c1-d8efb24f", "repo": "github.com/bobcob7/buff-bear", "ref": "3f2a9c1…", "profile": "security",
 "path": "/…/reviews/github.com/bobcob7/buff-bear/3f2a9c1…/security.json", "superseded": null,
 "valid": true, "verification": {"source": "repo", "reason": null, "anchors": 3, "verified": 3},
 "counts": {"comments": 2, "errors": 0, "advisories": 1, "skipped": 0},
 "diagnostics": [{"severity": "advisory", "name": "body-thin", "comment": "a-1", "path": "/comments/0/body", "message": "…"}],
 "skipped": [], "lenses": ["body-thin"], "warnings": []}
```

`diagnostics` holds advisories: notes on review quality that never fail
the run. `lenses` names the `ws review describe --lens` entries that
explain them.

## Exit codes

| Situation                                         | Error code            | Exit |
| ------------------------------------------------- | --------------------- | ---- |
| More than one path, or the file can't be read     | `usage`               | 2    |
| The review has errors; revise it and resubmit     | `review_invalid`      | 1    |
| The anchors can't be checked: the checkout diverged from the commit, or lacks it | `review_unverifiable` | 1 |
| Not inside a review workspace                     | `review_not_found`    | 3    |
| The review couldn't be stored                     | `internal`            | 1    |

For `review_invalid` and `review_unverifiable`, `details` holds
`diagnostics` (each with `severity`, `name`, `comment`, `path`, and
`message`), `lenses`, `verification`, `counts`, and `skipped`.

## Examples

```console
$ ws review submit --json review.json
$ ws review submit --json - < review.json
```
