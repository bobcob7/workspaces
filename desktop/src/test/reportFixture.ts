import type { ReportComment, ReviewReport } from "../api";

/**
 * `ws review report --json --repo github.com/bobcob7/buff-bear --ref 3f2a9c1…`
 * as ws printed it, for submissions from security and tests (one of them
 * superseded) and one `ws review decide --fix --suggestion 0 --note …`. It
 * pins the report types to ws's output (ADR 0040).
 */
export const realReport: ReviewReport = {
  "repo": "github.com/bobcob7/buff-bear",
  "ref": "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c",
  "review": null,
  "agent": null,
  "profiles": [
    {
      "name": "security",
      "model": "",
      "verdict": "request_changes",
      "severity": {
        "max": 9,
        "must_fix": 1,
        "should_fix": 1,
        "worth_fixing": 0,
        "optional": 0
      },
      "missing": false
    },
    {
      "name": "tests",
      "model": "",
      "verdict": "comment",
      "severity": {
        "max": 2,
        "must_fix": 0,
        "should_fix": 0,
        "worth_fixing": 0,
        "optional": 1
      },
      "missing": false
    }
  ],
  "comments": [
    {
      "id": "security:retry-after-zero-1",
      "profile": "security",
      "priority": 7,
      "category": "correctness",
      "body": "Retry-After is rounded down to 0 for waits under a second.",
      "code": null,
      "anchors": [
        {
          "file": "ratelimit/middleware.go",
          "line": 22,
          "end_line": null
        }
      ],
      "suggestions": [],
      "decision": null
    },
    {
      "id": "security:spoofable-client-ip-1",
      "profile": "security",
      "priority": 9,
      "category": "security",
      "body": "Any client can choose its own bucket by sending X-Forwarded-For. `clientIP` trusts the first entry from any caller.",
      "code": "func clientIP(r *http.Request) string {\n\tif xff := r.Header.Get(\"X-Forwarded-For\"); xff != \"\" {\n\t\treturn strings.Split(xff, \",\")[0]\n\t}",
      "anchors": [
        {
          "file": "ratelimit/ip.go",
          "line": 31,
          "end_line": 34
        },
        {
          "file": "ratelimit/middleware.go",
          "line": 12,
          "end_line": null
        }
      ],
      "suggestions": [
        {
          "summary": "Use r.RemoteAddr, and read X-Forwarded-For only from trusted proxies",
          "effort": "small",
          "scope": "file",
          "pros": [
            "Closes the bypass outright"
          ],
          "cons": [
            "Needs a TRUSTED_PROXIES setting"
          ],
          "code": null
        },
        {
          "summary": "Key buckets by username as well as IP",
          "effort": "medium",
          "scope": "module",
          "pros": [
            "Limits password spraying"
          ],
          "cons": [
            "Lets an attacker lock a user out"
          ],
          "code": null
        }
      ],
      "decision": {
        "decision": "fix",
        "suggestion": 0,
        "note": "trusted proxies only",
        "at": "2026-10-28T13:48:08.168351Z",
        "done": null
      }
    },
    {
      "id": "tests:missing-over-limit-1",
      "profile": "tests",
      "priority": 2,
      "category": "testing",
      "body": "No test drives a request over the limit.",
      "code": null,
      "anchors": [
        {
          "file": "ratelimit/limiter_test.go",
          "line": 0,
          "end_line": null
        }
      ],
      "suggestions": [],
      "decision": null
    }
  ],
  "superseded": [
    {
      "id": "#2:duplicate-name-1",
      "profile": "tests",
      "priority": 2,
      "category": "testing",
      "body": "Two tests share the name TestCache_Get.",
      "code": null,
      "anchors": [
        {
          "file": "session/cache_test.go",
          "line": 30,
          "end_line": null
        }
      ],
      "suggestions": [],
      "decision": null
    }
  ],
  "warnings": []
};

export const reviewReport = (overrides: Partial<ReviewReport> = {}): ReviewReport => ({ ...realReport, ...overrides });

export const reportComment = (overrides: Partial<ReportComment> = {}): ReportComment => ({
  id: "security:a-1",
  profile: "security",
  priority: 5,
  category: "correctness",
  body: "Something is off. More detail follows.",
  code: null,
  anchors: [{ file: "a.go", line: 3, end_line: null }],
  suggestions: [],
  decision: null,
  ...overrides,
});
