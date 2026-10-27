# 0039. `ws review` starts a review with a record of what it reviews and who asked, keeps submissions in `CONFIG_DIR/reviews/`, and `wait` hands the requester the combined report

- Status: Proposed
- Date: 2026-10-26

## Context

### Problem

An agent that has opened a pull request should be able to ask for a
review and get the findings back without a person in between, and the
desktop app should start reviews the same way. With reviews now a `ws`
feature ([0037](0037-review-engine-forked-from-loam-refinery.md)) run by
one agent with a subagent per profile
([0038](0038-review-fans-out-to-profile-subagents.md)): what commands do
requesters and reviewers run, what does `ws` keep, and how does the
report get back to the requester?

### What a solution needs

1. **One command set for agents and the app**, under
   [0016](0016-output-contract.md).
2. **The report reaches the requester** when the review is done, without
   polling by hand or reading a transcript.
3. **Records what is reviewed and for whom**: profiles, models, PR, and
   requester can't be derived from a workspace name.
4. **Reviews outlive their workspace**, so a report can be read, and
   acted on, after the review agent is removed.

### Relevant facts

- `ws` keeps per-workspace state in `state/<id>/`, removed with the
  workspace ([0012](0012-workspace-layout.md),
  [0015](0015-workspace-lifetime-follows-session.md)); records are
  written before the workspace is moved into place
  ([0022](0022-workspace-record-before-rename.md)) and carry a version
  ([0027](0027-respawn-lock-and-record-versions.md)).
- `gh pr view <n> --repo <cache id> --json
  number,title,headRefOid,headRefName,baseRefName,isCrossRepository,url`
  took 0.36–0.58 s[^bench-gh].
- Combined reviews are joined by repo and commit; qualified ids are
  `<profile>:<id>` for a profile's current submission and `#<n>:<id>`
  otherwise, and resubmitting under a profile supersedes the earlier
  one[^lr-collect].
- Claude Code: "When Claude Code runs a command in the background, it
  runs the command asynchronously and immediately returns a background
  task ID"; "In a session that runs unattended, such as a `-p` run or a
  cloud session, Claude Code stops a background command at its time
  limit", by default 30 minutes and at most 2 hours[^cc-bg].
- In a `ws claude` session, a background `sleep 150` that the agent
  started with a 60 s `timeout` ran the full 150 s, and when it exited
  Claude Code sent the idle agent a task notification, which it answered
  in a new turn[^bench-bg]. So a `claude --bg` session isn't held to the
  unattended time limit, and a background command's exit wakes it.

## Decision

We will add `ws review` and a guide, `ws llm review`.

**For requesters**

- `ws review start [--json] [--name N] [--pr N] [--profile P[:M]]...
  [--auto P[:M]]... [--requester ID|none] [--offline] <spec> -- [claude
  flags] [focus]`
  - `<spec>` is one repo. With `--pr`, it has no `@ref` and the commit is
    `gh pr view`'s `headRefOid`; without, `@ref` is resolved to a full
    commit. `gh` missing or signed out is `gh_missing` or `gh_auth` (exit
    1); no such PR is `pr_not_found` (exit 3).
  - Name: 0032's `review-<repo>-<n>-<sha7>`, or `review-<repo>-<sha7>`
    without a PR, with [0036](0036-workspace-id-suffix-and-rename.md)'s
    suffix.
  - Requester: the workspace the command runs in, `none` outside one, or
    the id given (`workspace_not_found` if it doesn't exist).
  - Profiles as [0038](0038-review-fans-out-to-profile-subagents.md)
    chooses them; an unknown one is `profile_unknown` (exit 2).
  - Optional `focus` text after `--` is added to the prompt under "From
    the requester".
  - Writes `state/<id>/review.toml`, then starts the agent with 0038's
    `--agents` and prompt, and prints the `ws claude` result plus `review`
    (the record) and `wait_command`.

  ```toml
  version = 1
  repo = "github.com/bobcob7/buff-bear"
  ref = "3f2a9c1…"                 # full commit
  requester = "fix-login-d8efb24f" # "" for none
  started_at = 2026-10-26T18:00:00Z
  profiles = [
    { name = "security", model = "opus", auto = false },
    { name = "go", model = "", auto = true },
  ]
  [pr]                             # absent without --pr
  number = 42
  url = "https://github.com/bobcob7/buff-bear/pull/42"
  base = "main"
  head_branch = "feat/login"
  cross_repository = false
  ```

- `ws review wait [--json] [--timeout D] <id>` polls the review's session
  every 5 s, as `ws list` reads it, until it is idle or stopped, then
  prints what `report` prints. The timeout (default `[review] timeout`, 60
  minutes) is `review_timeout` (exit 1); `wait` again resumes, and returns
  at once for a finished review.
- `ws review report [--json] [--format json|markdown|html] (<id> | --repo R
  --ref SHA)` combines the stored submissions:

  ```json
  {
    "review": { "...": "the record, or null after removal" },
    "agent": { "workspace": "review-buff-bear-42-3f2a9c1-…", "status": "idle" },
    "profiles": [{ "name": "security", "model": "opus", "verdict": "request_changes",
                   "severity": { "max": 9, "must_fix": 1, "...": 0 }, "missing": false }],
    "comments": [{ "id": "security:dropped-context-1", "profile": "security",
                   "priority": 9, "category": "correctness", "body": "…",
                   "anchors": [], "suggestions": [], "decision": null }],
    "superseded": [],
    "warnings": []
  }
  ```

  A finished review with `missing: true` profiles isn't an error; the
  requester decides. `decision` is [0040](0040-review-decisions-kept-with-findings.md)'s.
- `ws review profiles [--json]` lists every profile with its description,
  `when`, `match`, and model, for the app's settings.

**For reviewers** (inside a review workspace; see
[0037](0037-review-engine-forked-from-loam-refinery.md)):
`ws review prime [--profile P]`, `ws review describe [--lens N]`, and `ws
review submit [path|-]`. Outside one, they are `review_not_found` (exit 3).

**The store**, `CONFIG_DIR/reviews/<host>/<owner>/<name>/<sha>/`:
`<profile>.json` for each profile's current submission, earlier ones
renamed `<profile>.<n>.json` when superseded, and `decisions.json`
(0040). Written under a lock per directory, with temp-file-and-rename. It
isn't removed with the workspace; `ws cache prune` removes directories for
repos no longer cached. `docs/state.md` describes it; `report --json` is
the contract.

**Elsewhere.** `ws list --json` gains `review`: the record, or `null`. The
app matches reviews by the record's `pr.number` and `ref`, and by 0032's
name for workspaces without one. The app starts reviews with `ws review
start --json --pr <n> <cache id>` and its own profile settings, and its
"Review task" setting is removed. `config.toml` gains `[review] timeout`
and `[review.profiles.<name>]`. New error codes: `gh_missing`, `gh_auth`,
`review_timeout`, `review_invalid`, `review_unverifiable` (exit 1);
`profile_unknown` (exit 2); `pr_not_found`, `review_not_found` (exit 3).

**Getting the report to the requester.** `ws llm review` tells an agent to
start the review, run `wait_command` as a background command, carry on,
and act on the report when Claude Code tells it the command exited.
`wait`'s own timeout is the only limit; if it fires, the agent runs `wait`
again.

### Why this option

1. Agents and the app run the same commands; prompt, profiles, record,
   and store are written once, in Go.
2. `wait` turns "is it done?" into one command whose exit is the answer,
   costing the requester nothing while it runs in the background.
3. The record holds what names can't, and lives with the workspace; the
   store holds the findings, and outlives it.
4. Files per profile make "current" and "superseded" a file name, and a
   report a directory read; there is no database to keep, since every
   query is by repo and commit.

It's weak in that delivery is pull, not push: a requester that never runs
`wait` never hears back.

## Alternatives

### The review agent messages the requester

**Pros:** push; nothing for the requester to run.
**Cons:** relies on Claude Code's cross-session messaging from a
background session, which `internal/claude` doesn't use and 0024 doesn't
cover; the message would be prose, not the report. Could complement
`wait` later.

### A SQLite store, as loam-refinery has

**Pros:** queries across repos and time ("everything security flagged
this month").
**Cons:** nothing here asks those questions yet; files are what the rest
of `ws`'s state uses. Can be added beside the files if they do.

### Keep reviews in the review workspace's state

**Pros:** removed with the workspace, no new directory.
**Cons:** removing a finished review agent would lose the findings and
decisions while the PR is still open.

## Consequences

- Amends [0014](0014-command-set.md) (command set), [0016](0016-output-contract.md)
  (error codes, under existing exit codes), [0012](0012-workspace-layout.md)
  (`review.toml` in state, `reviews/` beside `cache/`), and
  [0032](0032-review-agent-per-pr-head.md) (matching may use the record).
- `docs/tools/ws-review.md`, `docs/state.md`, and `docs/config.md` are
  written with the implementation; `ws llm`'s coverage test covers the
  new guide.
- Conformance: unit tests for spec and `--pr` handling with a fake `gh`,
  the requester default, the record's round trip, the store's supersede
  rule and locking, `wait` against a fake `claude` session going working →
  idle, the timeout, and `missing`.
- Delivery by background `wait` was checked in a `ws claude`
  session[^bench-bg]. A requester run with `claude
  -p` *is* unattended, so its background `wait` stops after 30 minutes;
  `ws llm review` says to pass a `timeout` up to 2 hours there, or run
  `wait` again.

## References

[^bench-gh]: Measured 2026-10-26 on an Apple M4, macOS 26.6.2, gh 2.81.0, over the network to github.com. 3 runs of `/usr/bin/time -p gh pr view 10 --repo github.com/bobcob7/workspaces --json number,title,headRefOid,headRefName,baseRefName,isCrossRepository,url`; range of `real`. Network-bound.
[^lr-collect]: loam-refinery `describe --lens=collect-reviews`, built from f7b52c0, run 2026-10-26. "Each comment's id is qualified to stay unique: `profile:origin_id` when its submission is current for that profile, `#ordinal:origin_id` otherwise — no profile claimed, or superseded." "Resubmitting under the same profile supersedes".
[^cc-bg]: Claude Code docs, "Interactive mode", Background Bash commands. <https://code.claude.com/docs/en/interactive-mode> (accessed 2026-10-26). Quoted as above. Limits from "Tools reference", Time limit for background commands, <https://code.claude.com/docs/en/tools-reference> (accessed 2026-10-27): "A command that Claude starts in the background gets 30 minutes, or the `timeout` Claude passes with `run_in_background`, up to a maximum of 2 hours"; "A local session you work in from a terminal, the desktop app, or the VS Code extension has no time limit on background commands."
[^bench-bg]: Measured 2026-10-27 on an Apple M4, macOS 26.6.2, Claude Code 2.1.311, in the `ws claude` session of 0038's `bench-agents`. The agent ran `sleep 150; echo finished-ok` with `run_in_background: true` and `timeout: 60000` at 14:13:42Z and ended its turn; the transcript shows a `task-notification` at 14:16:12Z and the agent's reply "BG-RESULT: Background command "sleep 150; echo finished-ok" completed (exit code 0)" at 14:16:14Z. One run.
