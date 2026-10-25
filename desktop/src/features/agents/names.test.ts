import { describe, expect, it } from "vitest";
import { randomToken } from "./names";

describe("randomToken", () => {
  it("draws eight characters from the alphabet", () => {
    expect(randomToken(() => 0.999)).toBe("99999999");
  });
});
