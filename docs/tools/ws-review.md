# ws review

Structured reviews: check, store, and explain review documents.

## Synopsis

```
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
review it ([0039](../adrs/0039-ws-review-command.md)). The commands here
are the reviewer's, and run inside a review workspace; outside one they
fail with `review_not_found`. The requester's commands (`start`, `wait`,
`report`, `profiles`) are added with the rest of [the
design](../design/loam-refinery-reviews/README.md).

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
file. Requesters will read reviews through `ws review report`, added
with the rest of the design, which is the contract; the layout isn't.

## Options

| Option | Description |
| --- | --- |
| `--profile <NAME>` | `prime`: append this profile's guide |
| `--lens <NAME>,...` | `describe`: open these entries |
| `--list` | `describe`: the index alone |
| `--json` | `submit`: print the result below |

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
| `--profile` isn't one of the review's profiles, or no profile has the name | `profile_unknown` | 2 |
| A profile file can't be read or parsed | `config_invalid` | 1 |
| The review has errors: revise and resubmit | `review_invalid` | 1 |
| The anchors can't be checked: the checkout diverged from the commit, or doesn't have it | `review_unverifiable` | 1 |
| Not inside a review workspace | `review_not_found` | 3 |

For `review_invalid` and `review_unverifiable`, `details` holds the
`diagnostics` and the `lenses` that explain them, with `verification`,
`counts`, and `skipped`, in the shapes above. Without `--json`, the
diagnostics follow the message on stderr.

## Examples

```console
$ ws review prime --profile security
$ ws review describe --lens=review-profile
$ ws review submit --json - < review.json
```
