# ws review

```
ws review prime [--profile <name>]
ws review describe [--lens <name>,... | --list]
ws review submit [--json] [<path> | -]
```

Structured reviews: a review is a JSON document with addressable,
prioritized findings, each anchored to a file and line at the reviewed
commit and checked against it. A review workspace reviews one commit of one
repo with one or more profiles (`security`, `tests`, `go`, …); each profile
submits its own review, and the submissions are kept in
`$CONFIG_DIR/reviews/`, outside the workspace.

## When to use it

These commands are for reviewers, run inside a review workspace. Outside
one they fail with `review_not_found`.

- `ws review prime --profile <name>` first: how to write and submit a
  review, and what your profile looks for.
- `ws review describe` for the document's shape, and `--lens` for any
  field, check, or topic it names. A lens is a page of the format's
  manual; a profile is what you look for. They're unrelated.
- `ws review submit --json -` to check your review and store it.

Run `ws llm review <op>` for each one's guide.

## Exit codes

| Situation                                        | Error code            | Exit |
| ------------------------------------------------ | --------------------- | ---- |
| An unknown operation, or wrong arguments or flags | `usage`              | 2    |
| Not inside a review workspace                    | `review_not_found`    | 3    |

## Examples

```console
$ ws review prime --profile security
$ ws review describe --lens=priority
$ ws review submit --json - < review.json
```
