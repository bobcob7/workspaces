# ws review

Structured reviews: start a review, wait for it, read the combined
report, and decide what to do with each finding; and, for reviewers,
check, store, and explain review documents.

## Synopsis

```
# requesters
ws review start [OPTIONS] <SPEC> [-- <CLAUDE ARGS>... [<FOCUS>]]
ws review wait [--timeout <DURATION>] <ID>
ws review report [--format json|markdown|html] [--decided comment|fix|skip] (<ID> | --repo <REPO> --ref <SHA>)
ws review list [--repo <REPO>] [--pr <N>]
ws review decide (<ID> | --repo <REPO> --ref <SHA>) <FINDING>... (--comment | --fix [--suggestion <N>] | --skip | --clear) [--note <TEXT>]
ws review acted (<ID> | --repo <REPO> --ref <SHA>) <FINDING>... (--commented <URL> | --fix-sent <AGENT>)
ws review profiles
ws review profiles show <NAME>
ws review profiles add <NAME> --description <D> [--when <W>] [--match <GLOB>]... (--guide-file <F> | -)
ws review profiles edit <NAME> [--description <D>] [--when <W>] [--match <GLOB>]... [--no-match] [--guide-file <F> | -]
ws review profiles rm <NAME>

# reviewers, inside a review workspace
ws review prime [--profile <NAME>]
ws review describe [--lens <NAME>,... | --list]
ws review submit [OPTIONS] [<PATH> | -]
```

## Description

A review is one JSON document: a verdict, a summary, the reviewer's
profile, and comments with an id, a priority, a category, a body, anchors
(file and lines at the reviewed commit), and competing suggestions:
[The review document](../review-document.md) describes every field, and
its JSON Schema is
[`review.schema.json`](../../internal/review/schema/review.schema.json).
The format, its checks, and the guides come from a fork of
[loam-refinery](https://github.com/bobcob7/loam-refinery) at f7b52c0
([0037](../adrs/0037-review-engine-forked-from-loam-refinery.md)).

A **review workspace** reviews one commit of one repo. Its record,
`state/<id>/review.toml`, names the repo, the commit, and the profiles that
review it, and who asked ([0039](../adrs/0039-ws-review-command.md)).
`start`, `wait`, `report`, `decide`, `acted`, and `profiles` are the
requester's: an agent that wants its pull request reviewed, or the
desktop app. `prime`,
`describe`, and `submit` are the reviewer's, and run inside a review
workspace; outside one they fail with `review_not_found`.

### `ws review start`

Starts a review: a workspace at the reviewed commit, as [`ws
claude`](ws-claude.md) makes one, with a review agent that starts one
reviewer subagent per profile ([0038](../adrs/0038-review-fans-out-to-profile-subagents.md)).

1. **The commit.** `SPEC` names one repo. With `--pr <N>`, the spec has no
   `@ref`, and the commit is the pull request's head, from `gh pr view <N>
   --repo <cache id> --json number,title,headRefOid,headRefName,baseRefName,isCrossRepository,url,body`;
   fork pull requests work, since the commit is fetched by its hash.
   Without it, the spec's ref is resolved to a commit. The cache is
   fetched first unless `--offline`. `gh` runs only through `internal/gh`,
   with `GH_PROMPT_DISABLED=1` and `NO_COLOR=1`.
2. **The profiles.** The files the review covers are listed in the cache:
   `git diff --name-only <base>...<head>` with `--pr`, else the commit
   against its first parent (every file of a root commit). Without
   `--profile` or `--auto`, every profile whose `when` is `always` joins,
   and every `auto` one with a matching file; `config.toml`'s
   [`[review.profiles.<name>]`](../config.md#review) overrides `when` and
   sets `model`. `--profile NAME[:MODEL]` and `--auto NAME[:MODEL]`, both
   repeatable, replace that set: `--profile` profiles join, `--auto` ones
   join if a file matches. A profile's model is its flag's, else
   `config.toml`'s, else the session's.
3. **The requester**: the workspace holding the working directory,
   nobody outside one, or `--requester <ID>` (`none` for nobody).
4. **The record**, `state/<id>/review.toml`, is written before the
   workspace is moved into place ([0022](../adrs/0022-workspace-record-before-rename.md)).
5. **The agent** is started through `internal/claude` with
   `--agents <json>`: one `review-<profile>` subagent per profile, whose
   prompt is `ws review prime --profile <profile>`'s guide followed by the
   task, with tools `Read`, `Grep`, `Glob`, and `Bash`, and `model` only
   when the profile has one. Then come the options after `--`, and last
   the review agent's prompt (`internal/review/prompt.md`). The last
   argument after `--`, unless it starts with `-`, is the requester's
   **focus**, added to every prompt under "From the requester"; give a
   `claude` option its value with `=` (`--model=opus`) when no focus
   follows it. `--settings` and `--agents` are refused there.

The workspace is named `review-<repo>-<N>-<sha7>`, or
`review-<repo>-<sha7>` without `--pr`, plus [0036](../adrs/0036-workspace-id-suffix-and-rename.md)'s
suffix ([0032](../adrs/0032-review-agent-per-pr-head.md)); `--name`
replaces it. The requester is the workspace's parent, so removing it
removes the review ([0044](../adrs/0044-workspace-trees.md)). The workspace is hidden: `ws list` shows it only with `-a`,
and the desktop app lists it under Reviews rather than among the user's
agents; [`ws unhide`](ws-hide.md) shows it
([0043](../adrs/0043-hidden-workspaces.md)).

### `ws review wait`

Reads the review agent's session every 5 seconds, as `ws list` does,
until it is idle or stopped, or its workspace is gone, then prints what
`ws review report` prints. It returns at once for a finished review,
including one already removed: an id with no workspace is looked up in
the review store's `runs/` ([0045](../adrs/0045-finished-reviews-are-removed.md)).
After `--timeout` (default `[review] timeout`, 60 minutes) it fails with
`review_timeout`, and `details` has `workspace` and `status`; running it
again keeps waiting.

An agent runs `wait` as a background command and acts on the report when
Claude Code says the command exited. Under `claude -p`, which runs
unattended, a background command is stopped after 30 minutes unless the
agent passes a `timeout`, up to 2 hours; [`ws llm review`](#guides) says
so.

### `ws review report`

Combines the submissions stored for the review's repo and commit
([state](../state.md#reviews)). Name the review by its workspace id,
which also works after the workspace is removed, or by
`--repo host/owner/name` and `--ref <full SHA>`. `--format markdown` (the default without `--json`)
and `html` use the forked renderers; Markdown ends with the profiles that
haven't submitted. `--decided comment|fix|skip` lists only the findings
decided that way: a fixer reads `--decided fix --json`.

### `ws review list`

Lists every review the store has a run for, and any review workspace
started before runs were kept, newest first, optionally only of one repo
(`--repo`, an id) or pull request (`--pr`)
([0045](../adrs/0045-finished-reviews-are-removed.md)). With `--json`:
`{"reviews": [{"id", "review", "agent"}], "warnings"}`, where `review` is
the record and `agent` is `{"workspace", "status"}` while the workspace
exists, `null` once it's removed. The table's AGENT column says
`removed`.

### Finished reviews are removed

Once a review's agent is idle or stopped and every profile it chose has
a current submission, the removal pass removes its agent and workspace as
`ws rm` would, descendants first
([0045](../adrs/0045-finished-reviews-are-removed.md)). A review still
working, or missing a submission, is kept. `[review] remove_finished =
false` in [`config.toml`](../config.md) keeps them all.

### `ws review decide`

Records what to do with findings
([0040](../adrs/0040-review-decisions-kept-with-findings.md)): `--comment`
to post them, `--fix` to have them fixed, with `--suggestion <N>` (an
index into the finding's `suggestions`) to choose how, or `--skip`.
`--note` is kept with the decision; `--clear` removes it. Findings are
named by their `<profile>:<id>` in the current report; a superseded
finding (`#<n>:<id>`) or one not in the report fails with
`finding_not_found`, and nothing is changed. Setting a decision replaces
any earlier one, `done` included.

Decisions are kept in the review's store directory, in `decisions.json`
([state](../state.md#reviews)), so they outlive the review workspace and
every reader of the report sees them. They belong to one commit: a review
of a new commit starts undecided. A profile's resubmission drops the
decisions on its findings, since the same id can then name a different
finding.

### `ws review acted`

Records that decided findings were acted on
([0041](../adrs/0041-desktop-acts-on-findings.md)), in the decision's
`done`: `--commented <URL>` once findings decided `comment` are posted,
and `--fix-sent <AGENT>` once findings decided `fix` are sent to an
agent. Each finding must already be decided that way.

### `ws review profiles`

Lists every profile with its description, `when`, `match`, and model,
with `config.toml` applied, and its `source`: `builtin`, `user` (one you
added), or `override` (your file replacing the built-in of that name).
`builtin` is kept beside `source` for compatibility. The desktop app's
settings read it.

Its operations manage the files in `CONFIG_DIR/review/profiles/`
([0042](../adrs/0042-review-profile-management.md)):

- **`show <NAME>`** prints one profile as the listing does, with its
  guide and the file it's read from (`path`, null for a built-in).
- **`add <NAME>`** writes `<NAME>.md`. The name is lowercase letters,
  digits, and hyphens, and not `decisions`, which is reserved
  (`profile_invalid`); a name any profile has, built in or yours, is
  `profile_exists`. `--description` is required, and the guide comes from
  `--guide-file` or, with `-`, stdin, and can't be empty.
- **`edit <NAME>`** changes only the fields given: `--match` replaces the
  patterns, and `--no-match` removes them. Editing your profile or
  override rewrites its file; editing a built-in writes a full copy
  first, so it becomes an `override`. An override doesn't follow later
  improvements to its built-in.
- **`rm <NAME>`** deletes your file. For an override that restores the
  built-in (`restored`); a built-in without one is `profile_builtin`: set
  it to `off` instead. A name no profile has is `profile_unknown`.

Every write goes to a temp file, is parsed back, and is renamed into
place, under a lock on the directory, so a profile that wouldn't load is
never left behind. Reviews already running keep the guides they started
with: `start` copies each into the session.

### Terms

- **Profile**: what a reviewer looks for, such as `security` or `go`. A
  review runs one reviewer per profile, and each submits its own review
  document, naming its profile. `ws review prime --profile` prints a
  profile's guide.
- **Lens**: one entry `ws review describe --lens` explains: a field of
  the document (`priority`), a check a diagnostic named
  (`anchor-line-out-of-range`), or a topic (`tiers`). Lenses document the
  format; they don't change what a reviewer looks for. The name comes from
  loam-refinery's `describe --lens`.

A profile is a focus for the review; a lens is a page of its manual.

### Relation to `ws llm`

`ws llm` explains how to use `ws`: its commands, flags, and exit codes,
for an agent that drives `ws`. `ws llm review` is that guide for these
commands. `ws review prime` and `describe` explain the review format
itself, to the reviewer that writes one: `prime` is small enough to pin
into a reviewer's prompt, with its profile appended, and `describe`
answers one question at a time, so a reviewer pays only for the fields
and checks it asks about. Both read the review workspace, which `ws llm`
never does: `prime --profile` takes only the review's profiles.

### `ws review prime`

Prints the workflow: how to write a review, submit it, and read the
result, in a few hundred tokens, for a reviewer's prompt. With
`--profile`, the profile's guide follows, framed as operator-supplied.
The profile must be one of the review's.

Profiles direct a reviewer's attention
([0038](../adrs/0038-review-fans-out-to-profile-subagents.md)). Eight are
built in:

| Profile | When | Matches |
| --- | --- | --- |
| `architecture`, `claims`, `security`, `tests` | `always` | |
| `go` | `auto` | `*.go`, `go.mod` |
| `typescript` | `auto` | `*.ts`, `*.tsx` |
| `compatibility`, `efficiency` | `off` | |

`CONFIG_DIR/review/profiles/<name>.md` adds a profile, or replaces the
built-in of that name. Its front matter has `description` (required, up to
120 characters), `when` (`always`, `auto`, or `off`), and `match` (a list
of quoted file patterns; a pattern without `/` matches the file's base
name). Without `when`, a profile is `always`, or `auto` if it has `match`.

```markdown
---
description: Rust: ownership, unsafe blocks, and error handling
when: auto
match: ["*.rs", "Cargo.toml"]
---
Look hardest at …
```

### `ws review describe`

Explains the document a piece at a time. With no flag: its shape and the
index of every name it explains. `--lens` opens entries in full, comma
separated: a field (`priority`), a check a diagnostic named
(`anchor-line-out-of-range`), or a topic (`tiers`). `--list` prints the
index alone. The output is one JSON object, but a guide rather than a
result, so `--json` is refused, as for `ws review prime` and `ws llm`.

### `ws review submit`

Checks a review from `PATH`, or stdin for `-` or no path, and stores it:

1. `ref` may be left out and is filled in from the record; if given, it
   must be the record's commit (`review-ref`).
2. `profile` is required and must be one of the record's profiles
   (`review-profile`).
3. The forked checks run: the JSON Schema and structural checks, anchor
   verification against the workspace's repo at the commit, and
   advisories, which never fail the run. Verification runs git through
   `internal/git` ([0003](../adrs/0003-use-git-cli.md)).
4. A valid review is stored in
   `CONFIG_DIR/reviews/<host>/<owner>/<name>/<sha>/<profile>.json`
   ([state](../state.md#reviews)). The profile's previous submission, if
   any, is superseded: renamed `<profile>.<n>.json`.

The store is outside the review workspace on purpose: the workspace, its
agent, and its record are removed when the review agent is, but its
findings, and the decisions made on them later, are still needed while
the pull request is open ([0039](../adrs/0039-ws-review-command.md)). The
reviewer doesn't need `path`; it's reported so a person can find the
file. Requesters read reviews through `ws review report`, which is the
contract; the layout isn't.

### Guides

`ws llm review` tells an agent how to ask for a review and get the report
back: start it, run `wait_command` in the background, and act when woken.
It tells a fixer to read `ws review report --json --decided fix`.
`ws llm review <op>` has each command's guide.

## Options

| Option | Description |
| --- | --- |
| `--pr <N>` | `start`: review this pull request's head |
| `--profile <NAME>[:<MODEL>]` | `start`: review with this profile; repeatable. `prime`: append this profile's guide |
| `--auto <NAME>[:<MODEL>]` | `start`: review with this profile if a changed file matches it; repeatable |
| `--requester <ID>\|none` | `start`: who asked; default: the workspace you're in |
| `--name <NAME>` | `start`: name the workspace |
| `--offline` | `start`: don't fetch the cache |
| `--timeout <DURATION>` | `wait`: give up after this long |
| `--format json\|markdown\|html` | `report`: the output format |
| `--decided comment\|fix\|skip` | `report`: only the findings decided this way |
| `--repo <REPO>`, `--ref <SHA>` | `report`, `decide`, `acted`: the review by repo and commit |
| `--comment`, `--fix`, `--skip`, `--clear` | `decide`: the decision; exactly one |
| `--suggestion <N>` | `decide`: with `--fix`, the suggestion to apply, from 0 |
| `--note <TEXT>` | `decide`: a note kept with the decision |
| `--commented <URL>` | `acted`: the findings were posted in this review |
| `--fix-sent <AGENT>` | `acted`: the findings were sent to this agent |
| `--lens <NAME>,...` | `describe`: open these entries |
| `--list` | `describe`: the index alone |
| `--description <D>` | `profiles add`, `edit`: one line, at most 120 characters; required by `add` |
| `--when always\|auto\|off` | `profiles add`, `edit`: when the profile joins a review |
| `--match <GLOB>` | `profiles add`, `edit`: a file pattern; repeatable, and `edit` replaces them all |
| `--no-match` | `profiles edit`: remove every file pattern |
| `--guide-file <F>`, `-` | `profiles add`, `edit`: read the guide from a file, or from stdin |
| `--json` | `start`, `wait`, `report`, `decide`, `acted`, `profiles` and its operations, `submit`: print the result below |

`ws review start --json` prints [`ws claude`](ws-claude.md)'s result, plus
the record and the command that waits for the report:

```json
{
  "id": "review-buff-bear-42-3f2a9c1-d8efb24f",
  "session": "e8cdf5d8",
  "path": "/…/workspaces/review-buff-bear-42-3f2a9c1-d8efb24f",
  "repos": [{"id": "github.com/bobcob7/buff-bear", "path": "buff-bear", "ref": "3f2a9c1e…", "ref_kind": "commit", "commit": "3f2a9c1e…"}],
  "warnings": [],
  "review": {
    "version": 1, "repo": "github.com/bobcob7/buff-bear", "ref": "3f2a9c1e…",
    "requester": "fix-login-d8efb24f", "started_at": "2026-10-26T18:00:00Z",
    "profiles": [{"name": "security", "model": "opus", "auto": false}, {"name": "go", "model": "", "auto": true}],
    "pr": {"number": 42, "url": "https://github.com/bobcob7/buff-bear/pull/42", "base": "main", "head_branch": "feat/login", "cross_repository": false}
  },
  "wait_command": "ws review wait --json review-buff-bear-42-3f2a9c1-d8efb24f"
}
```

`ws review report --json`, and `wait --json`, print:

```json
{
  "repo": "github.com/bobcob7/buff-bear",
  "ref": "3f2a9c1e…",
  "review": {"version": 1, "...": "the record, or null after removal"},
  "agent": {"workspace": "review-buff-bear-42-3f2a9c1-d8efb24f", "status": "idle"},
  "profiles": [
    {"name": "security", "model": "opus", "verdict": "request_changes",
     "severity": {"max": 9, "must_fix": 1, "should_fix": 0, "worth_fixing": 0, "optional": 0}, "missing": false},
    {"name": "go", "model": "", "verdict": null, "severity": null, "missing": true}
  ],
  "comments": [
    {"id": "security:dropped-context-1", "profile": "security", "priority": 9, "category": "correctness",
     "body": "…", "code": null, "anchors": [{"file": "client.go", "line": 3, "end_line": 5}],
     "suggestions": [{"summary": "…", "effort": "trivial", "scope": "line", "pros": ["…"], "cons": ["…"], "code": null}],
     "decision": {"decision": "fix", "suggestion": 0, "note": "", "at": "2026-10-26T18:30:00Z", "done": null}}
  ],
  "superseded": [],
  "warnings": []
}
```

- `agent` is null once the workspace is removed; `status` is `ws list`'s
  session state.
- `profiles` are the record's, in order, then any other profile with a
  current submission. `missing` means no current submission; a finished
  review with missing profiles isn't an error.
- `comments` are the current submissions' findings, `<profile>:<id>`;
  `superseded` holds those of submissions a resubmission replaced,
  `#<n>:<id>`.
- `decision` is the finding's decision, or null when undecided, and
  always for a superseded finding. `suggestion` is an index into
  `suggestions`, or null. `done` is null until `ws review acted`:
  `{"commented": "<url>", "at": …}` or `{"fix_sent": "<agent>", "at": …}`.
- With `--decided`, `comments` holds only the findings decided that way
  and `superseded` is empty.

`ws review decide --json` and `acted --json` print each finding named,
with its decision, or null once cleared:

```json
{
  "repo": "github.com/bobcob7/buff-bear",
  "ref": "3f2a9c1e…",
  "decisions": {
    "security:dropped-context-1": {"decision": "fix", "suggestion": 0, "note": "",
      "at": "2026-10-26T18:30:00Z", "done": {"fix_sent": "fix-buff-bear-42-3f2a9c1-0c4e1a2b", "at": "2026-10-26T18:31:00Z"}},
    "go:naked-return-2": null
  },
  "warnings": []
}
```

`ws review profiles --json` prints:

```json
{"profiles": [{"name": "go", "description": "Go: concurrency, …", "when": "auto", "match": ["*.go", "go.mod"], "model": "", "builtin": true, "source": "builtin"}],
 "warnings": []}
```

`ws review profiles show`, `add`, and `edit --json` print the profile:

```json
{"name": "go", "description": "Go, our way", "when": "auto", "match": ["*.go", "go.mod"], "model": "",
 "builtin": false, "source": "override", "guide": "Anchor a concurrency finding…",
 "path": "/…/review/profiles/go.md", "warnings": []}
```

`ws review profiles rm --json` prints:

```json
{"name": "go", "path": "/…/review/profiles/go.md", "restored": true, "warnings": []}
```

`ws review submit --json` prints:

```json
{
  "workspace": "review-buff-bear-42-3f2a9c1-d8efb24f",
  "repo": "github.com/bobcob7/buff-bear",
  "ref": "3f2a9c1e3b7d5f0c8a1e2d4b6c8f0a2e4d6b8c0f",
  "profile": "security",
  "path": "/…/reviews/github.com/bobcob7/buff-bear/3f2a9c1…/security.json",
  "superseded": null,
  "valid": true,
  "verification": {"source": "repo", "reason": null, "anchors": 3, "verified": 3},
  "counts": {"comments": 2, "errors": 0, "advisories": 1, "skipped": 0},
  "diagnostics": [{"severity": "advisory", "name": "body-thin", "comment": "a-1", "path": "/comments/0/body", "message": "…"}],
  "skipped": [],
  "lenses": ["body-thin"],
  "warnings": []
}
```

## Errors

| Situation | Error code | Exit |
| --- | --- | --- |
| Wrong arguments or flags, `--json` on `prime` or `describe`, an unknown or ambiguous lens, or a `PATH` that can't be read | `usage` | 2 |
| `start`: not one spec, `@ref` with `--pr`, a malformed `--profile` or `--auto`, `--auto` on a profile without patterns, a profile chosen twice, or no profile left | `usage`, `spec_invalid` | 2 |
| `--profile` or `--auto` names no profile, or `config.toml` configures one that doesn't exist; `prime`: not one of the review's profiles | `profile_unknown` | 2 |
| `start`: `gh` isn't on `PATH` | `gh_missing` | 1 |
| `start`: `gh` isn't signed in to the repo's host | `gh_auth` | 1 |
| `start`: no such pull request, or GitHub doesn't know the repo | `pr_not_found` | 3 |
| `start`: `--requester` names no workspace | `workspace_not_found` | 3 |
| `start`: what [`ws claude`](ws-claude.md#errors) fails with | | |
| `wait`: still running after `--timeout` | `review_timeout` | 1 |
| `wait`, `report`, `decide`, `acted`: no such workspace, or it has no review record | `review_not_found` | 3 |
| `decide`: not exactly one of `--comment`, `--fix`, `--skip`, `--clear`; `--suggestion` without `--fix` or past the finding's suggestions; `--note` with `--clear` | `usage` | 2 |
| `acted`: not exactly one of `--commented` and `--fix-sent`, or a finding not decided `comment` or `fix` to match | `usage` | 2 |
| `decide`, `acted`: a finding isn't in the current report, or is superseded; `details.findings` lists them | `finding_not_found` | 3 |
| A profile file can't be read or parsed | `config_invalid` | 1 |
| `profiles show`, `edit`, `rm`: no profile has the name | `profile_unknown` | 2 |
| `profiles add`, `edit`: no `--description` for `add`, no guide or two, nothing to change, `--match` with `--no-match`, or a bad `--when` | `usage` | 2 |
| `profiles add`, `edit`, `rm`: an invalid or reserved name; `add`, `edit`: an empty guide, or a profile that wouldn't parse back | `profile_invalid` | 2 |
| `profiles add`: a profile already has the name | `profile_exists` | 4 |
| `profiles rm`: a built-in with no override | `profile_builtin` | 4 |
| The review has errors: revise and resubmit | `review_invalid` | 1 |
| The anchors can't be checked: the checkout diverged from the commit, or doesn't have it | `review_unverifiable` | 1 |
| Not inside a review workspace | `review_not_found` | 3 |

For `review_invalid` and `review_unverifiable`, `details` holds the
`diagnostics` and the `lenses` that explain them, with `verification`,
`counts`, and `skipped`, in the shapes above. Without `--json`, the
diagnostics follow the message on stderr.

## Examples

```console
$ ws review start --json --pr 42 buff-bear -- "Look hard at the retry loop"
$ ws review wait --json review-buff-bear-42-3f2a9c1-d8efb24f
$ ws review report --repo github.com/bobcob7/buff-bear --ref 3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90
$ ws review report --format html review-buff-bear-42-3f2a9c1-d8efb24f > report.html
$ ws review decide review-buff-bear-42-3f2a9c1-d8efb24f security:dropped-context-1 --fix --suggestion 0
$ ws review report --json --decided fix review-buff-bear-42-3f2a9c1-d8efb24f
$ ws review acted review-buff-bear-42-3f2a9c1-d8efb24f security:dropped-context-1 --fix-sent fix-buff-bear-42-3f2a9c1-0c4e1a2b
$ ws review profiles
$ ws review profiles add docs --description "Docs: accuracy and links" --match '*.md' - < docs-guide.md
$ ws review profiles edit go --guide-file our-go.md
$ ws review profiles rm go
$ ws review prime --profile security
$ ws review describe --lens=review-profile
$ ws review submit --json - < review.json
```
