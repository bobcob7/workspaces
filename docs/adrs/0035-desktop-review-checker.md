# 0035. The desktop app checks watched repos for pull requests in its Rust backend, with `gh pr list --repo <cache id>`, four at a time, and starts auto-reviews there

- Status: Proposed
- Date: 2026-10-24

## Context

### Problem

The Reviews section ([design](../design/desktop-sections/reviews.md))
lists the open pull requests of watched repos, refreshed every few minutes
and on **Check now**, and starts review agents
([0032](0032-review-agent-per-pr-head.md)) for new ones in auto-review
repos. Where does that checking run, how is `gh` invoked, and how does it
behave when GitHub limits it?

### What a solution needs

1. **Keeps running while the app is open**, whatever section is shown, and
   doesn't run twice at once (a doubled check could start a review twice
   while the first `ws claude` is still running).
2. **Uses the user's `gh`**: their install, their login, their hosts,
   including GitHub Enterprise.
3. **Cheap**: a check of a handful of repos takes seconds and a small part of
   GitHub's rate limit, and backs off when limited.
4. **Testable** without GitHub.

### Relevant facts

- `gh pr list --repo` takes `[HOST/]OWNER/REPO`[^gh-repo]; cache ids are
  `host/owner/name` ([0008](0008-repository-cache.md)), so a cache id is a
  valid `--repo` for any host `gh` is logged in to.
- `gh auth status --json hosts` lists each host `gh` is logged in to, with
  its `login` and `state`; it took 0.27–0.35 s[^bench-gh]. `gh api user`
  took 0.29–0.34 s.
- `gh pr list --state open --limit 100 --json …` with the fields Reviews
  shows (including `body`) took 0.38–0.59 s per repo, and raised the
  GraphQL `used` count by at most 1 point per call[^bench-gh]. Adding
  `commits` fails: "By the time this query traverses to the authors
  connection, it is requesting up to 1,000,000 possible nodes which exceeds
  the maximum limit of 500,000"[^bench-gh].
- `gh api repos/<owner>/<repo>/compare/<a>...<b>` returns `ahead_by` and the
  commits between two SHAs, in 0.49–0.67 s, against the REST limit[^bench-gh].
- GitHub's GraphQL API allows "5,000 points per hour per user". Over the
  limit, "the response status will still be `200`, but you will receive an
  error message"; for secondary limits without `retry-after`, "wait at least
  one minute", and on repeated failures increase the wait
  exponentially[^gh-limits].
- A repo that doesn't exist fails with exit 1 and `GraphQL: Could not resolve
  to a Repository with the name 'bobcob7/nonexistent-xyz'.` on
  stderr[^bench-gh].
- The app already runs `ws` and `claude` from Rust with the login shell's
  PATH (`desktop/src-tauri/src/env.rs`), and polls `ws list` from the
  frontend every 4 s ([0029](0029-desktop-app.md)). The frontend runs under
  React's `StrictMode`, which mounts effects twice in development
  (`desktop/src/main.tsx`).
- `ws claude --name` fails with `workspace_exists` when the id is taken
  ([`ws` tools README](../tools/README.md#creating-a-workspace)).
- `ws cache list --json` took 0.04–0.07 s with 3 cached repos[^bench-gh].

## Decision

We will check from a task in the Rust backend, started with the app:

- **Schedule.** The task wakes every 30 s and checks when the interval from
  Settings (default 5 minutes) has passed since the last check started, or at
  once on **Check now** or when a repo is newly watched. One check runs at a
  time; a Check now during a check is folded into it.
- **Each check:**
  1. `ws cache list --json`, so the Watched repos dialog lists the cache
     even when `gh` can't run.
  2. `gh auth status --json hosts`. `gh` missing is `gh_missing`; no host
     with `state: success` is `gh_auth`. Either stops the check and shows as
     the Alert in the pane, with what to run. The watched repos are the
     cached ones with `watch` on whose host is `github.com` or a host `gh`
     is logged in to; others are "GitHub repos only".
  3. For each watched repo, four at a time: `gh pr list --repo <cache id>
     --state open --limit 100 --json
     number,title,author,url,body,isDraft,headRefOid,headRefName,baseRefName,additions,deletions,changedFiles,reviewRequests,createdAt,updatedAt`.
     A failure keeps that repo's last result and records the first line of
     `gh`'s stderr as its warning.
  4. `ws list --json` (with `claude agents`, as the Agents poll does) to
     match reviews ([0032](0032-review-agent-per-pr-head.md)). For each
     reviewed pull request that's out of date, `gh api
     repos/<owner>/<repo>/compare/<reviewed>...<head>` once per pair, cached
     for the app's life, gives the count and subjects of the new commits.
  5. Auto-review (below).
- **Environment.** `gh` runs with the login shell's PATH, no stdin, and
  `GH_PROMPT_DISABLED=1`, `NO_COLOR=1`. "You" is the `login` from step 1 for
  the repo's host.
- **Rate limits.** A check where any `gh` error mentions "rate limit"
  doubles the wait before the next scheduled one (interval × 2, × 4, …, at
  most 60 minutes); a check without one resets it. Check now always runs.
- **Auto-review.** For each open pull request in a repo with auto-review on,
  oldest update first: skip it if dismissed at its head, if a review
  workspace already has its head, if an auto-review was already started at
  its head (even if you've since removed it), or if the rules in Settings
  exclude it (draft, opened by you, your review not requested). Start the
  rest with `ws claude` until the number of auto-started reviews whose agent
  is working or starting reaches the limit (default 2); the others wait for
  the next check. `workspace_exists` counts as started. Each start is
  recorded in `desktop.json` ([0034](0034-desktop-state-file.md)).
- **The frontend reads the result** with a `reviews_view` command on its
  4 s poll. The backend joins the last check with the last agent listing
  (which `list_entries` keeps) into groups, so 0032's name and matching
  rules live only in Rust.

### Why this option

1. A backend task outlives section switches and webview reloads, and a single
   task can't overlap itself; a frontend timer would run twice under
   `StrictMode` in development and again after each hot reload.
2. `gh` with the login shell's PATH is the user's own `gh`; passing the cache
   id as `--repo` carries the host, so Enterprise repos work with no extra
   setting.
3. Ten repos at four at a time take about three `gh` calls' time, a couple of
   seconds; at the default interval that's 120 GraphQL points an hour of
   5,000. The compare calls are made once per new pair, not every check.
4. The scheduling and auto-review decisions are plain functions over parsed
   `gh` and `ws` output, tested with `cargo test`; `gh` itself is faked with a
   script on PATH, as the `ws` tests already do.

It's weak in two ways. The 30 s wake means a changed interval takes effect
up to 30 s late. And the list's age is the pull request's `updatedAt`, which
also moves on comments, not the time of the last push: `gh pr list` can't
afford `commits` (above), so "pushed" ages aren't shown.

## Alternatives

### Check in the frontend, like the agent poll

**Pros:** one language for Reviews; state in Redux from the start; the same
pattern as `fetchEntries`.
**Cons:** `StrictMode` and hot reload double timers in development; auto-
review would start agents from whichever webview instance is alive, and 0032's
matching would have to be written in TypeScript as well as wherever else it's
needed. Whether a hidden window's timers are throttled is **unverified**.

### One `gh api graphql` query for all watched repos

**Pros:** one call per check; could ask for exactly the fields needed,
including the head commit's date.
**Cons:** a hand-written query that `gh` doesn't maintain; one failing repo
fails the whole query; and hosts differ per repo, so Enterprise repos need a
query each anyway. Could replace step 3 later if checks get slow.

### Webhooks or `gh` notifications instead of polling

**Pros:** immediate; no wasted calls.
**Cons:** webhooks need a public endpoint and admin rights on each repo; the
notifications API only covers repos you're subscribed to and needs its own
read state.

## Consequences

- `gh` becomes a requirement for Reviews (not for the rest of the app).
  `gh_missing` and `gh_auth` say what to run.
- The backend now runs `ws list` on its own once per check, besides the 4 s
  poll.
- Auto-review can start agents while you're in another section, or not
  looking; the limit bounds how many run at once, not how many are created
  over a day.
- Conformance: `cargo test` covers the `gh` arguments, parsing, error
  classification, the backoff, the cap, each auto-review rule, and that a
  removed auto-review isn't started again (`reviews::checker`,
  `reviews::auto`).
- **Unverified:** the exact wording of `gh`'s rate-limit errors; the match is
  on "rate limit" anywhere in stderr, case-insensitive.

## References

[^gh-repo]: GitHub CLI 2.81.0, `gh pr list --help`, accessed 2026-10-24. "-R, --repo [HOST/]OWNER/REPO   Select another repository using the [HOST/]OWNER/REPO format".
[^gh-limits]: GitHub Docs, "Rate limits and query limits for the GraphQL API." <https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api> (accessed 2026-10-24). "5,000 points per hour per user"; "Individual calls cannot request more than 500,000 total nodes"; "If you exceed your primary rate limit, the response status will still be `200`, but you will receive an error message"; for a secondary limit without `retry-after`, wait "at least one minute", and on repeated failures increase the time between retries exponentially.
[^bench-gh]: Measured 2026-10-24 on an Apple M4, macOS 26.6.2, gh 2.81.0, over the network to github.com, against bobcob7/workspaces with 2 open PRs. 3 runs each, range of `real` from `/usr/bin/time -p`: `gh pr list --repo github.com/bobcob7/workspaces --state open --limit 100 --json <the fields above>`; `gh auth status --json hosts`; `gh api user --jq .login`; `gh api repos/bobcob7/workspaces/compare/7f4a0c0...31d9429`; `ws cache list --json` (3 repos, local). GraphQL points: `gh api rate_limit --jq .resources.graphql.used` before and after each `gh pr list` (5→5, 5→6, 6→6). Adding `commits` to the `--json` fields failed with the node-limit error quoted (1 run; GitHub computes the node count from the query, not the data). The nonexistent-repo error is from `gh pr list --repo github.com/bobcob7/nonexistent-xyz --json number`. Network-bound.
