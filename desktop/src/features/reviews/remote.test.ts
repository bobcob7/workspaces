import { describe, expect, it } from "vitest";
import { remoteEnvironment } from "../../test/fixtures";
import { environmentBlock, reviewPermissionModes } from "./remote";

const now = Date.parse("2026-11-23T14:30:00Z");
const items = [
  remoteEnvironment({ name: "up" }),
  remoteEnvironment({ id: "b", name: "down", status: "offline", lastSeen: null }),
  remoteEnvironment({ id: "c", name: "stale", status: "unknown" }),
];

describe("environmentBlock", () => {
  it("never blocks local", () => {
    expect(environmentBlock([], "local", now)).toBe("");
    expect(environmentBlock(items, "local", now)).toBe("");
  });

  it("does not block an online environment", () => {
    expect(environmentBlock(items, "up", now)).toBe("");
  });

  it("blocks an offline environment with its reason", () => {
    expect(environmentBlock(items, "down", now)).toBe("down is offline");
  });

  it("blocks an environment whose daemon is not running", () => {
    expect(environmentBlock(items, "stale", now)).toBe("stale is offline (the ws daemon isn't running)");
  });

  it("does not block an environment that is not listed", () => {
    expect(environmentBlock(items, "elsewhere", now)).toBe("");
  });
});

describe("reviewPermissionModes", () => {
  it("drops bypassPermissions from a remote review only", () => {
    const modes = ["", "plan", "bypassPermissions"];
    expect(reviewPermissionModes(modes, "up")).toEqual(["", "plan"]);
    expect(reviewPermissionModes(modes, "local")).toEqual(modes);
  });
});
