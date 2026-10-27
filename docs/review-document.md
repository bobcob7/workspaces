# The review document

A review is one JSON object, written by a reviewer and checked by
[`ws review submit`](tools/ws-review.md#ws-review-submit). Its JSON Schema
is [`internal/review/schema/review.schema.json`](../internal/review/schema/review.schema.json);
this page describes the same format for people. Reviewers read it a piece
at a time with `ws review describe`, which prints the same fields and
explains every check
([0037](adrs/0037-review-engine-forked-from-loam-refinery.md)).

Unknown fields are rejected at every level.

## Example

```json
{
  "version": "1",
  "verdict": "request_changes",
  "profile": "go",
  "summary": "The retry loop drops the caller's deadline, so cancellation never reaches the client.",
  "comments": [
    {
      "id": "dropped-context-1",
      "priority": 9,
      "category": "correctness",
      "body": "c.do ignores the caller's ctx, so cancellation never propagates.",
      "anchors": [{"file": "internal/fetch/client.go", "line": 88, "end_line": 94}],
      "suggestions": [
        {
          "summary": "Pass the caller's ctx through to c.do",
          "effort": "trivial",
          "scope": "line",
          "pros": ["Deadlines propagate"],
          "cons": ["Callers see a change"]
        }
      ]
    }
  ]
}
```

## Fields

### Root

| Field | Required | Value |
| --- | --- | --- |
| `version` | yes | `"1"` |
| `verdict` | yes | `approve`, `request_changes`, or `comment` |
| `summary` | yes | 1–3 sentences, 30–1500 characters |
| `ref` | no in `ws` | the reviewed commit, 40 lowercase hex characters. `ws review submit` fills it in from the review workspace's record; if given, it must be that commit |
| `profile` | yes in `ws` | the profile the reviewer was primed with; one of the review's profiles |
| `comments` | yes | the findings; empty only when `verdict` is `approve` |
| `assessment` | no | `strong`, `sound`, `mixed`, or `weak`: the reviewer's grade of the change, separate from the verdict |

The schema itself still requires `ref` and leaves `profile` optional;
`ws review submit` adds `ref` before checking and requires `profile`
itself.

### Comment

| Field | Required | Value |
| --- | --- | --- |
| `id` | yes | a kebab-case slug naming the kind of finding, then a number: `missing-context-2`. Unique in the document; numbers per slug run from 1 |
| `priority` | yes | 1–10: 9–10 blocks merging, 7–8 should be fixed, 4–6 is worth fixing, 1–3 is optional |
| `category` | yes | `correctness`, `security`, `performance`, `maintainability`, `testing`, `documentation`, or `style` |
| `body` | yes | the finding and why it matters, 20–4000 characters |
| `code` | no | an excerpt of the problem |
| `anchors` | yes | where it applies, possibly empty; see below |
| `suggestions` | yes | ways to fix it, possibly empty; see below |

### Anchor

| Field | Required | Value |
| --- | --- | --- |
| `file` | yes | a path relative to the repo root, with `/` separators |
| `line` | no | the first line, from 1 |
| `end_line` | no | the last line, at least `line` |

Anchors are claims about the reviewed commit, and `ws review submit`
checks them against the review workspace's repo: the file must exist at
the commit and have the lines named.

### Suggestion

| Field | Required | Value |
| --- | --- | --- |
| `summary` | yes | what to do, one sentence, 10–500 characters |
| `effort` | yes | `trivial`, `small`, `medium`, or `large`: how much work |
| `scope` | yes | `line`, `block`, `file`, `module`, or `project`: how far the change reaches |
| `pros` | yes | strings |
| `cons` | yes | strings |
| `code` | no | an excerpt of the fix |

A comment can carry several suggestions that compete; the person
triaging the finding picks one
([0040](adrs/0040-review-decisions-kept-with-findings.md)).

## Checks

Each check has a name, which a diagnostic reports and `ws review describe
--lens=<name>` explains.

- **Structural** checks ask whether this is a document: the schema, unique
  and well-formed ids, safe paths. They always run and always fail the
  submission.
- **Verification** checks ask whether it's about anything real: the
  anchors at the commit, and, in `ws`, `review-ref` and `review-profile`
  against the review workspace's record. A failure is `review_invalid`;
  anchors that can't be checked at all (a changed checkout, a missing
  commit) are `review_unverifiable`.
- **Advisory** checks ask whether it's a good review: a thin body, a
  suggestion with no stated cost, every comment at priority 9. They're
  reported and never fail the submission.
