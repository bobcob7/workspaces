# 0040. The desktop app renders a review's findings itself from `ws review report --json`, and keeps a decision per finding in `desktop.json`, keyed by pull request, commit, and qualified id

- Status: Proposed
- Date: 2026-10-26

## Context

### Problem

A finished review is now a combined loam-refinery report
([0039](0039-ws-review-command.md)): submissions per profile, and comments
with an id, priority, category, body, anchors, and suggestions. In the
Reviews section, a person should read it comfortably and choose, finding
by finding, what to post as a comment, what to fix and how, and what to
skip ([0041](0041-desktop-acts-on-findings.md) acts on those choices).
How does the app show the report, and where are the choices kept?

### What a solution needs

1. **Selection is part of the view**: choosing an item and a suggestion
   happens where the finding is shown, and survives switching sections and
   restarting the app.
2. **A choice names exactly one finding, durably**, so it can't attach to
   a different finding after a resubmission.
3. **One source of truth for findings**: the store, through `ws`; the app
   keeps only decisions.
4. **Looks like the rest of the app**, in both themes.

### Relevant facts

- Qualified ids are `<profile>:<id>` for a profile's current submission,
  and `#<ordinal>:<id>` otherwise; "`ordinal` is positional and shifts on
  the next submission; treat a `#N:id` form as good for one call
  only"[^lr-collect].
- loam-refinery's HTML report is one self-contained page with a single
  static script that reads only `data-*` attributes the template
  wrote[^lr-html], about 22 KB for two short reviews
  ([0037](0037-loam-refinery-as-external-binary.md)).
- The app's webview CSP is `default-src 'self'; style-src 'self'
  'unsafe-inline'; …`, with no `script-src` exception
  (`desktop/src-tauri/tauri.conf.json`). The frontend already renders
  Markdown with `react-markdown` and `remark-gfm`, and uses MUI
  (`desktop/package.json`).
- `desktop.json` is written only by the backend, a missing key takes its
  default, and records for pull requests that closed or moved are pruned
  after each successful check ([0034](0034-desktop-state-file.md)).

## Decision

We will render findings as React components from `ws review report --json`
and keep decisions in `desktop.json`.

- **Reading.** A backend command `review_report(workspace)` runs `ws review
  report --json <id>` (or `--repo --ref` for a review whose workspace is
  gone) and caches the result by repo, commit, and the report's `total`.
  The pane asks for it when a review is selected, and again when its agent
  goes from working to idle.
- **Showing.** Findings grouped by priority band (9–10 must fix, 7–8
  should fix, 4–6 worth fixing, 1–3 optional), each with profile and
  category chips, body (Markdown), anchors (`file:line`, linked to the file
  at the commit on GitHub), code excerpt, and suggestions as cards with
  effort, scope, pros, and cons. Filters by profile, category, and band.
  Superseded submissions' comments are hidden by default. A header shows
  each profile's verdict and severity counts, and `missing_profiles` as a
  warning.
- **Full report.** **Open full report** runs `ws review report --format
  html`, writes it to the app's cache directory as `<repo>-<sha7>.html`,
  and opens it in the default browser.
- **Decisions.** Each finding with a `<profile>:<id>` qualified id takes
  one decision: `comment`, `fix` (with the index of the chosen suggestion,
  or `null` for "the fixer's choice"), or `skip`, and an optional note. A
  `#N:` finding is shown read-only, with "unprofiled or superseded; can't
  be selected". Stored as:

  ```json
  "triage": [{
    "repo": "github.com/bobcob7/buff-bear", "number": 42, "ref": "<40-hex>",
    "id": "security:dropped-context-1",
    "decision": "fix", "suggestion": 0, "note": "",
    "done": null
  }]
  ```

  under `reviews` in `desktop.json`. `done` is filled by
  [0041](0041-desktop-acts-on-findings.md)'s actions. Pruning follows
  0034, with one difference: triage is kept while the pull request is
  open even after its head moves, so the last review's decisions stay
  visible next to the next review; it's dropped when the PR closes.
- **Not carried over.** A new review at a new commit starts undecided.
  Its findings show "decided at <sha7>" when the same qualified id was
  decided at an earlier commit of the same PR.

### Why this option

1. Selection controls sit on each finding and dispatch to the same Redux
   store and backend commands as the rest of the app; an embedded page
   would need a message channel for every click.
2. Only profile-qualified ids are stable across calls, so only they can
   carry a decision; keying by commit as well means a resubmission at a new
   head is a new decision, never a silently re-attached one.
3. Findings stay in the store; the app caches them only for the session
   and keeps nothing but decisions.
4. MUI components follow the app's theme; the HTML report has its own
   palette, which is fine in a browser tab.

It's weak in that the app re-implements a view loam-refinery already has,
so changes to the report's shape need an app change too; and decisions
don't follow a finding from one commit to the next, only point back to it.

## Alternatives

### Embed loam-refinery's HTML report in the pane

**Pros:** no rendering code in the app; always matches loam-refinery's
current design.
**Cons:** selection would need controls the report doesn't have, wired
through `postMessage`; its static script is a contract loam-refinery keeps
for a browser, not for an embedding app; and whether that script would run
under the app's CSP at all is **unverified**. Kept as **Open full report**.

### Keep decisions in the review workspace's state

**Pros:** decisions next to the review record; removed with it.
**Cons:** `ws` would gain state about a UI's choices; removing a review
workspace would lose them while the PR is still open.

### Carry decisions over by qualified id

**Pros:** less re-triage after a push.
**Cons:** the same `profile:id` at a new commit can be a different
finding — ids are reviewer-chosen slugs — so a carried-over "skip" could
hide a new problem.

## Consequences

- Amends [0034](0034-desktop-state-file.md) with the `triage` key and its
  pruning rule; no version bump, since a missing key is empty.
- The report types in `desktop/src/features/reviews/` mirror the fields
  of `collect-reviews` the view uses; a Vitest fixture of real output pins
  them, as in 0037.
- Conformance: `cargo test` for the triage store (set, clear, pruning,
  `#N:` rejected) and the report cache; Vitest for grouping, filters, the
  read-only rule, and "decided at".

## References

[^lr-collect]: loam-refinery `describe --lens=collect-reviews`, built from f7b52c0, run 2026-10-26. Quoted as above.
[^lr-html]: loam-refinery `docs/features/html-report.md` §5.1, at f7b52c0. "Exactly one `<script>` element ships with the page, and it is **static**"; "The script's only channel into the page's data is reading `data-*` attributes and element content the template already wrote".
