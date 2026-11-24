import { describe, expect, it } from "vitest";
import { remoteEnvironment, testStore } from "../../test/fixtures";
import { offlineReason, selectEnvironmentChoices, statusIsOld } from "./environments";

const at = (iso: string): number => new Date(iso).getTime();
const hhmm = (iso: string): string => {
  const d = new Date(iso);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
};
const seen = "2026-11-23T14:02:00Z";
const now = at("2026-11-23T14:30:00Z");

describe("offlineReason", () => {
  it("is empty for an online environment", () => {
    expect(offlineReason(remoteEnvironment(), now)).toBe("");
  });

  it("names the last seen time of an offline one", () => {
    const env = remoteEnvironment({ name: "win-rig", status: "offline", lastSeen: seen });
    expect(offlineReason(env, at(seen))).toBe(`win-rig is offline (last seen ${hhmm(seen)})`);
  });

  it("adds the date when it was last seen on another day", () => {
    const env = remoteEnvironment({ name: "win-rig", status: "offline", lastSeen: "2026-11-20T14:02:00Z" });
    expect(offlineReason(env, now)).toMatch(/^win-rig is offline \(last seen 2026-11-\d\d \d\d:\d\d\)$/);
  });

  it("adds the date when it was last seen on the same day of another month", () => {
    const env = remoteEnvironment({ name: "win-rig", status: "offline", lastSeen: "2026-10-23T12:00:00Z" });
    expect(offlineReason(env, now)).toMatch(/^win-rig is offline \(last seen 2026-10-\d\d \d\d:\d\d\)$/);
  });

  it("says a pending environment hasn't enrolled yet", () => {
    const env = remoteEnvironment({ name: "fresh", state: "pending", status: "pending" });
    expect(offlineReason(env, now)).toBe("fresh hasn't enrolled yet");
  });

  it("says the daemon isn't running when the status is unknown", () => {
    const env = remoteEnvironment({ name: "win-rig", status: "unknown" });
    expect(offlineReason(env, now)).toBe("win-rig is offline (the ws daemon isn't running)");
  });

  it("leaves out the time when it was never seen", () => {
    const env = remoteEnvironment({ name: "win-rig", status: "offline", lastSeen: null });
    expect(offlineReason(env, now)).toBe("win-rig is offline");
  });
});

describe("statusIsOld", () => {
  it("is true past 30 s and for a listing never loaded", () => {
    expect(statusIsOld(now - 30_001, now)).toBe(true);
    expect(statusIsOld(null, now)).toBe(true);
  });

  it("is false at 30 s or less", () => {
    expect(statusIsOld(now - 30_000, now)).toBe(false);
    expect(statusIsOld(now, now)).toBe(false);
  });
});

describe("selectEnvironmentChoices", () => {
  it("offers only local when nothing is enrolled", () => {
    expect(selectEnvironmentChoices(testStore().getState(), now)).toEqual([{ name: "local", disabled: false }]);
  });

  it("lists local first, enables online and disables the rest with a reason", () => {
    const store = testStore({
      state: {
        environments: {
          items: [
            remoteEnvironment({ name: "linux-server" }),
            remoteEnvironment({ id: "b", name: "win-rig", status: "offline", lastSeen: seen }),
            remoteEnvironment({ id: "c", name: "fresh", state: "pending", status: "pending" }),
          ],
          loadedAt: now,
          warnings: [],
          error: null,
          latestRequestId: null,
          lastChoice: "local",
        },
      },
    });
    expect(selectEnvironmentChoices(store.getState(), now)).toEqual([
      { name: "local", disabled: false },
      { name: "linux-server", disabled: false },
      { name: "win-rig", disabled: true, reason: offlineReason(remoteEnvironment({ name: "win-rig", status: "offline", lastSeen: seen }), now) },
    ]);
  });
});
