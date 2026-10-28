# ws review

```
ws review start [--pr <n>] [--profile <p>[:<model>]]... [--auto <p>[:<model>]]... [--requester <id>|none] [--name <name>] [--offline] [--json] <spec> [-- <claude args>... [<focus>]]
ws review wait [--timeout <duration>] [--json] <id>
ws review report [--format json|markdown|html] [--decided comment|fix|skip] [--json] (<id> | --repo <repo> --ref <sha>)
ws review list [--repo <repo>] [--pr <n>] [--json]
ws review decide [--json] (<id> | --repo <repo> --ref <sha>) <finding>... (--comment | --fix [--suggestion <n>] | --skip | --clear) [--note <text>]
ws review acted [--json] (<id> | --repo <repo> --ref <sha>) <finding>... (--commented <url> | --fix-sent <agent>)
ws review profiles [--json]
ws review profiles show [--json] <name>
ws review profiles add [--json] <name> --description <d> [--when <w>] [--match <glob>]... (--guide-file <f> | -)
ws review profiles edit [--json] <name> [--description <d>] [--when <w>] [--match <glob>]... [--no-match] [--guide-file <f> | -]
ws review profiles rm [--json] <name>
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
usable. Once every profile has submitted and the review agent is idle,
`ws` removes the review's agent and workspace; `wait_command`, `ws review
report <id>`, and `ws review list` still find it by its id. Never post review findings to the pull request yourself.

**To triage** a finished review, record a decision per finding with
`ws review decide <review> <finding>... --comment|--fix|--skip`; findings
are the report's `<profile>:<id>` ids. A person usually does this in the
desktop app. Each finding's `decision` shows in the report.

**To fix** a review's findings (a fixer, such as an agent the desktop app
sent findings to), read `ws review report --json --decided fix` with the
review it names: each finding to fix, the chosen `suggestion` (an index
into its `suggestions`, or null for your own fix), and the person's
`note`. Skip findings whose `done` is set unless you were asked to redo
them. Fix, push, and start a new review as you were told.

**To review** (reviewers), inside a review workspace; outside one these
fail with `review_not_found`:

- `ws review prime --profile <name>` first: how to write and submit a
  review, and what your profile looks for.
- `ws review describe` for the document's shape, and `--lens` for any
  field, check, or topic it names. A lens is a page of the format's
  manual; a profile is what you look for. They're unrelated.
- `ws review submit --json -` to check your review and store it.

**To manage profiles**, `ws review profiles add`, `edit`, and `rm` write
the files in `$CONFIG_DIR/review/profiles/`. Editing a built-in makes an
override; removing the override restores the built-in. Reviews already
running keep the guides they started with.

Run `ws llm review <op>` for each one's guide.

## Exit codes

| Situation                                        | Error code            | Exit |
| ------------------------------------------------ | --------------------- | ---- |
| An unknown operation, or wrong arguments or flags | `usage`              | 2    |
| The review is still running when `wait` gives up | `review_timeout`      | 1    |
| Not a review workspace                           | `review_not_found`    | 3    |
| `decide`, `acted`: a finding not in the current report | `finding_not_found` | 3  |
| `profiles`: an invalid profile, a taken name, or removing a built-in | `profile_invalid`, `profile_exists`, `profile_builtin` | 2, 4, 4 |

## Examples

```console
$ ws review start --json --pr 42 buff-bear -- "Look hard at the retry loop"
$ ws review wait --json review-buff-bear-42-3f2a9c1-d8efb24f
$ ws review report --json --decided fix review-buff-bear-42-3f2a9c1-d8efb24f
$ ws review decide --json review-buff-bear-42-3f2a9c1-d8efb24f security:dropped-context-1 --fix --suggestion 0
$ ws review prime --profile security
$ ws review submit --json - < review.json
```
