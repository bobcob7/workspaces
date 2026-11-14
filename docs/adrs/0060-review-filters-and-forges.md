# 0060. Reviews checks each host through its forge (GitHub through `gh`, Forgejo through its REST API), shows a merge status per pull request, and filters by author and by you

- Status: Proposed; the host setting (`desktop.json` `settings.forges`, forge rule 1) superseded by [0061](0061-hosts-in-config-toml.md)
- Date: 2026-11-13

## Context

### Problem

The Reviews section ([0035](0035-desktop-review-checker.md)) lists open
pull requests only for repos on hosts `gh` knows, and only says whether
each one has been reviewed. People also run Forgejo (or Gitea) hosts beside
github.com and GitHub Enterprise, and want to:

- see pull requests from all of them in one list;
- filter that list by author, and by "me": pull requests I opened, or whose
  review was requested from me;
- see at a glance whether each pull request can merge, and if not, why:
  draft, conflicting, failing checks, behind its base, awaiting approval.

How does the desktop backend reach a Forgejo host, how does it know which
forge a host runs, who "me" is there, and how does it reduce each forge's
pull request fields to one merge status?

### What a solution needs

1. **Several hosts side by side**: github.com, GitHub Enterprise hosts and
   Forgejo hosts in one check, where a failing host (signed out, down)
   doesn't hide the others.
2. **Keeps today's GitHub behaviour**: `gh` with the user's login, cache ids
   as `--repo` ([0035](0035-desktop-review-checker.md)); existing
   `desktop.json` files load and keep working unchanged
   ([0034](0034-desktop-state-file.md)).
3. **No secret in `desktop.json` or in the frontend**, and a token works
   both in the desktop app (opened from Finder, without the shell's
   environment) and in Workspace Online (configured by environment,
   [0053](0053-workspace-online.md)).
4. **One merge status per pull request**, the same meaning on both forges
   wherever the forges allow it, computed in Rust and tested with recorded
   JSON.
5. **Cheap**: a check stays a few seconds and a small share of each host's
   limits; no new program to install.

### Relevant facts

- `gh pr list --json` offers `mergeable`, `mergeStateStatus`,
  `reviewDecision` and `statusCheckRollup` beside the fields asked for
  today[^gh-fields]. Adding those four took 2.9–4.2 s instead of 1.2–1.7 s
  for 30 pull requests, and cost 1 GraphQL point either way; with
  `--limit 100` it took 6.1 s[^bench-gh]. One of the runs at `--limit 100`
  failed with `HTTP 502: 502 Bad Gateway (https://api.github.com/graphql)`[^bench-gh].
- `statusCheckRollup` items are `CheckRun`s (`status`, `conclusion`) or
  `StatusContext`s (`state`); for merged pull requests `mergeable` and
  `mergeStateStatus` were `UNKNOWN`[^bench-gh]. GitHub computes
  mergeability in the background: "If the value is `null`, then GitHub has
  started a background job to compute the mergeability"[^gh-mergeable].
  `mergeStateStatus` is `BEHIND` when "The head ref is out of date",
  `BLOCKED` when "The merge is blocked", with no reason given, and `DIRTY`
  when "The merge commit cannot be cleanly created"[^gh-mergestate].
  Whether GitHub reports `BEHIND` for a branch whose base doesn't require
  it to be up to date is **unverified**.
- A Forgejo host answers `GET /api/v1/version` with
  `{"version":"17.0.0-100-2254cb0+gitea-1.22.0"}`; github.com answers that
  path with 410 and api.github.com with 404[^bench-forgejo].
- Forgejo's `GET /api/v1/repos/{owner}/{repo}/pulls?state=open` returns
  `draft`, `mergeable` (a boolean), `merge_base`, `base.sha`, `head.sha`,
  `user.login` and `requested_reviewers[].login`; asked for `limit=100`, it
  returned 50[^bench-forgejo], the server's page cap (`MAX_RESPONSE_ITEMS`,
  default 50[^forgejo-config]). It has no review decision and no check
  summary: those come from `GET …/commits/{sha}/status` (a combined `state`
  and `statuses[].status`, which included `skipped`) and
  `GET …/pulls/{n}/reviews` (`state`, `stale`, `official`,
  `dismissed`)[^bench-forgejo].
- Anonymous `GET /api/v1/user` and `GET …/branch_protections` return 401
  `token is required`[^bench-forgejo]. Forgejo accepts an access token in an
  `Authorization: token <token>` header[^forgejo-auth].
- The desktop app takes only `PATH` from the login shell
  (`desktop/src-tauri/src/env.rs`); other variables from the shell's rc
  files don't reach an app opened from Finder. `reqwest` 0.13 with rustls
  is already a dependency, behind the `online` feature
  (`desktop/src-tauri/Cargo.toml`).
- In the `ws` Go code, `gh` runs only through `internal/gh`
  ([0039](0039-ws-review-command.md)); in the desktop backend it runs only
  through `reviews::github` ([0035](0035-desktop-review-checker.md)).

## Decision

We will put a small forge layer under the checker. Each **host** (the first
part of a cache id) has one **forge kind**, `github` or `forgejo`:

- **GitHub** stays as it is: `gh`, run only from `reviews::github`, with
  four more `--json` fields (`mergeable,mergeStateStatus,reviewDecision,statusCheckRollup`).
- **Forgejo** (and Gitea, which shares the API) is read over HTTPS from
  `https://<host>/api/v1`, only from `reviews::forgejo`, with `reqwest`
  (made a regular dependency) behind an `Http` trait that tests replace.
  Per watched repo and check: the open pull requests (pages of 50, at most
  100), the branch protections once, and per pull request its head's
  combined status and its reviews, four requests at a time.

**Which forge a host runs**, first match wins:

1. The host's setting in `desktop.json`, `settings.forges.hosts.<host>.kind`
   (superseded by [0061](0061-hosts-in-config-toml.md): now `kind` in
   `config.toml`'s `[[hosts]]`)
   (`github` or `forgejo`); a missing entry means `auto`.
2. `github.com`, or a host `gh auth status` lists: `github`.
3. `GET https://<host>/api/v1/version` answers 200 with a JSON `version`:
   `forgejo`. The answer is kept for the app's run; a network error isn't,
   so the next check asks again.
4. Otherwise none: the repo can't be watched, and the Watched repos dialog
   says to choose the forge in Settings.

**Tokens** for Forgejo hosts, first found wins: the environment variable
`WS_FORGEJO_TOKEN_<HOST>` (the host upper-cased, every character other than
a letter or digit replaced with `_`, so `git.example.com:3000` is
`WS_FORGEJO_TOKEN_GIT_EXAMPLE_COM_3000`), then `forge-tokens.json` beside
`desktop.json`, written by Settings with mode 0600. The backend never sends
a token to the frontend; the view says only where it came from (`env`,
`file`, or none). Without a token, public repos are still listed.

**"Me"** is the login for the host: `gh auth status`'s login on GitHub
hosts, as today, and `GET /api/v1/user`'s `login` on Forgejo hosts with a
token. With no login, nothing on that host counts as mine or requested of
me, and the filter says so.

**Errors are per host.** The view gains a `hosts` list, each with its
forge, login, token source and error: `gh_missing` or `gh_auth` (for GitHub
hosts), `forge_auth` (a 401 or 403 from Forgejo), `forge_rate_limited` (a
429), `forge_failed` (anything else), or `forge_unknown` (no forge found
for a watched host). A failing host keeps its repos' last results, as a
failing repo does today. The view's top-level `error` keeps its meaning for
existing users: it's set for `gh_missing` and `gh_auth` only when every
watched repo is on a GitHub host.

**Merge status.** Each pull request gets `merge: {status, reasons, checks}`.
`reasons` lists every status that applies, in this precedence order, and
`status` is the first of them, or `ready` when none applies:

| Status | GitHub (`gh pr list`) | Forgejo (REST) |
| --- | --- | --- |
| `draft` | `isDraft`, or `mergeStateStatus` `DRAFT` | `draft` |
| `conflicting` | `mergeable` `CONFLICTING`, or `mergeStateStatus` `DIRTY` | `mergeable` false |
| `failing` | a check run concluded `FAILURE`, `TIMED_OUT`, `CANCELLED`, `ACTION_REQUIRED` or `STARTUP_FAILURE`, or a status context is `FAILURE` or `ERROR` | a status is `failure` or `error` |
| `behind` | `mergeStateStatus` `BEHIND` | the base's protection has `block_on_outdated_branch` and `merge_base` isn't `base.sha` |
| `changesRequested` | `reviewDecision` `CHANGES_REQUESTED` | a reviewer's latest official, current, undismissed review is `REQUEST_CHANGES` |
| `awaitingApproval` | `reviewDecision` `REVIEW_REQUIRED` | fewer such `APPROVED` reviews than the protection's `required_approvals` (1 when the protections can't be read) |
| `blocked` | `mergeStateStatus` `BLOCKED` and none of `failing`, `pending`, `changesRequested`, `awaitingApproval` | never |
| `pending` | a check run not `COMPLETED`, or a status context `PENDING` or `EXPECTED` | a status is `pending` |
| `unknown` | `mergeable` `UNKNOWN` | never |
| `ready` | none of the above | none of the above |

`checks` counts the checks: `total`, `passed`, `failed`, `pending`.

**Starting a review** of a Forgejo pull request can't use
`ws review start --pr`, which reads the pull request through `gh`
([0039](0039-ws-review-command.md)). The app starts it at the head commit
instead, `ws review start --json --name <0032 name> <repo>@<head sha>`, and
matches the review to its pull request by that name, as it does for
workspaces without a record ([0032](0032-review-agent-per-pr-head.md)).

**Filters** are frontend state: the search box as today, plus an author,
an "involves me" choice (anyone, mine or requested, mine, requested of me),
and a merge status. They aren't saved.

The [reference](../design/review-filters/reference.md) has the JSON shapes
and commands; [tasks](../design/review-filters/tasks.md) splits the work.

### Why this option

1. Each host is resolved and checked on its own, so a signed-out `gh`
   leaves Forgejo hosts listed, and the other way round.
2. GitHub keeps `gh`, its login and its Enterprise support; only the field
   list grows. Old `desktop.json` files have no `forges` key and load as
   all `auto`, which gives today's result for every GitHub host.
3. Tokens live outside `desktop.json` and never reach the webview. The
   environment variable serves Workspace Online, the file serves an app
   opened from Finder.
4. One Rust function maps both forges' signals to the same statuses, and
   is tested against JSON recorded from each.
5. GitHub stays one call per repo, at the cost measured above. Forgejo is
   two calls per pull request; Forgejo has no GraphQL to fold them into.

It's weak in three ways. Forgejo's `mergeable` is a plain boolean, so a
pull request whose mergeability is still being computed shows as
conflicting until the next check (**unverified**, see Consequences).
`behind` means "must update" on GitHub but is only known on Forgejo when
the protections can be read. And the GitHub check takes about twice as long
per repo.

## Alternatives

### The `tea` or `fj` command-line clients for Forgejo

**Pros:** the same shell-out-and-fake-script pattern as `gh`; they keep
their own logins.
**Cons:** another program to install on the Mac and in the Workspace
Online image ([0055](0055-slim-online-runtime-image.md)); their JSON output
covers pull request lists but not combined statuses or reviews, so the REST
calls would be needed anyway (**unverified** for the latest releases).

### Move forge access into `ws` (`ws forge prs --json`), through `internal/gh` and a new `internal/forgejo`

**Pros:** one place for forge access; CLI users and agents would get merge
status too; matches the Go rule that `gh` runs only in `internal/gh`.
**Cons:** a new `ws` command only the app calls ([0014](0014-command-set.md));
the checker, backoff and auto-review already live in Rust
([0035](0035-desktop-review-checker.md)), and would split across two
programs. It could come later, with the Rust layer calling `ws` instead.

### Tokens in `desktop.json`

**Pros:** one file; Settings writes it already.
**Cons:** `desktop.json` is shown, copied and backed up as plain settings,
and `settings_get` returns its settings to the webview.

### The git credential helper (`git credential fill`) as the token source

**Pros:** someone who pushes to the host over HTTPS already has a
credential there.
**Cons:** that credential may be a password, not a token; reading it
silently for another purpose surprises people; with no helper, git may
prompt. Could be added later as an opt-in third source.

### GitHub's `mergeStateStatus` alone

**Pros:** one field; GitHub's own verdict.
**Cons:** `BLOCKED` covers failing checks, missing approvals and other
rules alike, so it can't say why; and Forgejo has nothing like it.

## Consequences

- Reviews can watch repos on Forgejo hosts. Posting findings
  ([0041](0041-desktop-acts-on-findings.md)) stays GitHub-only:
  `review_comment_plan` fails with `forge_unsupported` for other forges.
- A Forgejo pull request whose head commit isn't in the cache (one from a
  fork) can't be reviewed yet: `ws review start` fails to resolve the
  commit. Teaching `ws review start --pr` about Forgejo is a follow-up.
- `reqwest` becomes a dependency of the desktop build, not only of
  `ws-online`.
- `desktop.json` gains `settings.forges`, still version 1: a missing key
  is `auto`. An older app that rewrites the file drops `forges`, as it does
  any key it doesn't know ([0034](0034-desktop-state-file.md)).
- A new file, `forge-tokens.json`, mode 0600, beside `desktop.json`.
- Conformance: `cargo test` covers the merge-status table and precedence
  (`reviews::merge`), the `gh` fields and their mapping with recorded JSON
  (`reviews::github`), the Forgejo requests, paging, auth and mapping with
  recorded JSON and a fake `Http` (`reviews::forgejo`), host resolution and
  token lookup (`reviews::forge`, `reviews::tokens`), and per-host errors in
  a check (`reviews::checker`). `npm test` covers the filters and the badge.
- **Unverified:** whether Forgejo reports `mergeable: false` while it is
  still computing it; whether a token without admin rights can read
  `branch_protections` (when it can't, `required_approvals` is taken as 1
  and `behind` isn't reported); whether GitHub reports `BEHIND` only when
  the base requires an up-to-date branch; whether `statusCheckRollup` stays within
  GitHub's node limit for repos with many checks per pull request. Each is
  checked against a live host before this ADR is accepted, with the
  `#[ignore]`d `check_live` test.

## References

[^gh-fields]: GitHub CLI 2.104.0, `gh pr list --help`, "JSON FIELDS", accessed 2026-11-13: "…isDraft, labels, latestReviews, maintainerCanModify, mergeCommit, mergeStateStatus, mergeable, … reviewDecision, reviewRequests, reviews, state, statusCheckRollup, title, updatedAt, url".
[^bench-gh]: Measured 2026-11-13 in a Linux arm64 pod, gh 2.104.0, over the network to github.com, against `github.com/bobcob7/workspaces`. 3 runs each of `gh pr list --repo github.com/bobcob7/workspaces --state all --limit 30 --json <fields>`, wall time from `date +%s%N`: the 15 fields of 0035 took 1.17–1.71 s; with `mergeable,mergeStateStatus,reviewDecision,statusCheckRollup` added, 2.89–4.24 s. GraphQL points from `gh api rate_limit --jq .resources.graphql.used` before and after each run: 0–1 per call in both cases. One run of `--limit 100` with all 19 fields: 6.05 s; an earlier one failed with the 502 quoted. Field shapes from `--limit 3 --json number,state,mergeable,mergeStateStatus,reviewDecision,statusCheckRollup`. Network-bound.
[^gh-mergeable]: GitHub REST API docs, "Get a pull request." <https://docs.github.com/en/rest/pulls/pulls#get-a-pull-request> (accessed 2026-11-13). "The value of the `mergeable` attribute can be `true`, `false`, or `null`. If the value is `null`, then GitHub has started a background job to compute the mergeability."
[^gh-mergestate]: GitHub's public GraphQL schema, enum `MergeStateStatus`, as mirrored in octokit/graphql-schema `schema.graphql`, `main` (accessed 2026-11-13; <https://github.com/octokit/graphql-schema>; also rendered at <https://docs.github.com/en/graphql/reference/enums#mergestatestatus>). `BEHIND`: "The head ref is out of date." `BLOCKED`: "The merge is blocked." `DIRTY`: "The merge commit cannot be cleanly created." `DRAFT` (deprecated): "The merge is blocked due to the pull request being a draft." `UNKNOWN`: "The state cannot currently be determined."
[^bench-forgejo]: Measured 2026-11-13 with curl against codeberg.org (Forgejo `17.0.0-100-2254cb0+gitea-1.22.0`), anonymous: `GET /api/v1/version` and `GET /api/forgejo/v1/version`; `GET https://github.com/api/v1/version` (410) and `https://api.github.com/api/v1/version` (404); `GET /api/v1/repos/forgejo/forgejo/pulls?state=open&limit=2` (keys listed above, `X-Total-Count: 187`, a `Link` header with `rel="next"`); the same with `limit=100` returned 50 items; `GET /api/v1/repos/forgejo/forgejo/commits/8db6637666e157ddd3af5d5033ac7edc347f2c10/status` (`state: pending`, 18 statuses, values included `skipped` and `pending`); `GET /api/v1/repos/forgejo/forgejo/pulls/15262/reviews` (keys `state`, `stale`, `official`, `dismissed`, `commit_id`, `user`); `GET /api/v1/user` and `GET /api/v1/repos/forgejo/forgejo/branch_protections` returned 401 `{"message":"token is required"}`. A page of 50 pull requests took 6.3–8.3 s and one combined status 0.6–1.0 s (3 runs each).
[^forgejo-config]: Forgejo docs, "Configuration Cheat Sheet", section `[api]`. <https://forgejo.org/docs/latest/admin/config-cheat-sheet/#api-api> (accessed 2026-11-13). "MAX_RESPONSE_ITEMS: 50: Maximum number of items in a page."
[^forgejo-auth]: Forgejo docs, "API Usage", Authentication. <https://forgejo.org/docs/latest/user/api-usage/> (accessed 2026-11-13). "`Authorization: token 65eaa9c8ef52460d22a93307fe0aee76289dc675`" as one of the accepted ways to pass an access token.
