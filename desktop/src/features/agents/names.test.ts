import { describe, expect, it } from "vitest";
import { randomSuffix, sanitizeName } from "./names";

describe("sanitizeName matches ws's ids", () => {
  it.each([
    ["Fix Login", "fix-login"],
    ["café-x", "cafe-x"],
    ["Ångström", "angstrom"],
    ["ﬁle №5", "file-no5"],
    ["a.b_c", "a-b-c"],
    ["Straße", "stra-e"],
    ["  --x--  ", "x"],
    ["🚀 launch", "launch"],
  ])("turns %j into %j", (name, id) => {
    expect(sanitizeName(name)).toBe(id);
  });

  it("cuts ids to 64 characters without a trailing dash", () => {
    expect(sanitizeName(`${"a".repeat(63)} b`)).toBe("a".repeat(63));
  });
});

describe("randomSuffix", () => {
  it("draws six characters from the id alphabet", () => {
    expect(randomSuffix(() => 0.999)).toBe("999999");
  });
});
