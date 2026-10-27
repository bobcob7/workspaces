# ws review prime

```
ws review prime [--profile <name>]
```

Prints how to write a review and submit it, in a few hundred tokens, for
a reviewer's prompt. With `--profile`, the profile's guide follows: what
that reviewer looks hardest at, and what it shouldn't report. The profile
must be one of the review's.

Profiles are built in (`architecture`, `claims`, `compatibility`,
`efficiency`, `go`, `security`, `tests`, `typescript`); a file
`$CONFIG_DIR/review/profiles/<name>.md` adds one or replaces a built-in.

Prints text, not a result: `--json` is refused.

## Exit codes

| Situation                                     | Error code         | Exit |
| --------------------------------------------- | ------------------ | ---- |
| Arguments, or `--json`                        | `usage`            | 2    |
| The profile isn't one of the review's, or doesn't exist | `profile_unknown` | 2 |
| The profile's file can't be read or parsed    | `config_invalid`   | 1    |
| Not inside a review workspace                 | `review_not_found` | 3    |

## Examples

```console
$ ws review prime --profile security
```
