# Reviews section: design

Check the cached GitHub repos you choose for open pull requests, and
review any of them with an agent in one click, or automatically. Shared
frame and rules: [README.md](README.md). What a review agent is and how
the app finds it: [ADR 0032](../../adrs/0032-review-agent-per-pr-head.md).

![A pull request waiting for review](img/reviews.png)

## Watching repos

- Pull requests are read only for **watched** repos. A repo is watched
  from its Repos detail card, the Add repo dialog, or the **Watched repos**
  dialog in this section's top bar.
- **Auto-review** is a second, per-repo switch, only available on a
  watched repo, off by default.
- Only GitHub repos can be watched; others are listed in the dialog as
  "GitHub repos only".

![Choosing watched repos](img/reviews-watched.png)

## Checking

- Every 5 minutes by default (Settings), and on **Check now**, the app runs
  `gh pr list --repo <owner/name> --state open --json …` for each watched
  repo, a few at a time.
- The top bar says `N repos watched · checked <age>`; while a check runs,
  a small spinner. A repo whose check fails keeps its last result and shows
  a warning row in the list: "api: gh: HTTP 404" and so on.
- `gh` missing or signed out is an Alert in the pane saying what to run
  (`brew install gh`, `gh auth login`).
- Checking keeps running while the app is open, whatever section is shown.

## List

Rows are pull requests: `#<number> <title>`, age on the right (since the
last push), summary `repo · author · <extra>`, and a status dot.

| Group       | When ([0032](../../adrs/0032-review-agent-per-pr-head.md))    | Dot   | Extra in summary                         |
| ----------- | ------------------------------------------------------------- | ----- | ---------------------------------------- |
| To review   | no review workspace                                           | amber | `review requested`, `draft`              |
| Reviewing   | review agent working or starting                              | blue  | `agent working`, `needs input`           |
| Reviewed    | review agent idle or stopped                                  | green, or yellow if out of date | `N new commits`, the agent's one-line result |
| Dismissed   | dismissed by you, and no new commits since                    | grey  | `draft`                                  |

- Within a group, newest first. Dismissed is collapsed by default.
- The rail badge counts **To review** plus out-of-date **Reviewed**.
- A filter box matches number, title, author, and repo.
- Closed or merged pull requests drop out on the next check; their review
  workspaces stay in Agents until removed.

## Detail

Header: title; review status; **Open on GitHub**, **Dismiss** (or
**Restore** when dismissed), and the primary action, **Review** / **Review
again**. Meta: `owner/repo#N` (link chip), author and age, `head → base`
(mono), the short head commit, `+adds −dels in N files`, and a chip when
your review is requested.

Body:

- **To review**: the PR description (Markdown, as the conversation renders
  it) and a **Review will run** card showing the `ws claude` command, with
  **Edit before starting**.
- **Reviewing / Reviewed**: a card for the review agent: its name and
  status dot, the commit it reviewed and when, **Open agent** (switches to
  Agents and selects it), **Remove** (`ws rm`). Inside, the agent's result
  (green-edged block) and its reply, as Agents shows a finished agent.
  More than one review (one per pushed commit) lists the newest first,
  older ones collapsed.
- **Out of date**: a warning strip naming the commits pushed since the
  review, and **Review again**.

![A review that's out of date](img/reviews-reviewed.png)

## Starting a review

**Review** starts it with no dialog, using the Settings defaults.
**Edit before starting** opens the dialog first:

![Starting a review](img/reviews-start.png)

- Workspace name (ADR 0032's name; editable, but changing it means the
  app won't connect the agent to the pull request; say so under the
  field).
- Repo: `id@<sha7>` chip, read-only.
- Task: the review template from Settings with the PR's fields filled in.
- Model and permission mode, defaulting to Settings.
- **Start review** runs `ws claude --json`, then selects the pull request,
  now under Reviewing.

## Auto-review

- On each check, a pull request in an auto-review repo starts a review if
  it has no review at its current head commit, isn't dismissed, and passes
  the rules in Settings (skip drafts, skip your own, only when your review
  is requested).
- At most N auto-reviews run at once (Settings, default 2); the rest wait
  for the next check. Reviews you start by hand don't count against it.
- A pull request reviewed automatically shows "auto" next to its agent's
  name.
- New commits on an auto-reviewed PR start a new review at the new head,
  under the same rules.

## Empty states

![No watched repos](img/reviews-empty.png)

- No watched repos: what Reviews does, and **Choose repos to watch**.
- Watched repos, no open pull requests: "No open pull requests in N
  watched repos. Checked <age>."

## What the PR needs to decide

For the implementing PR, each lasting one in an ADR:

1. **Where watch, auto-review, dismissed PRs, and settings are kept.** The
   app keeps no state today
   ([0029](../../adrs/0029-desktop-app.md)). Options: a `desktop.json` in
   `CONFIG_DIR` (next to `ws`'s files, but not `ws`'s format), the Tauri
   app-data directory, or new keys in `ws`'s `config.toml` with a `ws`
   command to set them. Dismissals must survive a restart; they're keyed
   by repo, number, and head commit so a push un-dismisses.
2. **How `gh` is run**: the login-shell PATH as for `ws`
   ([desktop/README](../../../desktop/README.md)); which host (`--repo
   host/owner/name` for GitHub Enterprise); concurrency; backing off on
   rate-limit errors.
3. **Where checking runs**: in the Rust backend on a timer (keeps running
   while the webview is busy) or in the frontend like the agent poll.
4. **What "done" means for a review agent**: the job result
   ([0029](../../adrs/0029-desktop-app.md)) is the summary shown; whether
   the template should ask the agent to end with a fixed format so the
   app can show a findings count.
5. **Tests** pinning ADR 0032's name format and matching, auto-review
   rules, and the cap.
