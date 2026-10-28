# 0045. A finished review's workspace and agent are removed; its record is kept in the review store, where `ws review` and the desktop app find it

- Status: Proposed
- Date: 2026-10-28

## Context

### Problem

Every review is an agent in its own workspace
([0032](0032-review-agent-per-pr-head.md), [0039](0039-ws-review-command.md)).
Once its reviewers have submitted, the agent sits idle and its workspace,
a full checkout, stays until someone removes it. Auto-review
([0035](0035-desktop-review-checker.md)) makes several a day. The
findings already live outside the workspace, in `CONFIG_DIR/reviews/`.
When can a review's agent and workspace go, and how do the commands and
the app that read reviews keep working once they have?

### What a solution needs

1. **Finished reviews are removed without anyone asking.**
2. **Nothing a person still needs goes**: a review that's still running,
   or stopped without its reports, stays.
3. **Everything that reads a review still works** after removal:
   `ws review wait <id>` and `report <id>`, the requester's report, the
   desktop app's Reviewed group, and acting on findings
   ([0040](0040-review-decisions-kept-with-findings.md),
   [0041](0041-desktop-acts-on-findings.md)).
4. **Can be turned off**, for someone who wants to read review agents'
   conversations.

### Relevant facts

- A review's submissions are kept in
  `reviews/<host>/<owner>/<name>/<sha>/<profile>.json`, which nothing
  removes with a workspace; `ws review report --repo R --ref SHA` reads
  them with no workspace (`docs/state.md`, [0039](0039-ws-review-command.md)).
- The record of what was reviewed and who asked, `review.toml`, is only
  in `state/<id>/`, which goes with the workspace ([0039](0039-ws-review-command.md)).
- `ws review wait <id>` and `report <id>` find a review through its
  workspace, and fail with `review_not_found` once it's gone
  (`internal/cli/review_report.go`).
- `wait` counts a review finished when its agent is idle or stopped, or
  its workspace is gone. `report` marks a profile `missing` when it has
  no current submission (`internal/cli/review_report.go`).
- The desktop app puts a pull request in Reviewed, and shows its report,
  only while a review workspace for it is listed
  (`desktop/src-tauri/src/reviews/view.rs`). Auto-review doesn't start a
  review again at a head it already reviewed, by its own records in
  `desktop.json` ([0034](0034-desktop-state-file.md)).
- [0044](0044-workspace-trees.md) makes a review a child of its
  requester and removes descendants with their parent.

## Decision

We will remove a review's workspace, and its agent, once the review is
complete, and keep each review's record in the review store so readers
don't need the workspace.

**Complete** means the review agent is idle or stopped and every profile
the review chose has a current submission (none `missing`). A review
whose agent is working, or that is missing a report, is kept, so a
person can see why; it still goes with its parent (0044) or `ws rm`.

**Removal.** The removal pass, which already runs before commands
([0026](0026-removal-pass-timing.md)), removes complete reviews the way
`ws rm` does: `claude stop`, `claude rm`, then the workspace, and its
descendants first (0044). `[review] remove_finished = false` in
`config.toml` turns it off; it defaults to `true`.

**The record in the store.** `ws review start` writes the review's
record, the same `review.toml`, also to
`reviews/<host>/<owner>/<name>/<sha>/runs/<workspace id>.toml`, before
the agent starts. It's never removed with the workspace; `ws cache
prune` removes it with the rest of the repo's reviews.

**Reading a removed review.**

- `ws review wait <id>` and `report <id>` look in `runs/` when no
  workspace has that id; a review found there is finished, its `agent`
  `null`, and `wait` returns at once.
- New `ws review list [--repo R] [--pr N] --json` lists every review run
  in the store, newest first: `id`, the `review` record, and `agent`:
  `{"workspace", "status"}` while its workspace exists, `null` once it's
  removed. Its guide is in `ws llm review list`.
- The desktop app reads `ws review list --json` with each check and
  matches runs to pull requests by `review.pr.number`, as it matches
  workspaces now, so a reviewed pull request stays in Reviewed with its
  report. A run without a workspace shows "agent removed" instead of
  **Open agent** and **Remove**.

### Why this option

1. The removal pass is where `ws` already decides workspaces are done,
   and it runs often, so finished reviews go within a minute of the next
   command.
2. Requiring every report means a review missing one stays visible, and
   one still running is never removed. Requesters lose nothing: their
   report is built from the store.
3. Putting the record next to the submissions makes the store complete
   on its own: the id, the requester, and the pull request survive the
   workspace, which `wait`, `report`, and the app need.
4. `remove_finished` keeps the old behaviour for anyone who wants it.

Where it's weak: the review agent's conversation is gone once it's
removed, which was the only way to see how a reviewer reached a finding.
And the app now reads two sources for reviews, workspaces and the
store, and must treat a run in both as one.

## Alternatives

### Remove when `ws review wait` returns

The requester's `wait` removes the review once it has the report.

**Pros:** removal follows delivery, and nothing removes a report nobody
has read.
**Cons:** reviews started from the app, or without a requester, have no
`wait`, and those are the ones that pile up.

### Keep finished reviews for a grace period

Remove a complete review a fixed time (an hour, say) after it finished.

**Pros:** its conversation can be read for a while.
**Cons:** another setting and timestamp to keep; the report, which is
what's read, doesn't need the workspace. Could be added later as
`remove_finished = "1h"`.

### Hide finished reviews instead of removing them

**Pros:** nothing lost.
**Cons:** [0043](0043-hidden-workspaces.md) already hides reviews; disk
and Claude Code's session list still fill up.

## Consequences

- Amends [0039](0039-ws-review-command.md) (`runs/` in the store, `ws
  review list`, `wait` and `report` by a removed id) and
  [0015](0015-workspace-lifetime-follows-session.md) (a workspace also
  goes when it's a complete review).
- `docs/state.md`, `docs/config.md`, `docs/tools/ws-review.md`, and `ws
  llm review` describe it; the app's change is in
  [docs/design/workspace-trees](../design/workspace-trees/README.md).
- Conformance: Go tests that the removal pass removes a complete review,
  keeps a working one and one missing a report, and keeps all with
  `remove_finished = false`; that `ws review start` writes `runs/`; that
  `wait` and `report` by id work after removal; and that `ws review list`
  lists runs with and without workspaces. Cargo tests that the Reviews
  view keeps a reviewed pull request whose workspace is gone, and that
  auto-review doesn't start it again.
- **Unverified:** that a review agent goes idle, not stopped, right after
  its last subagent submits, so it's removed at the next pass; checked
  with a real review before acceptance.
