import { describe, expect, it } from "vitest";
import { age, repoLabel } from "./format";

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
