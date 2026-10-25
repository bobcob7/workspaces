# Repos section: design

List and manage the repos in `ws`'s cache
([ADR 0008](../../adrs/0008-repository-cache.md)) and their branches, and
start work on any of them. Shared frame and rules:
[README.md](README.md).

![A cached repo with its branches](img/repos.png)

The section as built, with sample data: [desktop/docs/repos.png](../../../desktop/docs/repos.png).

## List

- One row per cached repo, grouped by `host/owner` (heading with count),
  sorted by name within a group, groups sorted by id.
- Row: repo name, last-fetched age on the right; summary `default branch ·
  N branches · N workspaces` (workspace count omitted when 0; a repo over
  100 MB shows its size instead of the workspace count).
- Small icons after the name, from Reviews: an eye when its pull requests
  are watched, a bolt when auto-review is on. Hidden until Reviews ships
  (see [Slots for Reviews](#slots-for-reviews)).
- A filter box at the top matches repo ids as `ws cache search` does
  (substring, case-insensitive).
- Top bar: `N repos · total size` (muted), **Fetch all**
  (`ws cache update --all`), **Add repo** (contained).

## Detail

Header:

- Title: the id, with `host/owner/` muted and the name bold.
- Actions: **Fetch** (`ws cache update <id>`), **New agent** and **New
  shell** (open the existing dialogs with this repo's spec filled in),
  **Remove** (red; confirms).
- Meta: upstream URL (mono), `default <branch>`, size, `fetched <age>`.
  The cache path is shown on hover of the upstream and copied on click.

Body, top to bottom:

1. **Pull request reviews** card (slot owned by Reviews, see
   [reviews.md](reviews.md) and [Slots for Reviews](#slots-for-reviews)): Watch and Auto-review switches, open PR count,
   last check, **Show in Reviews**. Shown only for GitHub repos.
2. **Tabs**: Branches (default), Tags, Workspaces, each with a count, and a
   filter box on the right.
   - **Branches**: Branch (mono; the default branch first, with a
     `default` chip), Last commit (subject), Author, Updated (age), and
     Workspaces (chips of workspace ids that have this branch checked out;
     clicking one opens it in Agents). Hovering a row replaces the chips
     with **New agent** and **New shell** for `repo@branch`.
   - **Tags**: Tag, commit subject, date, sorted newest first; the same
     Workspaces column and hover actions.
   - **Workspaces**: the workspaces with this repo, as Agents rows; click to
     open in Agents.

While a fetch runs, the Fetch button shows a spinner and the meta's age
reads "fetching…"; the branch list refreshes when it ends. A fetch error
shows as an Alert above the tabs with `ws`'s message.

## Add repo

![Adding a repo](img/repos-add.png)

- One field: repo spec or URL, as `ws cache add` accepts
  ([0007](../../adrs/0007-repo-spec-syntax-and-resolution.md)).
- As you type (debounced ~400 ms), `ws cache resolve --json` previews the
  id and how it was found (`source`: spec, cache, default, probe), and
  whether it's already cached. Ambiguous names list the candidates to pick
  from; `repo_not_found` shows under the field and disables **Add repo**.
- A **Watch pull requests in Reviews** switch, shown only once Reviews
  exists and the repo is on GitHub; on by default.
- **Add repo** runs `ws cache add --json`; the dialog stays open with a
  progress bar while it clones, then selects the new repo.

## Remove repo

![Removing a repo](img/repos-remove.png)

Confirm dialog saying what happens, from
[`ws cache remove`](../../tools/ws-cache.md#remove): the cached copy and
its size are deleted; the listed workspaces keep working; the next
workspace that needs the repo clones it again; Reviews stops watching it.
**Remove** is red.

## Empty and error states

- No cached repos: "Repos you use in workspaces are cached here so new
  workspaces start fast. Add one to get started." with **Add repo**.
- `ws` missing or failing: the same Alert as Agents.

## Decisions

Settled by the Repos PR:

1. **Listing branches and tags**: a new `ws cache refs <spec> --json`
   (kind, name, commit, subject, author, date; the default branch first,
   then newest first; tags newest first), and `branches` and `tags` counts
   in `ws cache list --json` for the list rows. The app never runs git
   itself. [ADR 0033](../../adrs/0033-ws-cache-refs.md) amends
   [0014](../../adrs/0014-command-set.md)'s command set.
2. **Which workspaces use a branch**: the app joins `ws list --json`'s
   repos with the refs, matching the repo `id`, `ref` (the name), and
   `ref_kind` (`branch` or `tag`). `ws` records the ref a workspace was
   created on, so a workspace that switched branches since still shows
   under the one it started on. Sessions outside a workspace are left out.
3. **When to read the cache**: `ws cache list` when the Repos section is
   entered and after Fetch, Fetch all, Add, and Remove; `ws cache refs`
   when a repo is selected and after it's fetched. Neither runs on the
   4 s agent poll. Workspace counts and chips come from the agent poll
   that's already running.
4. **Branch actions beyond starting work** stay out, as above.

Smaller choices the PR made:

- Starting an agent or shell from Repos switches to Agents and selects the
  new workspace once it's created, as starting one from Agents does.
- The Tags tab has the same columns as Branches, without Author, and its
  rows also list the workspaces on a tag.
- ↑/↓ move the selection while focus is in the repo list.

## Slots for Reviews

Repos renders what the Reviews section exports, and nothing in its place
until then (`features/repos/reviewsSlot.ts`). Any module in
`features/reviews/` may export, by name:

| Export                  | Props                          | Where |
| ----------------------- | ------------------------------ | ----- |
| `RepoReviewsCard`       | `repoId`                       | top of the repo detail, above the alerts and tabs |
| `RepoReviewsIcons`      | `repoId`                       | after the repo name in the list (eye, bolt) |
| `AddRepoWatchSwitch`    | `repoId \| null`, `checked`, `onChange` | in the Add repo dialog, under the info text; on by default |
| `RemoveRepoReviewsNote` | `repoId`                       | a paragraph in the Remove dialog, such as "Pull requests for this repo stop being checked, and it's removed from Reviews." |

Repos owns adding and removing; Reviews follows them in its own slice:
`addRepo.fulfilled` carries `meta.arg.watch` (set only when
`AddRepoWatchSwitch` exists), and `removeRepo.fulfilled` carries the
removed id as `meta.arg`. Both are exported from
`features/repos/reposSlice.ts`.
