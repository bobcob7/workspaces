import { describe, expect, it, vi } from "vitest";
import type { Entry } from "../../api";
import { entry, fakeApi, fakeHost, testStore, treeEntries } from "../../test/fixtures";
import { openTerminal } from "../terminals/terminalsSlice";
import { agentFocused, selected } from "../ui/uiSlice";
import {
  agentsReducer,
  createAgent,
  editorDir,
  fetchEntries,
  filtersCleared,
  hiddenShown,
  nodeToggled,
  openWorkspace,
  queryChanged,
  removeEntry,
  renameEntry,
  stopAgent,
  statusFilterChanged,
  type AgentsState,
} from "./agentsSlice";

const repo = (path: string): Entry["repos"][number] => ({
  id: `github.com/bobcob7/${path}`,
  path,
  ref: "main",
  refKind: "branch",
});

const initial = (): AgentsState => agentsReducer(undefined, { type: "init" });

const withTree = (): AgentsState =>
  agentsReducer(initial(), fetchEntries.fulfilled({ entries: treeEntries(), warnings: [] }, "req"));

const withExpanded = (expanded: Readonly<Record<string, boolean>>): AgentsState => ({ ...initial(), expanded });

describe("agentsReducer", () => {
  it("stores the listing when a fetch succeeds", () => {
    const listing = { entries: [entry()], warnings: ["w"] };
    const state = agentsReducer(initial(), fetchEntries.fulfilled(listing, "req"));
    expect(state).toMatchObject({ entries: listing.entries, warnings: ["w"], loaded: true, loading: false });
  });

  it("keeps the last entries when a fetch fails", () => {
    const loaded = agentsReducer(initial(), fetchEntries.fulfilled({ entries: [entry()], warnings: [] }, "r1"));
    const error = { code: "ws_missing", message: "no ws" };
    const state = agentsReducer(loaded, fetchEntries.rejected(null, "r2", undefined, error));
    expect(state.entries).toHaveLength(1);
    expect(state.error).toEqual(error);
  });

  it("marks an entry busy while it is being stopped", () => {
    const e = entry();
    const state = agentsReducer(initial(), stopAgent.pending("req", e));
    expect(state.busy).toEqual([e.key]);
  });

  it("clears busy when removing finishes", () => {
    const e = entry();
    const busy = agentsReducer(initial(), removeEntry.pending("req", e));
    expect(agentsReducer(busy, removeEntry.fulfilled(undefined, "req", e)).busy).toEqual([]);
  });

  it("marks an entry busy while it is being renamed", () => {
    const e = entry();
    const busy = agentsReducer(initial(), renameEntry.pending("req", { entry: e, name: "x" }));
    expect(busy.busy).toEqual([e.key]);
    expect(agentsReducer(busy, renameEntry.fulfilled(undefined, "req", { entry: e, name: "x" })).busy).toEqual([]);
  });

  it("records a creation error", () => {
    const error = { code: "claude_untrusted", message: "not trusted" };
    const request = { name: "", specs: [], prompt: "", model: "", permissionMode: "", offline: false };
    const state = agentsReducer(initial(), createAgent.rejected(null, "req", request, error));
    expect(state.createError).toEqual(error);
  });
});

describe("thunks", () => {
  it("does not fetch while a fetch is in flight", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await Promise.all([store.dispatch(fetchEntries()), store.dispatch(fetchEntries())]);
    expect(api.listEntries).toHaveBeenCalledTimes(1);
  });

  it("selects the new workspace after creating an agent", async () => {
    const store = testStore();
    await store.dispatch(createAgent({ name: "x", specs: [], prompt: "go", model: "", permissionMode: "", offline: false }));
    expect(store.getState().ui.selectedKey).toBe("new");
  });

  it("renames by workspace id and refreshes the list", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(renameEntry({ entry: entry({ workspaceId: "fix-login-d8efb24f" }), name: "Login fix" }));
    expect(api.renameWorkspace).toHaveBeenCalledWith("fix-login-d8efb24f", "Login fix");
    expect(api.listEntries).toHaveBeenCalled();
    expect(store.getState().ui.notice?.severity).toBe("success");
  });

  it("shows a failed rename as an error", async () => {
    const api = fakeApi({
      renameWorkspace: vi.fn(async () => {
        throw { code: "name_invalid", message: "a name can't be empty" };
      }),
    });
    const store = testStore({ api });
    await store.dispatch(renameEntry({ entry: entry(), name: " " }));
    expect(store.getState().ui.notice).toMatchObject({ severity: "error", message: "a name can't be empty" });
  });

  it("removes by workspace id and session id", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(removeEntry(entry()));
    expect(api.removeEntry).toHaveBeenCalledWith("fix-login", "e8cdf5d8", false);
  });

  it("removes keeping the workspaces it started", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(removeEntry({ ...entry(), keepChildren: true }));
    expect(api.removeEntry).toHaveBeenCalledWith("fix-login", "e8cdf5d8", true);
  });

  it("closes the agent's terminal before removing it", async () => {
    const terminals = fakeHost();
    const store = testStore({ terminals });
    const spec = { kind: "attach", sessionId: "e8cdf5d8", cwd: "/w" } as const;
    await store.dispatch(openTerminal({ key: "attach:e8cdf5d8", title: "x", spec }));
    await store.dispatch(removeEntry(entry()));
    expect(terminals.close).toHaveBeenCalledWith("attach:e8cdf5d8");
  });

  it("deselects a removed entry", async () => {
    const store = testStore();
    store.dispatch(selected("fix-login"));
    await store.dispatch(removeEntry(entry()));
    expect(store.getState().ui.selectedKey).toBeNull();
  });

  it("shows a failed stop as an error notice", async () => {
    const api = fakeApi({ stopAgent: vi.fn(async () => Promise.reject({ code: "claude_failed", message: "boom" })) });
    const store = testStore({ api });
    await store.dispatch(stopAgent(entry()));
    expect(store.getState().ui.notice).toEqual({ severity: "error", message: "boom" });
  });
});

describe("editorDir", () => {
  it("opens the workspace when it has no repos", () => {
    expect(editorDir(entry({ path: "/w/fix-login", repos: [] }))).toBe("/w/fix-login");
  });

  it("opens the only repo when the workspace has one", () => {
    expect(editorDir(entry({ path: "/w/fix-login", repos: [repo("buff-bear")] }))).toBe("/w/fix-login/buff-bear");
  });

  it("opens the workspace when it has several repos", () => {
    expect(editorDir(entry({ path: "/w/fix-login", repos: [repo("buff-bear"), repo("api")] }))).toBe("/w/fix-login");
  });
});

describe("openWorkspace", () => {
  it("passes the workspace path and repo paths", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(openWorkspace({ entry: entry({ repos: [repo("buff-bear"), repo("api")] }), with: "editor" }));
    expect(api.workspaceOpen).toHaveBeenCalledWith("editor", "/w/fix-login", ["buff-bear", "api"]);
  });

  it("shows a failure as an error notice", async () => {
    const api = fakeApi({
      workspaceOpen: vi.fn(async () => Promise.reject({ code: "open_program_missing", message: "`code` isn't on the app's PATH." })),
    });
    const store = testStore({ api });
    await store.dispatch(openWorkspace({ entry: entry(), with: "editor" }));
    expect(store.getState().ui.notice).toEqual({
      severity: "error",
      message: "Couldn't open fix-login: `code` isn't on the app's PATH.",
    });
  });
});

describe("tree state", () => {
  it("starts with nothing expanded, an empty query and every status", () => {
    expect(initial()).toMatchObject({ expanded: {}, query: "", statusFilter: "all", showHidden: false });
  });

  it("keeps the last open or closed choice for a node", () => {
    const opened = agentsReducer(initial(), nodeToggled({ key: "orch", open: true }));
    const closed = agentsReducer(opened, nodeToggled({ key: "orch", open: false }));
    expect(closed.expanded).toEqual({ orch: false });
  });

  it("opens the ancestors of a selected entry, even one the user closed", () => {
    const state = agentsReducer({ ...withTree(), expanded: { orch: false } }, selected("t1r"));
    expect(state.expanded).toEqual({ t1: true, orch: true });
  });

  it("opens the ancestors of a focused entry", () => {
    const state = agentsReducer(withTree(), agentFocused("hidkid"));
    expect(state.expanded.hid).toBe(true);
  });

  it("leaves the expanded choices alone when the selection is cleared", () => {
    const state = agentsReducer(withExpanded({ orch: false }), selected(null));
    expect(state.expanded).toEqual({ orch: false });
  });

  it("leaves the expanded choices alone when a root is selected", () => {
    const state = agentsReducer(withExpanded({ orch: false }), selected("solo"));
    expect(state.expanded).toEqual({ orch: false });
  });

  it("sets the query and drops the nodes the user closed", () => {
    const state = agentsReducer(withExpanded({ a: true, b: false }), queryChanged("x"));
    expect(state).toMatchObject({ query: "x", expanded: { a: true } });
  });

  it("sets the status filter and drops the nodes the user closed", () => {
    const state = agentsReducer(withExpanded({ a: true, b: false }), statusFilterChanged("done"));
    expect(state).toMatchObject({ statusFilter: "done", expanded: { a: true } });
  });

  it("clears the query and status filter but keeps Show hidden", () => {
    const shown = agentsReducer(
      { ...withExpanded({ a: true, b: false }), query: "x", statusFilter: "done" },
      hiddenShown(true),
    );
    const state = agentsReducer(shown, filtersCleared());
    expect(state).toMatchObject({ query: "", statusFilter: "all", expanded: { a: true }, showHidden: true });
  });

  it("drops the expanded choice of an entry that is no longer listed", () => {
    const withT1 = agentsReducer(withTree(), nodeToggled({ key: "t1", open: true }));
    const without = agentsReducer(
      withT1,
      fetchEntries.fulfilled({ entries: treeEntries().filter((e) => e.key !== "t1"), warnings: [] }, "req"),
    );
    expect(without.expanded).toEqual({});
  });
});
