# 0039. `ws review` starts a review with a record of what it reviews and who asked, `wait` hands the requester the combined report, and `report` reads it at any time

- Status: Proposed
- Date: 2026-10-26

## Context

### Problem

An agent that has opened a pull request should be able to ask for a
review and get the findings back, without a person in between. The
desktop app should start reviews the same way, so the prompt and checks
live in one place. With [0037](0037-loam-refinery-as-external-binary.md)
and [0038](0038-review-fans-out-to-profile-subagents.md), a review is a
`ws claude` agent whose results are in loam-refinery's store. What
command does a requester run, what does `ws` keep about the review, and
how does the report get back to the requester?

### What a solution needs

1. **One command for agents and the app**, with `--json` output under
   [0016](0016-output-contract.md).
2. **The report reaches the requester** when the review is done, without
   it polling by hand or reading a transcript.
3. **Records what was reviewed and for whom**, since the profiles and the
   requester can't be derived from a workspace name.
4. **Readable later**, including after the review workspace is removed.

### Relevant facts

- `ws` keeps per-workspace state in `state/<id>/` and removes it with the
  workspace ([0012](0012-workspace-layout.md),
  [0015](0015-workspace-lifetime-follows-session.md)); records are written
  before the workspace is moved into place
  ([0022](0022-workspace-record-before-rename.md)) and carry a version
  ([0027](0027-respawn-lock-and-record-versions.md)).
- `gh pr view <n> --repo <cache id> --json
  number,title,headRefOid,headRefName,baseRefName,isCrossRepository,url`
  took 0.36–0.58 s[^bench-gh].
- The review store outlives workspaces; `collect-reviews --repo
  <cache id> --ref <sha>` reads it from anywhere
  ([0037](0037-loam-refinery-as-external-binary.md)).
- Claude Code: "When Claude Code runs a command in the background, it runs
  the command asynchronously and immediately returns a background task ID.
  Claude Code can respond to new prompts while the command continues
  executing in the background"[^cc-bg]. In "a session that runs
  unattended", background commands have a time limit[^cc-bg].

## Decision

We will add `ws review` with three subcommands, and a guide, `ws llm
review`.

**`ws review start [--json] [--name N] [--pr N] [--profile P]...
[--requester ID|none] [--offline] <spec> -- [--model M]
[--permission-mode M] [extra instructions]`**

- `<spec>` is one repo. With `--pr`, it has no `@ref`, and the commit is
  `gh pr view`'s `headRefOid`; without, `@ref` is resolved to a full
  commit as `ws claude` does. `gh` missing or signed out is `gh_missing` or
  `gh_auth` (exit 1); no such PR is `pr_not_found` (exit 3).
- The name is 0032's (`review-<repo>-<n>-<sha7>`), or `review-<repo>-<sha7>`
  without a PR, with [0036](0036-workspace-id-suffix-and-rename.md)'s
  suffix.
- The **requester** defaults to the workspace the command runs in (the
  one whose directory contains the current directory), `none` outside one.
  An id that doesn't exist is `workspace_not_found`.
- Before the workspace is moved into place, `ws` writes
  `state/<id>/review.toml`:

  ```toml
  version = 1
  repo = "github.com/bobcob7/buff-bear"
  ref = "3f2a9c1…"            # full commit
  profiles = ["architecture", "claims", "security", "tests"]
  requester = "fix-login-d8efb24f"   # "" for none
  started_at = 2026-10-26T18:00:00Z

  [pr]                         # absent without --pr
  number = 42
  url = "https://github.com/bobcob7/buff-bear/pull/42"
  base = "main"
  head_branch = "feat/login"
  cross_repository = false
  ```

- Then it starts the agent with 0038's prompt, and prints the `ws claude`
  result plus `review` (the record) and `wait_command` (`ws review wait
  <id>`).

**`ws review wait [--json] [--timeout D] <id>`** polls the review's
session every 5 s, as `ws list` reads it, until it is idle or stopped,
then prints what `report` prints. The timeout (default `[review] timeout`,
60 minutes) is `review_timeout` (exit 1); running `wait` again resumes, and
returns at once for a finished review.

**`ws review report [--json] [--format json|markdown|html] (<id> | --repo R
--ref SHA)`** runs `collect-reviews` ([0037](0037-loam-refinery-as-external-binary.md)).
With `--json`, it prints:

```json
{
  "review": { "...": "the record, or null with --repo/--ref" },
  "agent": { "status": "idle", "workspace": "review-buff-bear-42-3f2a9c1-…" },
  "missing_profiles": ["security"],
  "report": { "...": "collect-reviews output, verbatim" },
  "warnings": []
}
```

`missing_profiles` is the record's profiles with no current (not
superseded) submission. A finished review with missing profiles isn't an
error: the requester decides. `--format markdown|html` prints
loam-refinery's own rendering, unchanged.

**Elsewhere.** `ws list --json` gains `review`: the record, or `null`. The
app matches reviews by the record's `pr.number` and `ref` when there is
one, and by 0032's name otherwise. The app starts reviews with `ws review
start --json --pr <n> <cache id>`, and its "Review task" setting becomes
"Extra instructions". `config.toml` gains `[review] profiles` and
`timeout`. New error codes: `gh_missing`, `gh_auth`, `review_timeout`,
`refinery_missing`, `refinery_version`, `refinery_failed` (exit 1);
`profile_unknown` (exit 2); `pr_not_found` (exit 3).

**Getting the report to the requester.** `ws llm review` tells an agent
to start the review, run `wait_command` as a background command, carry on,
and act on the report when it arrives; and before finishing its own task,
to run `wait` once more in the foreground if it hasn't seen the result.

### Why this option

1. The app and agents run the same command; the prompt, the profile check,
   and the record are written once, in Go, with tests.
2. `wait` turns "is it done?" into one command whose exit is the answer;
   as a background command it costs the requester nothing while it runs.
3. The record holds what names can't: profiles, requester, PR. It lives
   and dies with the workspace, like the session record.
4. `report --repo --ref` reads the store directly, so a removed review, or
   one submitted by hand, can still be read.

It's weak in that delivery is pull, not push: a requester that never runs
`wait` never hears back, and the requester link is only as good as "the
workspace you ran it from".

## Alternatives

### The review agent messages the requester

The prompt would tell the review agent to send the report to the requester
session when done.

**Pros:** push; nothing for the requester to run.
**Cons:** depends on Claude Code's cross-session messaging from inside a
background session, which `internal/claude` doesn't use and 0024 doesn't
cover; the message is prose the review agent writes, not the report. Could
complement `wait` later.

### Keep it in the app only

**Pros:** no new `ws` command or record.
**Cons:** agents can't request reviews; the prompt and checks would be
Rust-only; and the record would be app state about agents, which 0032
avoids.

### `ws claude` with a `--review` flag

**Pros:** no new command.
**Cons:** `wait` and `report` don't fit under `ws claude`, and the flag
would change what `ws claude`'s positional arguments mean.

## Consequences

- Amends [0014](0014-command-set.md) (command set), [0016](0016-output-contract.md)
  (error codes, under existing exit codes), [0012](0012-workspace-layout.md)
  (`review.toml` in state), and [0032](0032-review-agent-per-pr-head.md)
  (matching may use the record).
- `docs/tools/ws-review.md`, `docs/state.md`, and `docs/config.md` are
  written with the implementation; `ws llm`'s coverage test covers the new
  guide.
- Conformance: unit tests for spec and `--pr` handling with a fake `gh`,
  the requester default, the record's round trip and version check, `wait`
  against a fake `claude` whose session goes working → idle, the timeout,
  and `missing_profiles`.
- **Unverified:** whether a background command in a `claude --bg` session
  counts as unattended and is stopped at a time limit, and whether its
  exit prompts the agent. `wait`'s timeout and the "run it once more"
  rule are the fallback; check with a review started from a `ws claude`
  agent before accepting.

## References

[^bench-gh]: Measured 2026-10-26 on an Apple M4, macOS 26.6.2, gh 2.81.0, over the network to github.com. 3 runs of `/usr/bin/time -p gh pr view 10 --repo github.com/bobcob7/workspaces --json number,title,headRefOid,headRefName,baseRefName,isCrossRepository,url`; range of `real`. Network-bound.
[^cc-bg]: Claude Code docs, "Interactive mode", Background Bash commands. <https://code.claude.com/docs/en/interactive-mode> (accessed 2026-10-26). Quoted as above; and "Background commands in a local session you work in from a terminal, the desktop app, or the VS Code extension have no time limit. In a session that runs unattended, such as a `-p` run or a cloud session, Claude Code stops a background command at its time limit".
