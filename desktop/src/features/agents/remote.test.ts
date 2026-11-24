import { describe, expect, it } from "vitest";
import { entry, remoteEnvironment } from "../../test/fixtures";
import { clock } from "../environments/environments";
import { entryActions } from "./remote";

const now = Date.parse("2026-11-23T15:00:00Z");
const remote = (overrides: Parameters<typeof entry>[0] = {}) =>
  entry({ environment: "linux-server", key: "env:linux-server:fix", ...overrides });
const offlineEnv = remoteEnvironment({ status: "offline", lastSeen: "2026-11-23T14:02:00Z" });
const lastSeen = clock("2026-11-23T14:02:00Z", now);

describe("entryActions", () => {
  it("enables everything for a local entry", () => {
    const actions = entryActions(entry(), undefined, now);
    for (const action of Object.values(actions)) {
      expect(action).toEqual({ enabled: true, reason: "" });
    }
  });

  it("a local entry stays enabled even when its flags say offline", () => {
    const actions = entryActions(entry({ environmentOnline: false }), offlineEnv, now);
    expect(actions.attach.enabled).toBe(true);
    expect(actions.remove.enabled).toBe(true);
  });

  describe("an online remote entry", () => {
    const actions = entryActions(remote(), remoteEnvironment(), now);

    it.each(["attach", "stop", "rename", "hide", "remove"] as const)("enables %s", (name) => {
      expect(actions[name]).toEqual({ enabled: true, reason: "" });
    });

    it("disables stopping a ws runner with its reason", () => {
      expect(actions.stopRunner).toEqual({
        enabled: false,
        reason: "Stopping a ws runner on linux-server isn't supported yet",
      });
    });

    it("disables the file actions with their reason", () => {
      expect(actions.openFolder).toEqual({ enabled: false, reason: "The files are on linux-server" });
    });

    it("disables the conversation with its note", () => {
      expect(actions.conversation).toEqual({
        enabled: false,
        reason: "The conversation of an agent on linux-server isn't shown here yet. Use Terminal.",
      });
    });
  });

  describe("an offline remote entry", () => {
    const actions = entryActions(remote({ environmentOnline: false }), offlineEnv, now);
    const reason = `linux-server is offline (last seen ${lastSeen})`;

    it.each(["attach", "stop", "rename", "hide", "remove"] as const)("disables %s with the offline reason", (name) => {
      expect(actions[name]).toEqual({ enabled: false, reason });
    });

    it("keeps the other reasons", () => {
      expect(actions.stopRunner.reason).toBe("Stopping a ws runner on linux-server isn't supported yet");
      expect(actions.openFolder.reason).toBe("The files are on linux-server");
    });

    it("names the environment when it isn't listed", () => {
      const unlisted = entryActions(remote({ environmentOnline: false }), undefined, now);
      expect(unlisted.attach).toEqual({ enabled: false, reason: "linux-server is offline" });
    });

    it("explains a missing daemon", () => {
      const noDaemon = entryActions(remote({ environmentOnline: false }), remoteEnvironment({ status: "unknown" }), now);
      expect(noDaemon.stop.reason).toBe("linux-server is offline (the ws daemon isn't running)");
    });
  });

  describe("a stale remote entry of an online environment", () => {
    const actions = entryActions(remote({ snapshotStale: true }), remoteEnvironment(), now);

    it.each(["attach", "stop", "rename", "hide", "remove"] as const)("keeps %s enabled", (name) => {
      expect(actions[name]).toEqual({ enabled: true, reason: "" });
    });
  });
});
