# 0032. A pull request review is a `ws claude` agent on the PR's head commit, named after the PR and that commit

- Status: Proposed
- Date: 2026-10-24

## Context

### Problem

The desktop app's Reviews section ([0031](0031-desktop-sections-rail.md))
lists open pull requests in watched repos and offers to start an agent that
reviews one. The app must then show, for each pull request, whether a
review is running, finished, or out of date because the author pushed
since; and Agents must show which agent is reviewing what.

What is a review agent, and how does the app connect it to its pull request
without keeping records of its own?

### What a solution needs

1. **No second source of truth.** The app keeps no state about agents
   ([0029](0029-desktop-app.md)); `ws list` and `claude agents` are the
   record. A review must be found from them.
2. **Reviews the exact code under review**, including pull requests from
   forks, whose branches the cache doesn't have.
3. **Tells a stale review from a current one** after new commits are
   pushed.
4. **Starting the same review twice is harmless**, which matters once
   reviews start automatically.

### Relevant facts

- A repo spec may name a full 40-character commit hash. If the cache
  doesn't have it, `ws` fetches it from upstream into
  `refs/ws/commits/<hash>` and checks it out detached; this was measured
  with a `refs/pull/*/head` commit that only the PR had
  ([0007](0007-repo-spec-syntax-and-resolution.md), footnote
  `bench-fetch-sha`).
- `ws claude --name <name>` derives the workspace id from the name, and
  fails if that id is taken rather than adding a suffix
  ([0012](0012-workspace-layout.md)). The id also names the session
  ([`ws claude`](../tools/ws-claude.md)).
- `ws list --json` gives each workspace's `id` and each repo's `id`, `ref`,
  and `ref_kind` ([`ws list`](../tools/ws-list.md)).
- `gh pr list --json` returns, among others, `number`, `title`, `author`,
  `headRefOid`, `baseRefName`, `isDraft`, `reviewRequests`, `updatedAt`,
  and `url`[^gh-pr-list]. Listing one repo's open PRs with those fields
  took 0.38–0.58 s[^bench-gh].
- Workspace ids are limited to `[a-z0-9-]` and cut to 64 characters
  ([0012](0012-workspace-layout.md), `maxIDLen` in
  `internal/workspace/name.go`).

## Decision

We will start a review as an ordinary agent:

```
ws claude --json --name review-<repo>-<number>-<sha7> <host>/<owner>/<repo>@<headRefOid> -- [--model …] [--permission-mode …] <task>
```

- `<repo>` is the repo's name, sanitized like any workspace name and
  shortened so the whole name fits in 64 characters (so `ws` never cuts
  off the number or commit); `<number>` is the PR number; `<sha7>` the
  first 7 characters of the head commit. The spec uses the full `headRefOid`, so fork PRs work.
- **Finding reviews.** A workspace is a review of PR `N` in repo `R` when
  its id starts with `review-<R name, shortened as above>-<N>-` and it has exactly one repo
  whose id is `R`. Its repo's `ref` is the commit it reviewed.
- **Review status** of a pull request, from the newest matching workspace:
  none is **To review**; one whose agent is working or starting is
  **Reviewing**; one that is idle or stopped is **Reviewed**, and is
  **out of date** if its commit isn't the PR's current `headRefOid`.
  The agent's own status (needs input, done) is shown as in Agents.
- **Starting twice is a no-op.** The same PR at the same commit produces
  the same name; `ws` rejects the second `ws claude`, and the app treats
  that error for a review name as "already started", not as a failure.
  (Amended by [0036](0036-workspace-id-suffix-and-rename.md): `ws` now
  adds a random suffix, so the app checks its listing and its own starts
  in progress instead.)
- **Results** are the agent's job result and links, read as Agents reads
  them ([0029](0029-desktop-app.md)); the Reviews section shows them under
  the pull request.
- **Removing** a review is removing its workspace (`ws rm`), which also
  makes the PR "To review" again.

The Reviews section's other state, which repos are watched, which PRs were
dismissed, and the review settings, is not decided here; the PR that
implements Reviews records it in its own ADR.

### Why this option

1. Everything the app shows about a review is derived from `ws list`; there
   is nothing to keep in sync, and a review started from a terminal with the
   same name shows up too.
2. Checking out `headRefOid` reviews exactly the commit `gh` reported, and
   a full hash is the one form `ws` can fetch for a fork PR.
3. The commit in the name and in the repo's `ref` is what tells a stale
   review apart.
4. Deterministic names make `ws`'s own name check the guard against
   duplicates, which auto-review relies on.

It's weak where names are a convention: a workspace someone names
`review-api-12-…` by hand is taken for a review. That is acceptable for a
tool one person runs, and arguably correct.

## Alternatives

### Keep a review record in the app (PR → session id)

**Pros:** works whatever the workspace is named; could store extra data,
such as when a review was dismissed.
**Cons:** a second record of agents that can disagree with `ws list` when a
workspace is removed outside the app; needs storage the app doesn't have
today.

### Check out the PR's branch (`repo@feature-branch`)

**Pros:** the workspace can push fixes to the branch.
**Cons:** fork branches aren't in the cache, and a branch moves, so the
review no longer says which commit it looked at. A follow-up "fix these
findings" agent can check out the branch instead.

### Let the agent fetch the PR itself (`gh pr checkout`) from the default branch

**Pros:** one spec for every review; `gh` handles forks.
**Cons:** the workspace record then says the default branch, not the PR, so
the app can't tell what was reviewed; and the agent needs network and `gh`
credentials before it starts work.

## Consequences

- Review names are a contract between the app and `ws list`. Unit tests in
  the Reviews section pin the name format and the matching rule.
- Each review is a full workspace with its own clone; with auto-review on,
  busy repos produce many. The Reviews settings cap how many auto-reviews
  run at once, and finished reviews are removed like any other agent.
- `refs/ws/commits/*` refs accumulate for reviewed commits until
  `ws cache prune` ([0007](0007-repo-spec-syntax-and-resolution.md)).
- Repo names over 64 − 16 − (digits in the PR number) characters are
  shortened in review names, so two repos whose names share that prefix
  can only be told apart by the repo id check.
- Only GitHub repos can be reviewed, since pull requests come from `gh`.

## References

[^gh-pr-list]: GitHub CLI 2.81.0, `gh pr list --help`, "JSON FIELDS", accessed 2026-10-24. Lists `additions`, `author`, `baseRefName`, `body`, `changedFiles`, `deletions`, `headRefName`, `headRefOid`, `isCrossRepository`, `isDraft`, `number`, `reviewRequests`, `title`, `updatedAt`, and `url`, among others.
[^bench-gh]: Measured 2026-10-24 on an Apple M4, macOS 26.6.2, gh 2.81.0, over the network to github.com. 3 runs of `/usr/bin/time -p gh pr list --repo bobcob7/workspaces --json number,title,author,headRefOid,isDraft,updatedAt,reviewRequests,additions,deletions,changedFiles,baseRefName,headRefName,url`; range of `real` reported. Network-bound.
