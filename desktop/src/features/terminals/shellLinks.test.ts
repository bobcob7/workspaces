import { describe, expect, it } from "vitest";
import type { Entry, TermSpec } from "../../api";
import { entry, fakeApi, testStore } from "../../test/fixtures";
import { fetchEntries } from "../agents/agentsSlice";
import { selected } from "../ui/uiSlice";
import { shellLinks } from "./shellLinks";
import { openTerminal, type TerminalInfo } from "./terminalsSlice";

const openedAt = Date.parse("2026-10-25T10:00:00Z");

const shell = (key: string, name: string, overrides: Partial<TerminalInfo> = {}): TerminalInfo => {
  const spec: TermSpec = { kind: "shell", name, specs: [] };
  return { key, title: name, spec, state: "open", exitCode: null, lastUsed: 1, generation: 1, workspaceId: null, openedAt, ...overrides };
};

const shellEntry = (id: string, name: string, createdAt = "2026-10-25T10:00:01Z"): Entry =>
  entry({ key: id, kind: "shell", workspaceId: id, name, sessionId: null, status: "running", createdAt });

describe("shellLinks", () => {
  it("links a named shell to the new workspace with that name", () => {
    const links = shellLinks([shell("shell:t1", "This is a Test")], [shellEntry("this-is-a-test-d8efb24f", "This is a Test")]);
    expect(links).toEqual([{ key: "shell:t1", workspaceId: "this-is-a-test-d8efb24f" }]);
  });

  it("links an unnamed shell to a new workspace without a name", () => {
    const links = shellLinks([shell("shell:t1", "")], [shellEntry("x-00000001", "Named"), shellEntry("ws-20261025-k3f9qa", "ws-20261025-k3f9qa")]);
    expect(links).toEqual([{ key: "shell:t1", workspaceId: "ws-20261025-k3f9qa" }]);
  });

  it("skips workspaces created before the terminal opened", () => {
    const links = shellLinks([shell("shell:t1", "api")], [shellEntry("api-00000001", "api", "2026-10-25T09:00:00Z")]);
    expect(links).toEqual([]);
  });

  it("skips workspaces another terminal has", () => {
    const linked = shell("shell:t0", "api", { workspaceId: "api-00000001" });
    const links = shellLinks([linked, shell("shell:t1", "api")], [shellEntry("api-00000001", "api"), shellEntry("api-00000002", "api", "2026-10-25T10:00:02Z")]);
    expect(links).toEqual([{ key: "shell:t1", workspaceId: "api-00000002" }]);
  });

  it("gives two shells with one name a workspace each, oldest first", () => {
    const links = shellLinks(
      [shell("shell:t2", "api", { openedAt: openedAt + 500 }), shell("shell:t1", "api")],
      [shellEntry("api-00000002", "api", "2026-10-25T10:00:02Z"), shellEntry("api-00000001", "api")],
    );
    expect(links).toEqual([
      { key: "shell:t1", workspaceId: "api-00000001" },
      { key: "shell:t2", workspaceId: "api-00000002" },
    ]);
  });

  it("never links agents", () => {
    const agent = entry({ key: "api-00000001", workspaceId: "api-00000001", name: "api", createdAt: "2026-10-25T10:00:01Z" });
    expect(shellLinks([shell("shell:t1", "api")], [agent])).toEqual([]);
  });
});

describe("shellLinkListener", () => {
  it("links the terminal and moves the selection to its workspace once listed", async () => {
    const workspace = shellEntry("this-is-a-test-d8efb24f", "This is a Test", new Date(Date.now() + 1000).toISOString());
    const store = testStore({ api: fakeApi({ listEntries: async () => ({ entries: [workspace], warnings: [] }) }) });
    await store.dispatch(
      openTerminal({ key: "shell:t1", title: "This is a Test", spec: { kind: "shell", name: "This is a Test", specs: [] }, openedAt: Date.now() }),
    );
    store.dispatch(selected("shell:t1"));
    await store.dispatch(fetchEntries());
    expect(store.getState().terminals.items[0]?.workspaceId).toBe("this-is-a-test-d8efb24f");
    expect(store.getState().ui.selectedKey).toBe("this-is-a-test-d8efb24f");
  });
});
