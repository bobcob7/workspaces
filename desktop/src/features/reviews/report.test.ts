import { describe, expect, it } from "vitest";
import type { FindingDecision } from "../../api";
import { realReport, reportComment, reviewReport } from "../../test/reportFixture";
import {
  anchorLabel,
  anchorUrl,
  bandCount,
  bandOf,
  clickedDecision,
  decidedEarlier,
  findingTitle,
  groupFindings,
  keyAction,
  noFilter,
  reportSummary,
  severityText,
  tally,
  tallyParts,
  visibleFindings,
} from "./report";

const decided = (decision: FindingDecision["decision"], done: FindingDecision["done"] = null): FindingDecision => ({
  decision,
  suggestion: null,
  note: "",
  at: "2026-10-26T18:30:00Z",
  done,
});

const ids = (report = realReport, filter = noFilter): readonly (readonly string[])[] =>
  groupFindings(report, filter).map((g) => g.findings.map((f) => f.comment.id));

describe("bandOf", () => {
  it("follows the priority ladder", () => {
    expect([10, 9, 8, 7, 6, 4, 3, 1].map(bandOf)).toEqual([
      "must",
      "must",
      "should",
      "should",
      "worth",
      "worth",
      "optional",
      "optional",
    ]);
  });
});

describe("groupFindings", () => {
  it("groups ws's report by band, in band order", () => {
    expect(groupFindings(realReport, noFilter).map((g) => g.band.id)).toEqual(["must", "should", "optional"]);
    expect(ids()).toEqual([["security:spoofable-client-ip-1"], ["security:retry-after-zero-1"], ["tests:missing-over-limit-1"]]);
  });

  it("orders a band by priority, then profile", () => {
    const report = reviewReport({
      comments: [
        reportComment({ id: "tests:a-1", profile: "tests", priority: 5 }),
        reportComment({ id: "security:b-1", profile: "security", priority: 4 }),
        reportComment({ id: "go:c-1", profile: "go", priority: 5 }),
      ],
    });
    expect(ids(report)).toEqual([["go:c-1", "tests:a-1", "security:b-1"]]);
  });

  it("filters by profile and category", () => {
    expect(ids(realReport, { ...noFilter, profile: "tests" })).toEqual([[], [], ["tests:missing-over-limit-1"]]);
    expect(ids(realReport, { ...noFilter, category: "security" })).toEqual([["security:spoofable-client-ip-1"], [], []]);
  });

  it("hides decided findings", () => {
    expect(ids(realReport, { ...noFilter, hideDecided: true })[0]).toEqual([]);
  });

  it("leaves superseded findings out until shown, then marks them read-only", () => {
    expect(ids().flat()).not.toContain("#2:duplicate-name-1");
    const optional = groupFindings(realReport, { ...noFilter, showSuperseded: true }).at(-1);
    expect(optional?.findings.map((f) => [f.comment.id, f.readOnly])).toEqual([
      ["tests:missing-over-limit-1", false],
      ["#2:duplicate-name-1", true],
    ]);
    expect(optional?.total).toBe(1);
  });
});

describe("bandCount", () => {
  it("is the count without a filter", () => {
    const [must] = groupFindings(realReport, noFilter);
    expect(must !== undefined && bandCount(must, noFilter)).toBe("1");
  });

  it("is `n of total` with a filter on", () => {
    const filter = { ...noFilter, hideDecided: true };
    const [must] = groupFindings(realReport, filter);
    expect(must !== undefined && bandCount(must, filter)).toBe("0 of 1");
  });
});

describe("visibleFindings", () => {
  it("skips a collapsed Optional band", () => {
    const groups = groupFindings(realReport, noFilter);
    expect(visibleFindings(groups, false)).toHaveLength(2);
    expect(visibleFindings(groups, true)).toHaveLength(3);
  });
});

describe("findingTitle", () => {
  it("is the first sentence without Markdown or the period", () => {
    expect(findingTitle("`clientIP` trusts *any* caller. So the limit never applies.")).toBe("clientIP trusts any caller");
  });

  it("keeps a body without a sentence end whole", () => {
    expect(findingTitle("\n\nNo test drives a request over the limit\n\nMore.")).toBe("No test drives a request over the limit");
  });

  it("doesn't stop at a dot inside a word", () => {
    expect(findingTitle("ip.go trusts headers. More.")).toBe("ip.go trusts headers");
  });
});

describe("anchors", () => {
  it("labels lines, ranges, and whole files", () => {
    expect(anchorLabel({ file: "ratelimit/ip.go", line: 31, end_line: 34 })).toBe("ratelimit/ip.go:31-34");
    expect(anchorLabel({ file: "ratelimit/ip.go", line: 31, end_line: null })).toBe("ratelimit/ip.go:31");
    expect(anchorLabel({ file: "ratelimit/limiter_test.go", line: 0, end_line: null })).toBe("ratelimit/limiter_test.go");
  });

  it("links to the file and lines at the reviewed commit on GitHub", () => {
    const ref = realReport.ref;
    expect(anchorUrl(realReport.repo, ref, { file: "ratelimit/ip.go", line: 31, end_line: 34 })).toBe(
      `https://github.com/bobcob7/buff-bear/blob/${ref}/ratelimit/ip.go#L31-L34`,
    );
    expect(anchorUrl(realReport.repo, ref, { file: "a b.go", line: 0, end_line: null })).toBe(
      `https://github.com/bobcob7/buff-bear/blob/${ref}/a%20b.go`,
    );
  });
});

describe("severityText", () => {
  it("counts findings by band", () => {
    expect(realReport.profiles.map(severityText)).toEqual(["1 must · 1 should", "1 optional"]);
  });

  it("says when a profile found nothing", () => {
    const profile = { ...realReport.profiles[0], severity: { max: null, must_fix: 0, should_fix: 0, worth_fixing: 0, optional: 0 } };
    expect(severityText(profile as (typeof realReport.profiles)[0])).toBe("no findings");
  });
});

describe("clickedDecision", () => {
  it("sets a new decision and clears the selected one", () => {
    expect(clickedDecision(null, "fix")).toBe("fix");
    expect(clickedDecision("comment", "fix")).toBe("fix");
    expect(clickedDecision("fix", "fix")).toBe("clear");
  });
});

describe("tally", () => {
  const report = reviewReport({
    comments: [
      reportComment({ id: "a:1", priority: 9, decision: decided("comment") }),
      reportComment({ id: "a:2", priority: 9, decision: decided("fix") }),
      reportComment({ id: "a:3", priority: 7, decision: decided("fix", { fix_sent: "fix-login", at: "t" }) }),
      reportComment({ id: "a:4", priority: 5, decision: decided("skip") }),
      reportComment({ id: "a:5", priority: 2 }),
    ],
    superseded: [reportComment({ id: "#1:x", priority: 2 })],
  });

  it("counts decisions, done, and hidden optional findings", () => {
    const groups = groupFindings(report, noFilter);
    expect(tally(report, groups, false)).toEqual({
      comment: 1,
      fix: 1,
      skip: 1,
      undecided: 1,
      done: 1,
      readOnly: 0,
      optionalHidden: 1,
    });
  });

  it("counts read-only findings when shown, and nothing hidden with Optional open", () => {
    const groups = groupFindings(report, { ...noFilter, showSuperseded: true });
    const t = tally(report, groups, true);
    expect([t.readOnly, t.optionalHidden]).toEqual([1, 0]);
  });

  it("lists only non-zero parts, in order", () => {
    const parts = tallyParts({ comment: 2, fix: 0, skip: 1, undecided: 0, done: 2, readOnly: 1, optionalHidden: 0 });
    expect(parts.map((p) => `${p.count} ${p.label}`)).toEqual(["2 to comment", "1 skipped", "2 done", "1 read-only (superseded)"]);
  });
});

describe("reportSummary", () => {
  it("counts the profiles in while the agent works", () => {
    const report = reviewReport({ profiles: [...realReport.profiles, { ...realReport.profiles[0], name: "go", missing: true } as never] });
    expect(reportSummary(report, true)).toBe("2 of 3 profiles in");
  });

  it("names the worst band and the findings before any decision", () => {
    const report = reviewReport({ comments: realReport.comments.map((c) => ({ ...c, decision: null })) });
    expect(reportSummary(report, false)).toBe("1 must fix · 3 findings");
  });

  it("names the decisions left once you've decided", () => {
    expect(reportSummary(realReport, false)).toBe("1 to fix · 2 undecided");
  });

  it("is all done when nothing is left", () => {
    const report = reviewReport({ comments: [reportComment({ decision: decided("skip") })] });
    expect(reportSummary(report, false)).toBe("all done");
  });

  it("says when there are no findings", () => {
    expect(reportSummary(reviewReport({ comments: [] }), false)).toBe("no findings");
  });
});

describe("decidedEarlier", () => {
  it("points each id decided in an older review at its commit, newest first", () => {
    const older = [
      reviewReport({ ref: "a41c9f2" + "0".repeat(33), comments: [reportComment({ id: "s:a-1", decision: decided("fix") })] }),
      reviewReport({ ref: "9c04e2a" + "0".repeat(33), comments: [reportComment({ id: "s:a-1", decision: decided("skip") }), reportComment({ id: "s:b-1" })] }),
    ];
    expect([...decidedEarlier(older)]).toEqual([["s:a-1", "a41c9f2"]]);
  });
});

describe("keyAction", () => {
  it("maps the report's keys", () => {
    expect(keyAction("ArrowDown")).toEqual({ kind: "move", by: 1 });
    expect(keyAction("ArrowUp")).toEqual({ kind: "move", by: -1 });
    expect(keyAction("Enter")).toEqual({ kind: "toggle" });
    expect(keyAction(" ")).toEqual({ kind: "toggle" });
    expect(keyAction("c")).toEqual({ kind: "decide", decision: "comment" });
    expect(keyAction("F")).toEqual({ kind: "decide", decision: "fix" });
    expect(keyAction("s")).toEqual({ kind: "decide", decision: "skip" });
    expect(keyAction("3")).toEqual({ kind: "suggest", index: 2 });
    expect(keyAction("Backspace")).toEqual({ kind: "clear" });
    expect(keyAction("0")).toBeNull();
    expect(keyAction("x")).toBeNull();
  });
});
