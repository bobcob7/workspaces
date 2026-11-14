import { describe, expect, it } from "vitest";
import { pullRequest, reviewPlan, reviewRun, reviewsView } from "../../test/fixtures";
import { realReport } from "../../test/reportFixture";
import {
  activeFilterCount,
  authorsOf,
  badgeCount,
  checksSummary,
  commandPreview,
  groupPrs,
  matchesFilter,
  matchesFilters,
  noFilters,
  summary,
  visibleOrder,
  watchedCount,
} from "./groups";

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

describe("summary with a report", () => {
  it("adds what is left to decide after the new commits", () => {
    const pr = pullRequest({ group: "reviewed", outOfDate: true, newCommits: { count: 2, subjects: [] }, reviews: [reviewRun()] });
    expect(summary(pr, realReport)).toBe("buff-bear · alice · 2 new commits · 1 to fix · 2 undecided");
  });

  it("counts the profiles in while reviewing", () => {
    const pr = pullRequest({ group: "reviewing", reviews: [reviewRun({ status: "working" })] });
    expect(summary(pr, realReport)).toBe("buff-bear · alice · 2 of 2 profiles in");
  });

  it("keeps the agent's result without a report", () => {
    const pr = pullRequest({ group: "reviewed", reviews: [reviewRun()] });
    expect(summary(pr, null)).toBe("buff-bear · alice · Two problems worth fixing before merge, one nit.");
  });
});

describe("matchesFilters", () => {
  it("matches the author case-insensitively", () => {
    expect(matchesFilters(pullRequest(), { ...noFilters, author: "ALICE" })).toBe(true);
    expect(matchesFilters(pullRequest(), { ...noFilters, author: "carol" })).toBe(false);
  });

  it("matches each involves choice", () => {
    const mine = pullRequest({ mine: true, requested: false });
    const requested = pullRequest({ mine: false, requested: true });
    expect(matchesFilters(mine, { ...noFilters, involves: "mine" })).toBe(true);
    expect(matchesFilters(requested, { ...noFilters, involves: "mine" })).toBe(false);
    expect(matchesFilters(requested, { ...noFilters, involves: "requested" })).toBe(true);
    expect(matchesFilters(mine, { ...noFilters, involves: "requested" })).toBe(false);
    expect(matchesFilters(mine, { ...noFilters, involves: "meAny" })).toBe(true);
    expect(matchesFilters(requested, { ...noFilters, involves: "meAny" })).toBe(true);
    expect(matchesFilters(pullRequest({ mine: false, requested: false }), { ...noFilters, involves: "meAny" })).toBe(false);
  });

  it("matches the merge status", () => {
    const failing = pullRequest({ merge: { status: "failing", reasons: ["failing"], checks: { total: 1, passed: 0, failed: 1, pending: 0 } } });
    expect(matchesFilters(failing, { ...noFilters, merge: "failing" })).toBe(true);
    expect(matchesFilters(pullRequest(), { ...noFilters, merge: "failing" })).toBe(false);
  });

  it("matches everything without filters", () => {
    expect(matchesFilters(pullRequest({ mine: false, requested: false }), noFilters)).toBe(true);
  });
});

describe("groupPrs with filters", () => {
  const prs = [
    pullRequest({ key: "alice", author: { login: "alice" } }),
    pullRequest({ key: "carol", author: { login: "carol" }, group: "reviewed" }),
  ];

  it("drops pull requests that do not match and groups left empty", () => {
    const groups = groupPrs(prs, "", { ...noFilters, author: "carol" });
    expect(groups.map((g) => g.id)).toEqual(["reviewed"]);
    expect(groups[0]?.prs.map((p) => p.key)).toEqual(["carol"]);
  });

  it("behaves as before without a third argument", () => {
    expect(groupPrs(prs, "")).toEqual(groupPrs(prs, "", noFilters));
  });
});

describe("authorsOf", () => {
  it("returns the distinct logins, sorted", () => {
    const prs = [
      pullRequest({ author: { login: "carol" } }),
      pullRequest({ author: { login: "Alice" } }),
      pullRequest({ author: { login: "carol" } }),
      pullRequest({ author: { login: "bob" } }),
    ];
    expect(authorsOf(prs)).toEqual(["Alice", "bob", "carol"]);
  });
});

describe("activeFilterCount", () => {
  it("counts author, involves other than anyone, and merge", () => {
    expect(activeFilterCount(noFilters)).toBe(0);
    expect(activeFilterCount({ author: "alice", involves: "mine", merge: "ready" })).toBe(3);
    expect(activeFilterCount({ ...noFilters, involves: "requested" })).toBe(1);
  });
});

describe("checksSummary", () => {
  it("is null without checks", () => {
    expect(checksSummary({ total: 0, passed: 0, failed: 0, pending: 0 })).toBeNull();
  });

  it("lists the non-zero parts", () => {
    expect(checksSummary({ total: 7, passed: 5, failed: 1, pending: 1 })).toBe("5 passed, 1 failed, 1 running");
    expect(checksSummary({ total: 2, passed: 2, failed: 0, pending: 0 })).toBe("2 passed");
  });
});

describe("commandPreview for Forgejo", () => {
  it("names the review and its commit instead of the pull request", () => {
    const plan = reviewPlan({ forge: "forgejo", repo: "git.example.com/team/svc", head: "3f2a9c1e0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c" });
    expect(commandPreview(plan)).toBe("ws review start --name review-buff-bear-42-3f2a9c1 git.example.com/team/svc@3f2a9c1");
  });
});

describe("watchedCount", () => {
  it("ignores watched repos without a forge", () => {
    const view = reviewsView({
      repos: [
        { id: "github.com/bobcob7/buff-bear", github: true, forge: "github", watch: true, autoReview: false, open: 1, checkedAtMs: 1, error: null },
        { id: "gitlab.example.com/team/web", github: false, forge: null, watch: true, autoReview: false, open: null, checkedAtMs: null, error: null },
      ],
    });
    expect(watchedCount(view)).toBe(1);
  });
});
