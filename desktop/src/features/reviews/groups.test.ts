import { describe, expect, it } from "vitest";
import { pullRequest, reviewPlan, reviewRun, reviewsView } from "../../test/fixtures";
import { badgeCount, commandPreview, groupPrs, matchesFilter, summary, visibleOrder } from "./groups";

describe("groupPrs", () => {
  it("orders groups To review, Reviewing, Reviewed, Dismissed", () => {
    const prs = [
      pullRequest({ key: "d", group: "dismissed" }),
      pullRequest({ key: "r", group: "reviewed" }),
      pullRequest({ key: "t", group: "toReview" }),
      pullRequest({ key: "w", group: "reviewing" }),
    ];
    expect(groupPrs(prs, "").map((g) => g.id)).toEqual(["toReview", "reviewing", "reviewed", "dismissed"]);
  });

  it("puts the newest first within a group", () => {
    const prs = [
      pullRequest({ key: "old", updatedAt: "2026-10-20T00:00:00Z" }),
      pullRequest({ key: "new", updatedAt: "2026-10-24T00:00:00Z" }),
    ];
    expect(groupPrs(prs, "")[0]?.prs.map((p) => p.key)).toEqual(["new", "old"]);
  });

  it("leaves out groups with nothing in them", () => {
    expect(groupPrs([pullRequest()], "")).toHaveLength(1);
  });
});

describe("matchesFilter", () => {
  it("matches the number with or without #", () => {
    expect(matchesFilter(pullRequest({ number: 42 }), "#42")).toBe(true);
    expect(matchesFilter(pullRequest({ number: 42 }), "4")).toBe(false);
  });

  it("matches title, author, and repo case-insensitively", () => {
    const pr = pullRequest();
    expect(matchesFilter(pr, "RATE")).toBe(true);
    expect(matchesFilter(pr, "alice")).toBe(true);
    expect(matchesFilter(pr, "buff-bear")).toBe(true);
    expect(matchesFilter(pr, "carol")).toBe(false);
  });
});

describe("summary", () => {
  it("says when your review is requested", () => {
    expect(summary(pullRequest())).toBe("buff-bear · alice · review requested");
  });

  it("says when a review agent needs input", () => {
    const pr = pullRequest({ group: "reviewing", reviews: [reviewRun({ status: "needsInput" })] });
    expect(summary(pr)).toBe("buff-bear · alice · needs input");
  });

  it("counts new commits on an out-of-date review", () => {
    const pr = pullRequest({ group: "reviewed", outOfDate: true, newCommits: { count: 2, subjects: ["a", "b"] } });
    expect(summary(pr)).toBe("buff-bear · alice · 2 new commits");
  });

  it("shows the first line of a finished review's result", () => {
    const pr = pullRequest({ group: "reviewed", reviews: [reviewRun({ result: "3 findings\nmore" })] });
    expect(summary(pr)).toBe("buff-bear · alice · 3 findings");
  });
});

describe("badgeCount", () => {
  it("counts To review and out-of-date Reviewed", () => {
    const view = reviewsView({
      prs: [
        pullRequest({ key: "a" }),
        pullRequest({ key: "b", group: "reviewed", outOfDate: true }),
        pullRequest({ key: "c", group: "reviewed" }),
        pullRequest({ key: "d", group: "reviewing" }),
        pullRequest({ key: "e", group: "dismissed" }),
      ],
    });
    expect(badgeCount(view)).toBe(2);
  });
});

describe("visibleOrder", () => {
  it("skips dismissed pull requests while their group is collapsed", () => {
    const groups = groupPrs([pullRequest({ key: "t" }), pullRequest({ key: "d", group: "dismissed" })], "");
    expect(visibleOrder(groups, false)).toEqual(["t"]);
    expect(visibleOrder(groups, true)).toEqual(["t", "d"]);
  });
});

describe("commandPreview", () => {
  it("shows ws review start for the pull request", () => {
    expect(commandPreview(reviewPlan())).toBe("ws review start --pr 42 github.com/bobcob7/buff-bear");
  });

  it("includes the profiles, agent options, and the start of the focus", () => {
    const plan = reviewPlan({
      profiles: [
        { name: "security", model: "opus", auto: false },
        { name: "go", model: "", auto: true },
      ],
      model: "opus",
      permissionMode: "plan",
      focus: "Look hard at the SQL\nand the migrations",
    });
    expect(commandPreview(plan)).toBe(
      'ws review start --pr 42 github.com/bobcob7/buff-bear --profile security:opus --auto go -- --model=opus --permission-mode=plan "Look hard at the SQL"',
    );
  });
});
