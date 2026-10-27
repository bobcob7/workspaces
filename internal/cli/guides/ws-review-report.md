# ws review report

```
ws review report [--format json|markdown|html] [--json] (<id> | --repo <repo> --ref <sha>)
```

Combines a review's stored submissions into one report. Name the review
by its workspace, or by `--repo` (`host/owner/name`) and `--ref` (the full
commit), which still works after the workspace is removed: submissions
outlive it.

Each profile's latest submission is current; a resubmission supersedes
the one before it. Findings are `<profile>:<id>`; a superseded
submission's are `#<n>:<id>`, under `superseded`.

## Flags

| Flag                | Meaning                                                          |
| ------------------- | ---------------------------------------------------------------- |
| `--format <f>`      | `json`, `markdown` (the default without `--json`), or `html`     |
| `--repo <repo>`     | The reviewed repo's id, with `--ref`                             |
| `--ref <sha>`       | The reviewed commit, all 40 characters, with `--repo`            |
| `--json`            | Print the report as JSON; the same as `--format json`            |

## JSON output

```json
{
  "repo": "github.com/bobcob7/buff-bear",
  "ref": "3f2a9c1e…",
  "review": {"version": 1, "repo": "github.com/bobcob7/buff-bear", "ref": "3f2a9c1e…", "profiles": [], "pr": null},
  "agent": {"workspace": "review-buff-bear-42-3f2a9c1-d8efb24f", "status": "idle"},
  "profiles": [
    {"name": "security", "model": "opus", "verdict": "request_changes",
     "severity": {"max": 9, "must_fix": 1, "should_fix": 0, "worth_fixing": 2, "optional": 0}, "missing": false},
    {"name": "go", "model": "", "verdict": null, "severity": null, "missing": true}
  ],
  "comments": [
    {"id": "security:dropped-context-1", "profile": "security", "priority": 9, "category": "correctness",
     "body": "…", "code": null, "anchors": [{"file": "client.go", "line": 3, "end_line": 5}],
     "suggestions": [{"summary": "…", "effort": "trivial", "scope": "line", "pros": [], "cons": [], "code": null}],
     "decision": null}
  ],
  "superseded": [],
  "warnings": []
}
```

- `review`: the record, or null once the workspace is removed. `agent`:
  the review agent's workspace and state (`working`, `starting`, `idle`,
  `stopped`, `unknown`), or null once it's removed.
- `profiles`: the record's, in order, then any other that submitted.
  `missing` means no submission yet; a finished review with missing
  profiles isn't an error.
- `priority` runs from 1 to 10: 9–10 must fix, 7–8 should fix, 4–6 worth
  fixing, 1–3 optional.
- `decision` is always null for now.

## Exit codes

| Situation                                          | Error code         | Exit |
| -------------------------------------------------- | ------------------ | ---- |
| Both or neither of a workspace and `--repo`/`--ref`, a short `--ref`, or an unknown `--format` | `usage` | 2 |
| No such workspace, or it isn't a review            | `review_not_found` | 3    |

## Examples

```console
$ ws review report --json review-buff-bear-42-3f2a9c1-d8efb24f
$ ws review report --json --repo github.com/bobcob7/buff-bear --ref 3f2a9c1e4b5d6f7a8b9c0d1e2f3a4b5c6d7e8f90
$ ws review report --format html review-buff-bear-42-3f2a9c1-d8efb24f > report.html
```
