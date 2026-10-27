# ws review decide

```
ws review decide [--json] (<id> | --repo <repo> --ref <sha>) <finding>... (--comment | --fix [--suggestion <n>] | --skip | --clear) [--note <text>]
```

Records what to do with findings of a review: post them as comments,
fix them (with one of their suggestions, if you name it), or skip them.
The decisions are kept with the review's submissions, so the requester, a
fix agent, and the desktop app all see them in `ws review report`.

Name the review by its workspace, or by `--repo` and `--ref`, as for
`ws review report`. Name each finding by its `<profile>:<id>` from the
report's `comments`. Superseded findings (`#<n>:<id>`) can't be decided.
Decisions belong to one commit: a review of a new commit starts
undecided, and a profile's resubmission drops the decisions on its
findings.

Setting a decision replaces any earlier one, `done` included. `--clear`
removes it. All the findings are checked before any is changed.

## Flags

| Flag                  | Meaning                                                  |
| --------------------- | -------------------------------------------------------- |
| `--comment`           | Post the findings as review comments                     |
| `--fix`               | Have the findings fixed                                  |
| `--suggestion <n>`    | With `--fix`, the suggestion to apply, counting from 0   |
| `--skip`              | Leave the findings as they are                           |
| `--clear`             | Remove the findings' decisions                           |
| `--note <text>`       | A note kept with the decisions; not with `--clear`       |
| `--repo <repo>`       | The reviewed repo's id, with `--ref`                     |
| `--ref <sha>`         | The reviewed commit, all 40 characters, with `--repo`    |
| `--json`              | Print the result as JSON                                 |

## JSON output

```json
{
  "repo": "github.com/bobcob7/buff-bear",
  "ref": "3f2a9c1e…",
  "decisions": {
    "security:dropped-context-1": {"decision": "fix", "suggestion": 0, "note": "",
      "at": "2026-10-26T18:30:00Z", "done": null},
    "go:naked-return-2": null
  },
  "warnings": []
}
```

`decisions` has each finding named, with its decision, or null once
cleared.

## Exit codes

| Situation                                          | Error code          | Exit |
| -------------------------------------------------- | ------------------- | ---- |
| Not exactly one of `--comment`, `--fix`, `--skip`, `--clear`; `--suggestion` without `--fix` or past the finding's suggestions; `--note` with `--clear`; no finding | `usage` | 2 |
| No such workspace, or it isn't a review            | `review_not_found`  | 3    |
| A finding isn't in the current report, or is superseded; `details.findings` lists them | `finding_not_found` | 3 |

## Examples

```console
$ ws review decide --json review-buff-bear-42-3f2a9c1-d8efb24f security:dropped-context-1 --fix --suggestion 0
$ ws review decide review-buff-bear-42-3f2a9c1-d8efb24f go:naked-return-2 tests:missing-case-1 --skip --note "covered in #43"
$ ws review decide --repo github.com/bobcob7/buff-bear --ref 3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90 go:naked-return-2 --clear
```
