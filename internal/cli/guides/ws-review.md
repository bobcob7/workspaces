# ws review

```
ws review start [--pr <n>] [--profile <p>[:<model>]]... [--auto <p>[:<model>]]... [--requester <id>|none] [--name <name>] [--offline] [--json] <spec> [-- <claude args>... [<focus>]]
ws review wait [--timeout <duration>] [--json] <id>
ws review report [--format json|markdown|html] [--json] (<id> | --repo <repo> --ref <sha>)
ws review profiles [--json]
ws review prime [--profile <name>]
ws review describe [--lens <name>,... | --list]
ws review submit [--json] [<path> | -]
```

Structured reviews: a review is a JSON document with addressable,
prioritized findings, each anchored to a file and line at the reviewed
commit and checked against it. A review workspace reviews one commit of one
repo with one or more profiles (`security`, `tests`, `go`, …): one review
agent starts a reviewer subagent per profile, each submits its own review,
and the submissions are kept in `$CONFIG_DIR/reviews/`, outside the
workspace.

## When to use it

**To get your work reviewed** (requesters), after you've pushed a pull
request:

1. `ws review start --json --pr <n> <repo>` starts the review and returns
   at once. Its result has `wait_command`.
2. Run `wait_command` as a **background command** (`run_in_background`),
   then carry on or end your turn. Don't poll, sleep, or read the review
   agent's transcript.
3. When Claude Code tells you the command exited, read its output: the
   report, with each profile's verdict and every finding. Act on it: fix
   what's worth fixing, push, and say what you skipped and why.
4. Exit 1 with `review_timeout` means the review is still running: run
   `wait_command` again, in the background.

In a session that runs unattended, such as `claude -p`, Claude Code stops
a background command after 30 minutes unless you pass a `timeout`. There,
pass a `timeout` of up to 2 hours (7200000 ms) with `run_in_background`,
or run `wait_command` again when it's stopped. An interactive or
`claude --bg` session has no such limit.

A profile with `"missing": true` didn't submit; the review is still
usable. Never post review findings to the pull request yourself.

**To review** (reviewers), inside a review workspace; outside one these
fail with `review_not_found`:

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
| The review is still running when `wait` gives up | `review_timeout`      | 1    |
| Not a review workspace                           | `review_not_found`    | 3    |

## Examples

```console
$ ws review start --json --pr 42 buff-bear -- "Look hard at the retry loop"
$ ws review wait --json review-buff-bear-42-3f2a9c1-d8efb24f
$ ws review prime --profile security
$ ws review submit --json - < review.json
```
