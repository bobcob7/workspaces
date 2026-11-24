import { describe, expect, it, vi } from "vitest";
import { fakeApi, loop, remoteEnvironment, testStore } from "../../test/fixtures";
import type { AppError } from "../../api";
import { offlineReason } from "../environments/environments";
import {
  addLoop,
  creatingStarted,
  editingStarted,
  editLoop,
  fetchLoops,
  loopSelected,
  removeLoop,
  runLoopNow,
  selectLoopBusy,
  selectLoopGroups,
  setLoopPaused,
} from "./loopsSlice";

const editRequest = { name: "deps", every: "2h", prompt: "Update.", specs: [], keep: 3 };

describe("loops", () => {
  it("fills the loops and warnings when a fetch succeeds", async () => {
    const api = fakeApi({ loopsList: vi.fn(async () => ({ loops: [loop()], warnings: ["loop skipped"], environmentStatus: null, loopsUnknown: false })) });
    const store = testStore({ api });
    await store.dispatch(fetchLoops());
    expect(store.getState().loops.byEnvironment["local"]?.items).toEqual([loop()]);
    expect(store.getState().loops.warnings).toEqual(["loop skipped"]);
  });

  it("unselects a selected loop that's gone", async () => {
    const store = testStore();
    store.dispatch(loopSelected("deps"));
    await store.dispatch(fetchLoops());
    expect(store.getState().loops.selected).toBeNull();
  });

  it("keeps the form open and sets formError when an add is rejected", async () => {
    const error = { code: "loop_invalid", message: 'period "2m" is under 5m' };
    const api = fakeApi({
      loopsAdd: vi.fn(async () => {
        throw error;
      }),
    });
    const store = testStore({ api });
    store.dispatch(creatingStarted());
    await store.dispatch(addLoop({ name: "deps", every: "2m", prompt: "Update.", specs: [] }));
    expect(store.getState().loops.formError).toEqual(error);
    expect(store.getState().loops.creating).toBe(true);
  });

  it("selects the new loop and closes the form when an add succeeds", async () => {
    const added = loop({ name: "deps" });
    const api = fakeApi({
      loopsAdd: vi.fn(async () => added),
      loopsList: vi.fn(async () => ({ loops: [added], warnings: [], environmentStatus: null, loopsUnknown: false })),
    });
    const store = testStore({ api });
    store.dispatch(creatingStarted());
    await store.dispatch(addLoop({ name: "deps", every: "1h", prompt: "Update.", specs: [] }));
    expect(store.getState().loops.creating).toBe(false);
    expect(store.getState().loops.selected).toBe("deps");
  });

  it("shows a success notice with the workspace to open when a run starts", async () => {
    const store = testStore();
    await store.dispatch(runLoopNow({ name: "deps" }));
    expect(store.getState().ui.notice).toEqual({
      severity: "success",
      message: "Started loop-deps-1",
      action: { label: "Open", agent: "loop-deps-1" },
    });
  });

  it("replaces the loop, closes the edit form, and clears formError when an edit succeeds", async () => {
    const edited = loop({ name: "deps", every: "2h", keep: 3 });
    const api = fakeApi({
      loopsEdit: vi.fn(async () => edited),
      loopsList: vi
        .fn()
        .mockResolvedValueOnce({ loops: [loop({ name: "deps" })], warnings: [], environmentStatus: null, loopsUnknown: false })
        .mockResolvedValue({ loops: [edited], warnings: [], environmentStatus: null, loopsUnknown: false }),
    });
    const store = testStore({ api });
    await store.dispatch(fetchLoops());
    store.dispatch(editingStarted());
    await store.dispatch(editLoop(editRequest));
    expect(store.getState().loops.byEnvironment["local"]?.items).toEqual([edited]);
    expect(store.getState().loops.editing).toBe(false);
    expect(store.getState().loops.formError).toBeNull();
  });

  it("keeps the edit form open and sets formError when an edit is rejected", async () => {
    const error = { code: "loop_invalid", message: 'period "2m" is under 5m' };
    const api = fakeApi({
      loopsEdit: vi.fn(async () => {
        throw error;
      }),
    });
    const store = testStore({ api });
    store.dispatch(editingStarted());
    await store.dispatch(editLoop(editRequest));
    expect(store.getState().loops.formError).toEqual(error);
    expect(store.getState().loops.editing).toBe(true);
    expect(store.getState().loops.submitting).toBe(false);
  });

  it("marks the form submitting while an edit is pending", () => {
    const store = testStore({ api: fakeApi({ loopsEdit: vi.fn(() => new Promise<never>(() => undefined)) }) });
    void store.dispatch(editLoop(editRequest));
    expect(store.getState().loops.submitting).toBe(true);
  });

  it("clears editing when a loop is selected after the edit form opened", () => {
    const store = testStore();
    store.dispatch(editingStarted());
    store.dispatch(loopSelected("deps"));
    expect(store.getState().loops.editing).toBe(false);
  });
});

const linux = remoteEnvironment({ name: "linux-server" });
const win = remoteEnvironment({ name: "win-rig", status: "offline", lastSeen: "2026-11-23T08:10:00Z" });
const withEnvironments = (...items: ReturnType<typeof remoteEnvironment>[]): { environments: { items: typeof items } } => ({
  environments: { items },
});

describe("loops by environment", () => {
  const stateWith = (...items: ReturnType<typeof remoteEnvironment>[]): Parameters<typeof testStore>[0] => ({
    state: withEnvironments(...items) as never,
  });
  const listing = (loops: ReturnType<typeof loop>[], extra: object = {}) => ({
    loops,
    warnings: [],
    environmentStatus: null,
    loopsUnknown: false,
    ...extra,
  });

  it("fetches local and each enrolled environment", async () => {
    const loopsList = vi.fn(async (_environment?: string) => listing([]));
    const store = testStore({ api: fakeApi({ loopsList }), ...stateWith(linux, win) });
    await store.dispatch(fetchLoops());
    expect(loopsList.mock.calls.map(([environment]) => environment).sort()).toEqual(["linux-server", "local", "win-rig"]);
  });

  it("does not fetch a pending environment", async () => {
    const loopsList = vi.fn(async (_environment?: string) => listing([]));
    const store = testStore({ api: fakeApi({ loopsList }), ...stateWith(remoteEnvironment({ name: "new-box", state: "pending", status: "pending" })) });
    await store.dispatch(fetchLoops());
    expect(loopsList).toHaveBeenCalledTimes(1);
    expect(loopsList).toHaveBeenCalledWith("local");
  });

  it("holds each environment's loops under its own name", async () => {
    const remote = loop({ name: "deps", environment: "linux-server" });
    const loopsList = vi.fn(async (environment?: string) => (environment === "linux-server" ? listing([remote]) : listing([loop()])));
    const store = testStore({ api: fakeApi({ loopsList }), ...stateWith(linux) });
    await store.dispatch(fetchLoops());
    const { byEnvironment } = store.getState().loops;
    expect(byEnvironment["local"]?.items).toEqual([loop()]);
    expect(byEnvironment["linux-server"]?.items).toEqual([remote]);
    expect(byEnvironment["linux-server"]?.error).toBeNull();
    expect(byEnvironment["linux-server"]?.loadedAt).not.toBeNull();
  });

  it("a failing environment keeps its last loops", async () => {
    const remote = loop({ name: "deps", environment: "linux-server" });
    const fail = { code: "environment_offline", message: "linux-server is offline" } satisfies AppError;
    const loopsList = vi
      .fn()
      .mockImplementationOnce(async () => listing([]))
      .mockImplementationOnce(async () => listing([remote]))
      .mockImplementationOnce(async () => listing([]))
      .mockImplementationOnce(async () => {
        throw fail;
      });
    const store = testStore({ api: fakeApi({ loopsList }), ...stateWith(linux) });
    await store.dispatch(fetchLoops());
    await store.dispatch(fetchLoops());
    const { byEnvironment } = store.getState().loops;
    expect(byEnvironment["linux-server"]?.items).toEqual([remote]);
    expect(byEnvironment["linux-server"]?.error).toBe("linux-server is offline");
    expect(byEnvironment["local"]?.error).toBeNull();
  });

  it("a failing environment does not stop the others", async () => {
    const loopsList = vi.fn(async (environment?: string) => {
      if (environment === "local") {
        throw { code: "internal", message: "boom" };
      }
      return listing([loop({ environment: "linux-server" })]);
    });
    const store = testStore({ api: fakeApi({ loopsList }), ...stateWith(linux) });
    await store.dispatch(fetchLoops());
    expect(store.getState().loops.byEnvironment["linux-server"]?.items).toHaveLength(1);
    expect(store.getState().loops.byEnvironment["local"]?.error).toBe("boom");
  });

  it("keeps the local warnings", async () => {
    const loopsList = vi.fn(async (environment?: string) => ({ ...listing([]), warnings: [`from ${environment ?? "?"}`] }));
    const store = testStore({ api: fakeApi({ loopsList }), ...stateWith(linux) });
    await store.dispatch(fetchLoops());
    expect(store.getState().loops.warnings).toEqual(["from local"]);
  });

  it("records that an environment has not reported its loops", async () => {
    const loopsList = vi.fn(async (environment?: string) => listing([], environment === "linux-server" ? { loopsUnknown: true } : {}));
    const store = testStore({ api: fakeApi({ loopsList }), ...stateWith(linux) });
    await store.dispatch(fetchLoops());
    expect(store.getState().loops.byEnvironment["linux-server"]?.loopsUnknown).toBe(true);
    expect(store.getState().loops.byEnvironment["local"]?.loopsUnknown).toBe(false);
  });

  it("unselects a selected loop only when its own environment lacks it", async () => {
    const loopsList = vi.fn(async (environment?: string) => listing(environment === "local" ? [loop({ name: "deps" })] : []));
    const store = testStore({ api: fakeApi({ loopsList }), ...stateWith(linux) });
    store.dispatch(loopSelected("deps", "linux-server"));
    await store.dispatch(fetchLoops());
    expect(store.getState().loops.selected).toBeNull();
  });

  it("keeps a selected remote loop that its environment still lists", async () => {
    const loopsList = vi.fn(async (environment?: string) => listing(environment === "linux-server" ? [loop({ name: "deps" })] : []));
    const store = testStore({ api: fakeApi({ loopsList }), ...stateWith(linux) });
    store.dispatch(loopSelected("deps", "linux-server"));
    await store.dispatch(fetchLoops());
    expect(store.getState().loops.selected).toBe("deps");
    expect(store.getState().loops.selectedEnvironment).toBe("linux-server");
  });

  it("keeps a selected loop when its environment's fetch failed", async () => {
    const loopsList = vi.fn(async (environment?: string) => {
      if (environment === "linux-server") {
        throw { code: "environment_offline", message: "offline" };
      }
      return listing([]);
    });
    const store = testStore({ api: fakeApi({ loopsList }), ...stateWith(linux) });
    store.dispatch(loopSelected("deps", "linux-server"));
    await store.dispatch(fetchLoops());
    expect(store.getState().loops.selected).toBe("deps");
  });

  it("selects a loop on local by default", () => {
    const store = testStore();
    store.dispatch(loopSelected("deps"));
    expect(store.getState().loops.selectedEnvironment).toBe("local");
  });

  it("adds on the loop's environment and selects it there", async () => {
    const added = loop({ name: "deps", environment: "linux-server" });
    const api = fakeApi({ loopsAdd: vi.fn(async () => added) });
    const store = testStore({ api, ...stateWith(linux) });
    await store.dispatch(addLoop({ name: "deps", every: "1h", prompt: "Update.", specs: [], environment: "linux-server", confirmed: true }));
    expect(api.loopsAdd).toHaveBeenCalledWith(expect.objectContaining({ environment: "linux-server", confirmed: true }));
    expect(store.getState().loops.selectedEnvironment).toBe("linux-server");
  });

  it("edits a remote loop in place in its own environment's list", async () => {
    const original = loop({ name: "deps", environment: "linux-server" });
    const local = loop({ name: "deps" });
    const edited = loop({ name: "deps", every: "2h", environment: "linux-server" });
    const loopsList = vi.fn(async (environment?: string) => listing(environment === "local" ? [local] : [original]));
    const api = fakeApi({ loopsList, loopsEdit: vi.fn(async () => edited) });
    const store = testStore({ api, ...stateWith(linux) });
    await store.dispatch(fetchLoops());
    loopsList.mockClear();
    loopsList.mockImplementation(async (environment?: string) => listing(environment === "local" ? [local] : [edited]));
    await store.dispatch(editLoop({ ...editRequest, environment: "linux-server" }));
    expect(store.getState().loops.byEnvironment["linux-server"]?.items).toEqual([edited]);
    expect(store.getState().loops.byEnvironment["local"]?.items).toEqual([local]);
  });

  it("pauses a loop on its environment", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(setLoopPaused({ name: "deps", paused: true, environment: "linux-server" }));
    expect(api.loopsSetPaused).toHaveBeenCalledWith("deps", true, "linux-server");
  });

  it("writes a paused remote loop into its own environment's list only", async () => {
    const local = loop({ name: "deps" });
    const remote = loop({ name: "deps", environment: "alpha" });
    const paused = loop({ name: "deps", paused: true, environment: "alpha" });
    const api = fakeApi({ loopsSetPaused: vi.fn(async () => paused) });
    const byEnvironment = {
      local: { items: [local], loadedAt: 1, error: null, loopsUnknown: false },
      alpha: { items: [remote], loadedAt: 1, error: null, loopsUnknown: false },
    };
    const store = testStore({ api, state: { loops: { ...testStore().getState().loops, byEnvironment } } });
    await store.dispatch(setLoopPaused({ name: "deps", paused: true, environment: "alpha" }));
    expect(api.loopsSetPaused).toHaveBeenCalledWith("deps", true, "alpha");
    expect(store.getState().loops.byEnvironment["alpha"]?.items).toEqual([paused]);
    expect(store.getState().loops.byEnvironment["local"]?.items).toEqual([local]);
  });

  it("marks only the loop of the environment an action runs on as busy", async () => {
    let finish: (value: ReturnType<typeof loop>) => void = () => undefined;
    const api = fakeApi({ loopsSetPaused: vi.fn(() => new Promise<ReturnType<typeof loop>>((resolve) => (finish = resolve))) });
    const store = testStore({ api });
    const pending = store.dispatch(setLoopPaused({ name: "deps", paused: true, environment: "alpha" }));
    const state = store.getState();
    expect(selectLoopBusy(state, "deps", "alpha")).toBe(true);
    expect(selectLoopBusy(state, "deps")).toBe(false);
    expect(selectLoopBusy(state, "deps", "beta")).toBe(false);
    finish(loop({ name: "deps", paused: true }));
    await pending;
    expect(selectLoopBusy(store.getState(), "deps", "alpha")).toBe(false);
  });

  it("runs a loop on its environment", async () => {
    const api = fakeApi();
    await testStore({ api }).dispatch(runLoopNow({ name: "deps", environment: "linux-server" }));
    expect(api.loopsRunNow).toHaveBeenCalledWith("deps", "linux-server");
  });

  it("removes a remote loop only with the confirmation it was given", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(removeLoop({ name: "deps", environment: "linux-server", confirmed: true }));
    expect(api.loopsRemove).toHaveBeenCalledWith("deps", true, "linux-server");
    await store.dispatch(removeLoop({ name: "deps", environment: "linux-server" }));
    expect(api.loopsRemove).toHaveBeenLastCalledWith("deps", false, "linux-server");
  });
});

describe("selectLoopGroups", () => {
  const fetched = async (api = fakeApi({ loopsList: vi.fn(async (_environment?: string) => ({ loops: [], warnings: [], environmentStatus: null, loopsUnknown: false })) })) => {
    const store = testStore({ api, state: withEnvironments(linux, win, remoteEnvironment({ name: "alpha" })) as never });
    await store.dispatch(fetchLoops());
    return store;
  };
  const now = Date.parse("2026-11-23T09:00:00Z");

  it("groups put local first", async () => {
    const store = await fetched();
    expect(selectLoopGroups(store.getState(), now).map((g) => g.environment)).toEqual(["local", "alpha", "linux-server", "win-rig"]);
  });

  it("an offline group carries its reason", async () => {
    const groups = selectLoopGroups((await fetched()).getState(), now);
    const offline = groups.find((g) => g.environment === "win-rig");
    expect(offline?.online).toBe(false);
    expect(offline?.reason).toBe(offlineReason(win, now));
    expect(offline?.reason).toContain("win-rig is offline");
    expect(groups.find((g) => g.environment === "linux-server")).toMatchObject({ online: true, reason: "" });
    expect(groups[0]).toMatchObject({ environment: "local", online: true, reason: "", stale: false, loopsUnknown: false });
  });

  it("a group carries its loops, and whether they are stale or unknown", async () => {
    const stale = loop({ name: "deps", environment: "win-rig", stale: true, summary: true });
    const api = fakeApi({
      loopsList: vi.fn(async (environment?: string) => ({
        loops: environment === "win-rig" ? [stale] : [],
        warnings: [],
        environmentStatus: null,
        loopsUnknown: environment === "alpha",
      })),
    });
    const groups = selectLoopGroups((await fetched(api)).getState(), now);
    expect(groups.find((g) => g.environment === "win-rig")).toMatchObject({ items: [stale], stale: true, loopsUnknown: false });
    expect(groups.find((g) => g.environment === "alpha")).toMatchObject({ items: [], stale: false, loopsUnknown: true });
  });

  it("lists an enrolled environment before its first fetch with no loops", () => {
    const store = testStore({ state: withEnvironments(linux) as never });
    expect(selectLoopGroups(store.getState(), now).map((g) => [g.environment, g.items])).toEqual([
      ["local", []],
      ["linux-server", []],
    ]);
  });

  it("leaves out a pending environment", () => {
    const store = testStore({ state: withEnvironments(remoteEnvironment({ name: "new-box", state: "pending", status: "pending" })) as never });
    expect(selectLoopGroups(store.getState(), now).map((g) => g.environment)).toEqual(["local"]);
  });
});
