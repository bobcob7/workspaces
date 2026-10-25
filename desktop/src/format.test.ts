import { describe, expect, it } from "vitest";
import { age, count, formatSize, repoLabel } from "./format";

const now = Date.parse("2026-10-18T12:00:00Z");

describe("age", () => {
  it("is empty without a timestamp", () => {
    expect(age(null, now)).toBe("");
  });

  it("says just now under a minute", () => {
    expect(age("2026-10-18T11:59:30Z", now)).toBe("just now");
  });

  it("uses the largest whole unit", () => {
    expect(age("2026-10-18T09:30:00Z", now)).toBe("2h ago");
  });

  it("is empty for an unparseable timestamp", () => {
    expect(age("yesterday", now)).toBe("");
  });
});

describe("repoLabel", () => {
  it("shows the repo name and ref", () => {
    expect(repoLabel("github.com/bobcob7/buff-bear", "main")).toBe("buff-bear@main");
  });
});

describe("formatSize", () => {
  it("uses decimal units with one decimal under 100", () => {
    expect(formatSize(18_432_000)).toBe("18.4 MB");
  });

  it("rounds sizes of 100 or more", () => {
    expect(formatSize(412_345_678)).toBe("412 MB");
  });

  it("drops a trailing .0", () => {
    expect(formatSize(1_000_000_000)).toBe("1 GB");
  });

  it("shows bytes under a kilobyte", () => {
    expect(formatSize(512)).toBe("512 B");
  });
});

describe("count", () => {
  it("is singular for one", () => {
    expect(count(1, "branch", "branches")).toBe("1 branch");
  });

  it("is plural otherwise", () => {
    expect(count(0, "workspace")).toBe("0 workspaces");
  });
});

describe("repoLabel with a commit", () => {
  it("shortens a full commit hash", () => {
    expect(repoLabel("github.com/bobcob7/buff-bear", "9c04e2a7d1b3c5e7f9a1b3c5d7e9f1a3b5c7d9e1")).toBe("buff-bear@9c04e2a");
  });
});
