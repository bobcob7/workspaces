import { afterEach, describe, expect, it, vi } from "vitest";
import { readDetailsExpanded, writeDetailsExpanded } from "./detailsPreference";

describe("details preference", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("is expanded when nothing is stored", () => {
    expect(readDetailsExpanded()).toBe(true);
  });

  it("remembers a collapsed choice", () => {
    writeDetailsExpanded(false);
    expect(readDetailsExpanded()).toBe(false);
  });

  it("remembers an expanded choice after a collapse", () => {
    writeDetailsExpanded(false);
    writeDetailsExpanded(true);
    expect(readDetailsExpanded()).toBe(true);
  });

  it("falls back to expanded when storage can't be read", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("storage disabled");
    });
    expect(readDetailsExpanded()).toBe(true);
  });

  it("doesn't throw when storage can't be written", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("storage full");
    });
    expect(() => writeDetailsExpanded(false)).not.toThrow();
  });
});
