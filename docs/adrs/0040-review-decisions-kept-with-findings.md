# 0040. A decision per finding is kept with the review in `ws`'s store, set with `ws review decide`; the desktop app renders findings itself from `ws review report --json`

- Status: Proposed
- Date: 2026-10-26

## Context

### Problem

A finished review is a combined report ([0039](0039-ws-review-command.md)):
profiles with verdicts, and comments with an id, priority, category,
body, anchors, and suggestions. A person should read it comfortably in the
Reviews section and choose, finding by finding, what to post as a comment,
what to fix and how, and what to skip; [0041](0041-desktop-acts-on-findings.md)
acts on those choices. Where are the choices kept, and how does the app
show the report?

### What a solution needs

1. **A choice names exactly one finding, durably**, so it can't attach
   to a different finding after a resubmission.
2. **Agents can read the choices**: the requester or a fix agent should
   see which findings were chosen, with which fix, and which were already
   acted on.
3. **Selection is part of the view** and survives switching sections and
   restarting the app.
4. **Looks like the rest of the app**, in both themes.

### Relevant facts

- Every submission in `ws`'s store has a profile from the review record
  ([0037](0037-review-engine-forked-from-loam-refinery.md)), so a current
  finding's id is `<profile>:<id>`; a superseded one's is `#<n>:<id>`,
  where `n` "is positional and shifts on the next submission"[^lr-collect].
- The store keeps a directory per repo and commit that outlives the
  review workspace ([0039](0039-ws-review-command.md)).
- The HTML renderer, forked with the rest, writes one self-contained page
  with a single static script[^lr-html], about 22 KB for two short
  reviews[^bench-lr].
- The app's webview CSP is `default-src 'self'; style-src 'self'
  'unsafe-inline'; …`, with no `script-src` exception
  (`desktop/src-tauri/tauri.conf.json`). The frontend renders Markdown
  with `react-markdown` and `remark-gfm`, and uses MUI
  (`desktop/package.json`).
- `desktop.json` holds the app's own state and settings
  ([0034](0034-desktop-state-file.md)).

## Decision

**Decisions live in the store.** `decisions.json` in the review's
directory holds one entry per decided finding:

```json
{ "version": 1, "decisions": {
  "security:dropped-context-1": {
    "decision": "fix", "suggestion": 0, "note": "",
    "at": "2026-10-26T18:30:00Z",
    "done": null } } }
```

- `ws review decide [--json] (<review> | --repo R --ref SHA) <finding>...
  (--comment | --fix [--suggestion N] | --skip | --clear) [--note TEXT]`
  sets or clears them. Only `<profile>:<id>` ids that exist in the
  current report are accepted (`finding_not_found`, exit 3).
- `ws review acted … <finding>... (--commented URL | --fix-sent AGENT)`
  fills `done` ([0041](0041-desktop-acts-on-findings.md)).
- `ws review report` includes each comment's `decision`, and takes
  `--decided comment|fix|skip` to list only those.

**Reading.** The backend runs `ws review report --json` when a review is
selected, every 30 s while its agent works (so each profile shows as it
submits), and when the agent goes idle; it caches the result by repo and
commit for the session.

**Showing.** Findings are React components: grouped by priority band (9–10
must fix, 7–8 should fix, 4–6 worth fixing, 1–3 optional), each with
profile and category chips, Markdown body, anchors linked to the file at
the commit on GitHub, code excerpt, and suggestions as cards with effort,
scope, pros, and cons. Filters by profile, category, and band. A header
shows each profile's verdict, severity, and model, and missing profiles.
Superseded comments are hidden by default and read-only.
**Open full report** writes `ws review report --format html` to the app's
cache directory and opens it in the browser.

**Not carried over.** A review at a new commit starts undecided; a finding
whose qualified id was decided at an earlier commit of the same PR shows
"decided at <sha7>".

### Why this option

1. Keying by repo, commit, and a profile-qualified id names one finding
   for good; superseded findings can't be decided, so a positional id never
   carries a decision.
2. Decisions next to the findings are in every report, so the requester,
   a fix agent, and the app see the same state, and a review removed from
   Agents keeps them.
3. The selection controls sit on each finding and dispatch to the same
   Redux store and backend commands as the rest of the app.
4. MUI components follow the app's theme; the HTML report keeps its own
   look in the browser.

It's weak in that the app re-implements a view the HTML renderer already
has, and that decisions don't follow a finding from one commit to the
next, only point back to it.

## Alternatives

### Keep decisions in `desktop.json`

**Pros:** no new `ws` commands; app state stays in the app, as 0034 has
it.
**Cons:** agents can't see what was chosen or already done, so the Fix
message has to carry everything; decisions about findings would live apart
from the findings `ws` owns.

### Embed the HTML report in the pane

**Pros:** no rendering code in the app.
**Cons:** selection would need controls the page doesn't have, wired
through `postMessage`; whether its script runs under the app's CSP is
**unverified**. Kept as **Open full report**.

### Carry decisions over by qualified id

**Pros:** less re-triage after a push.
**Cons:** the same `profile:id` at a new commit can be a different
finding, since ids are reviewer-chosen slugs, so a carried-over "skip"
could hide a new problem.

## Consequences

- `desktop.json` gains no triage; the app calls `ws` for every change.
  Each decision is a `ws` call (tens of milliseconds locally), batched per
  click on several findings.
- The report types in `desktop/src/features/reviews/` mirror `report
  --json`; a Vitest fixture of real output pins them.
- Conformance: Go tests for `decide` and `acted` (accepted ids, clearing,
  the lock, the round trip, superseded ids rejected); Vitest for grouping,
  filters, the read-only rule, and "decided at".

## References

[^lr-collect]: loam-refinery `describe --lens=collect-reviews`, built from f7b52c0, run 2026-10-26. "`ordinal` is positional and shifts on the next submission; treat a `#N:id` form as good for one call only."
[^lr-html]: loam-refinery `docs/features/html-report.md` §5.1, at f7b52c0. "Exactly one `<script>` element ships with the page, and it is **static**".
[^bench-lr]: Measured 2026-10-26 with loam-refinery built from f7b52c0, as in [0037](0037-review-engine-forked-from-loam-refinery.md)'s `bench-lr`: `collect-reviews --format html` for two one-comment reviews was 22,412 bytes.
