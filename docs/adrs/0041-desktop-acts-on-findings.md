# 0041. Selected findings are posted as one GitHub review at the reviewed commit after a preview, and sent to fix to the requesting agent, or else to a new agent on the PR's branch

- Status: Proposed
- Date: 2026-10-26

## Context

### Problem

With decisions kept per finding in the review's store ([0040](0040-review-decisions-kept-with-findings.md)),
the app needs two actions: **Comment** puts the chosen findings on the
pull request, and **Fix** gets the chosen findings fixed, with the chosen
suggestion for each. How is each carried out?

### What a solution needs

1. **Nothing reaches GitHub unseen.** Posting is outward-facing and
   notifies people, so a person sees exactly what's posted, under their
   own account, before it is.
2. **Comments land on the right lines**, at the commit that was reviewed.
3. **A fix goes to whoever owns the change**: preferably the agent that
   asked for the review and has the branch checked out.
4. **Done once.** An action already taken shows as done, to the app and to
   agents, and isn't repeated by accident.

### Relevant facts

- GitHub's "Create a review for a pull request" takes `commit_id`
  ("Defaults to the most recent commit in the pull request when you do not
  specify a value"), `event` (`APPROVE`, `REQUEST_CHANGES`, `COMMENT`;
  blank leaves it `PENDING`), and `comments[]` with `path`, `line`,
  `side`, and `start_line`. "This endpoint triggers notifications", and
  "Creating content too quickly using this endpoint may result in
  secondary rate limiting"[^gh-review].
- For a review comment, `line` is "The line of the blob in the pull request
  diff that the comment applies to"; `subject_type` is `line` or `file`,
  and `line` is "Required unless using subject_type:file"[^gh-comment].
- "List pull requests files" returns each file's `filename`, `status`, and
  `patch`, at most 3000 files, up to 100 a page[^gh-files]; for one PR it
  took 0.39–0.54 s[^bench-gh].
- The app already types into an agent's `claude attach` terminal, as a
  bracketed paste for several lines ([0029](0029-desktop-app.md)).
- The review record names the requester, the PR's head branch, and
  whether the PR comes from a fork ([0039](0039-ws-review-command.md)).
  A workspace's `origin` is upstream, so a clone of a fork PR's base repo
  can't push to the fork's branch ([0009](0009-workspace-repository-remotes.md)).

## Decision

**Comment.**

1. **Place each finding.** The backend reads the PR's files with `gh api
   repos/<owner>/<repo>/pulls/<n>/files --paginate` and parses each
   `patch` for the new side's line ranges. A finding's first anchor whose
   lines fall in a hunk becomes a line comment (`side: RIGHT`, `line` =
   `end_line` or `line`, `start_line` when it spans lines); an anchor in a
   changed file but outside the hunks, or a file with no `patch`, becomes a
   file comment (`subject_type: file`); anything else goes in the review
   body.
2. **Preview.** A dialog shows every comment and the body as GitHub will
   render them, editable, with the event (default **Comment**; **Request
   changes** and **Approve** selectable, never preselected). Each comment
   is the finding's band and category, its body, the chosen suggestion if
   any, and a footer with the qualified id. `@name` in agent-written text
   is wrapped in backticks so a finding can't mention anyone. If the
   reviewed commit isn't the PR's current head, the dialog says the
   comments may show as outdated.
3. **Post** with one `gh api --method POST
   repos/<owner>/<repo>/pulls/<n>/reviews --input -`, `commit_id` set to
   the reviewed commit. Success runs `ws review acted … --commented <url>`
   for the posted findings ([0040](0040-review-decisions-kept-with-findings.md));
   failure shows `gh`'s message, posts nothing, and keeps the decisions.

**Fix.** A dialog lists the selected findings and a target:

- **The requester** (default when the record names one whose session
  exists): the app sends a message through the agent's terminal, as the
  reply box does.
- **A new fix agent** otherwise, or by choice: `ws claude --json --name
  fix-<repo>-<n>-<sha7> <cache id>@<head_branch>` for a same-repo PR. For
  a fork PR, the agent starts at the reviewed commit and is told to push
  a `fix/pr-<n>-<sha7>` branch to `origin` instead.

The message (or the new agent's task) is short, because the decisions are
in the store: the findings' qualified ids and titles, the command that
lists them in full with the chosen fixes and notes (`ws review report
<review> --decided fix --json`, or `--repo --ref`), and to push when done
and start a new review with `ws review start --pr <n>`. Sending runs `ws
review acted … --fix-sent <agent>`.

A decision with `done` set shows as done, and is included in a later
action only if the person ticks it again.

### Why this option

1. The preview is the only path to GitHub, it shows the request as sent,
   and it's one review: one notification batch, one call against the
   secondary limit.
2. `commit_id` pins comments to the commit whose anchors loam-refinery
   verified; the patch check keeps a line comment from failing on a line
   that isn't in the diff, and file comments and the body catch the rest.
3. The requester usually wrote the change and has its branch; sending it
   the findings closes the loop 0039 opens. The fix agent covers a review
   started by hand or a requester that's gone.
4. `done`, kept with the decision, makes repeats deliberate, and tells the
   fix agent which findings are already handled.

It's weak in two ways: the hunk parser is a small piece of diff logic the
app owns, and a message typed into the requester's terminal arrives as
user input mid-task, which the requester has to fit in with what it's
doing.

## Alternatives

### Post each finding as its own review comment

**Pros:** a failure affects one comment; simpler payloads.
**Cons:** one notification per finding, and many calls in a row, which the
docs warn may hit the secondary rate limit[^gh-review].

### Create a pending review and let the person submit it on GitHub

Leave `event` blank, so the review stays `PENDING`.

**Pros:** GitHub's own UI is the final preview.
**Cons:** a second place to finish the job; a pending review is invisible
to the app, and the person's other pending review on that PR, if any,
would conflict. Could be an option in the dialog later.

### Always start a new fix agent

**Pros:** one path; never interrupts a working agent.
**Cons:** a second clone of the branch while the requester still has its
own, and two agents that may push to the same branch.

## Consequences

- Comment needs `gh` signed in with write access to the repo; the error
  is shown as `gh` reports it.
- The hunk parser, the placement rules, the `@` escaping, and the request
  body are unit-tested in `reviews::comment` with captured `pulls/files`
  output; the fix message and target choice in `reviews::fix`.
- A fix agent that reads `--decided fix` sees the person's choices
  exactly, rather than a copy pasted into its prompt.
- Agents that read the report themselves (`ws review wait`) can fix
  without the app; Comment stays app-only, so agents never post to GitHub.
- **Unverified:** that GitHub accepts `subject_type: file` comments inside
  a create-review request's `comments[]` (documented for single review
  comments). Check against a test PR before accepting; if not, those go in
  the body.

## References

[^gh-review]: GitHub Docs, REST API, "Create a review for a pull request." <https://docs.github.com/en/rest/pulls/reviews?apiVersion=2022-11-28#create-a-review-for-a-pull-request> (accessed 2026-10-26). Quoted as above; also "By leaving this blank, you set the review action state to PENDING".
[^gh-comment]: GitHub Docs, REST API, "Create a review comment for a pull request." <https://docs.github.com/en/rest/pulls/comments?apiVersion=2022-11-28#create-a-review-comment-for-a-pull-request> (accessed 2026-10-26). Quoted as above.
[^gh-files]: GitHub Docs, REST API, "List pull requests files." <https://docs.github.com/en/rest/pulls/pulls?apiVersion=2022-11-28#list-pull-requests-files> (accessed 2026-10-26). "Responses include a maximum of 3000 files. The paginated response returns 30 files per page by default."
[^bench-gh]: Measured 2026-10-26 on an Apple M4, macOS 26.6.2, gh 2.81.0, over the network to github.com. 3 runs of `/usr/bin/time -p gh api repos/bobcob7/workspaces/pulls/10/files --paginate`; range of `real`. Each file object had `additions`, `blob_url`, `changes`, `contents_url`, `deletions`, `filename`, `patch`, `raw_url`, `sha`, `status`. Network-bound.
