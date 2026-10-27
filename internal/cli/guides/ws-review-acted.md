# ws review acted

```
ws review acted [--json] (<id> | --repo <repo> --ref <sha>) <finding>... (--commented <url> | --fix-sent <agent>)
```

Records that decided findings were acted on, by filling their decision's
`done`: `--commented` for findings decided `comment`, once the review
holding them is posted, and `--fix-sent` for findings decided `fix`, once
they're sent to an agent. A finding with `done` set has been handled;
deciding it again clears `done`.

Findings are named as for `ws review decide`, and each must already be
decided the matching way. All are checked before any is changed.

## Flags

| Flag                  | Meaning                                                  |
| --------------------- | -------------------------------------------------------- |
| `--commented <url>`   | The findings were posted in the review at this URL       |
| `--fix-sent <agent>`  | The findings were sent to this agent's workspace to fix  |
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
      "at": "2026-10-26T18:30:00Z",
      "done": {"fix_sent": "fix-buff-bear-42-3f2a9c1-0c4e1a2b", "at": "2026-10-26T18:31:00Z"}}
  },
  "warnings": []
}
```

`done` has `commented` (the URL) or `fix_sent` (the agent), and `at`.

## Exit codes

| Situation                                          | Error code          | Exit |
| -------------------------------------------------- | ------------------- | ---- |
| Not exactly one of `--commented` and `--fix-sent`; no finding; a finding not decided `comment` (for `--commented`) or `fix` (for `--fix-sent`) | `usage` | 2 |
| No such workspace, or it isn't a review            | `review_not_found`  | 3    |
| A finding isn't in the current report, or is superseded | `finding_not_found` | 3 |

## Examples

```console
$ ws review acted --json review-buff-bear-42-3f2a9c1-d8efb24f security:dropped-context-1 --fix-sent fix-buff-bear-42-3f2a9c1-0c4e1a2b
$ ws review acted review-buff-bear-42-3f2a9c1-d8efb24f go:naked-return-2 --commented https://github.com/bobcob7/buff-bear/pull/42#pullrequestreview-1
```
