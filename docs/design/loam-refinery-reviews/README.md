# Reviews with loam-refinery's ideas: design

How a review started from `ws` or the desktop app becomes a structured,
multi-reviewer report that the agent who asked for it can act on, and
that a person can triage finding by finding. The review format, checks,
profiles, and combining come from
[loam-refinery](https://github.com/bobcob7/loam-refinery), forked into
`ws` and specialized for workspaces. The decisions are ADRs
[0037](../../adrs/0037-review-engine-forked-from-loam-refinery.md) to
[0041](../../adrs/0041-desktop-acts-on-findings.md); the desktop screens
are in a follow-up design PR.

## Why

Today a review is a `ws claude` agent with a free-form prompt
([0032](../../adrs/0032-review-agent-per-pr-head.md)). What comes back is
prose: the app shows the agent's job result and last reply, and "counts no
findings" ([reviews.md](../desktop-sections/reviews.md#decisions)). That
leaves three gaps:

1. **No shape.** A finding can't be referred to, sorted, or filtered, and
   there's no telling a hollow "LGTM" from a real review.
2. **One reviewer.** A single prompt asks one agent to look for bugs,
   security problems, and missing tests at once, on one model.
3. **No way back.** An agent that wants its own PR reviewed has to start
   the review, find the workspace, and read a transcript. A person who
   wants to post two findings and fix a third has to copy text by hand.

loam-refinery answers the first two: a review is a JSON document with
addressable comments, numeric priorities, anchors verified against the
commit, and competing suggestions with effort and blast radius; a
**profile** (`architecture`, `claims`, `security`, `tests`, `go`, …)
focuses a reviewer; and reviews of one commit combine into one report,
each comment qualified as `<profile>:<id>`. `ws` takes that code and
builds the rest around workspaces.

## Overview

```mermaid
sequenceDiagram
    participant R as Requester<br/>(agent or desktop app)
    participant WS as ws
    participant O as Review agent<br/>(ws claude, PR head)
    participant S as review-<profile><br/>subagents
    participant ST as ws review store

    R->>WS: ws review start --pr 42 buff-bear
    WS->>WS: gh pr view → headRefOid; changed files → profiles<br/>write state/<id>/review.toml
    WS->>O: claude --bg --agents {review-security: {model: opus}, …}
    WS-->>R: {workspace, review, wait_command}
    R->>WS: ws review wait <id>  (background)
    O->>S: start every review-* subagent
    S->>WS: ws review prime --profile=P
    S->>ST: ws review submit -  (checked, anchors verified)
    O->>WS: ws review report (retries missing profiles once)
    O-->>O: idle (done)
    WS-->>R: report: profiles, findings, decisions
    R->>WS: ws review decide … (agent, or a person in the app)
    R->>R: fix; the app can also post comments
```

### 1. A fork of loam-refinery, specialized

`ws` copies loam-refinery's document format, checks, anchor
verification, guides, profiles, combining, and renderers at f7b52c0 and
owns them from then on. It doesn't run or require loam-refinery. That
frees `ws` to specialize: `submit` fills in the commit from the review
record and only accepts the record's profiles; `ws` keeps the store and
its own output contract; and findings can carry decisions and be posted,
which loam-refinery rules out. See
[0037](../../adrs/0037-review-engine-forked-from-loam-refinery.md).

### 2. One review agent per PR head; one subagent per profile, each with its model

0032 stays as it is: a review is one `ws claude` workspace at the PR's
head commit. `ws review start` picks the profiles: the `always` ones,
plus `auto` ones whose file patterns match the diff (`go` for `*.go`).
It starts the session with `--agents`, defining a `review-<profile>`
subagent per profile with that profile's guide as its prompt and its
model pinned. The review agent starts them all, checks with `ws review
report` that each submitted, and retries a missing one once. See
[0038](../../adrs/0038-review-fans-out-to-profile-subagents.md).

### 3. `ws review`

```
# requesters
ws review start [--pr N] [--profile P[:MODEL]]... [--auto P[:MODEL]]... [--requester ID|none] <spec> -- [claude flags] [focus]
ws review wait [--timeout D] <id>
ws review report [--format json|markdown|html] [--decided comment|fix|skip] (<id> | --repo R --ref SHA)
ws review decide (<id> | --repo R --ref SHA) <finding>... (--comment | --fix [--suggestion N] | --skip | --clear) [--note T]
ws review acted  (<id> | --repo R --ref SHA) <finding>... (--commented URL | --fix-sent AGENT)
ws review profiles

# reviewers, inside a review workspace
ws review prime [--profile P]
ws review describe [--lens N]
ws review submit [path|-]
```

`start` writes `state/<id>/review.toml` (repo, commit, PR, profiles with
models, requester) and starts the agent. Submissions go to
`CONFIG_DIR/reviews/<host>/<owner>/<name>/<sha>/`, which outlives the
workspace. `wait` blocks until the review agent is done and prints the
report; an agent that asked for a review runs it as a background command.
See [0039](../../adrs/0039-ws-review-command.md).

### 4. Decisions live with the findings; the app renders them

Each `<profile>:<id>` finding can carry a decision (**comment**, **fix**
with a chosen suggestion, or **skip**), a note, and whether it was acted
on, in `decisions.json` beside the submissions. Agents see them in `ws
review report`; the app sets them with `ws review decide`. The Reviews
pane renders findings itself, grouped by priority band, filterable by
profile and category, with anchors, code, and suggestions side by side;
**Open full report** opens the HTML version in the browser. See
[0040](../../adrs/0040-review-decisions-kept-with-findings.md).

### 5. Acting on selected findings

- **Comment** posts one GitHub pull request review with `gh api`, at the
  reviewed commit: a line comment per finding whose anchor is in the diff,
  file comments and the review body for the rest. A preview shows exactly
  what will be posted; nothing is posted without confirming it. Agents
  never post.
- **Fix** tells the agent that requested the review, if its session
  still exists, to fix the findings decided `fix`, which it reads with
  `ws review report --decided fix`. Otherwise it starts a fix agent on the
  PR's branch.

See [0041](../../adrs/0041-desktop-acts-on-findings.md).

## What changes where

| Area | Change |
| ---- | ------ |
| `internal/review/` (new) | The fork: document, schema, checks, verification (through `internal/git`), guides and profiles, combining, renderers |
| `internal/review/store` (new) | `CONFIG_DIR/reviews/`: submissions, supersede, decisions, locking |
| `internal/review/start` (new) | Review records, profile choice, `--agents` JSON, the review agent's prompt |
| `internal/cli/review.go` | `ws review` subcommands; `ws llm review` guide |
| `internal/cli/list.go` | `review` field per workspace in `ws list --json` |
| `docs/config.md` | `[review] timeout`, `[review.profiles.<name>]` `when` and `model` |
| `desktop/src-tauri/src/reviews/` | Start reviews with `ws review start`; report, decide, Comment, Fix |
| `desktop/src/features/reviews/` | Report view, decision controls, Comment preview, Fix dialog; profile and model settings |
| loam-refinery | Nothing; it stays its own project |

## Build order

1. Fork into `internal/review/` with its tests; `ws review prime`,
   `describe`, `submit` and the store.
2. Review records, profile choice, `--agents`, `ws review start`, `wait`,
   `report`, `ws llm review`, and `review` in `ws list`.
3. `decide`, `acted`, and `--decided`.
4. The desktop app starts reviews with `ws review start` and its profile
   settings.
5. Report view and decisions in the desktop app.
6. Comment and Fix.

After step 2, agents can review each other's work from a terminal with no
app at all; after step 3, they can triage it too.

## Out of scope

- Posting to GitHub without a person confirming it. Agents get the
  report; only the app posts, and only from the preview.
- Reconciling findings across profiles. Owning the code makes it
  possible later; for now the app shows findings side by side and leaves
  the call to you.
- Comparing reviews across commits ("what's new since the last review").
- GitLab and other forges, as in [reviews.md](../desktop-sections/reviews.md).
