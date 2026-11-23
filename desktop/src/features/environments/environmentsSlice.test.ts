import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fakeApi, remoteEnvironment, testStore } from "../../test/fixtures";
import {
  environmentsReducer,
  fetchEnvironments,
  lastChoiceKey,
  lastChoiceSet,
  removeEnvironment,
  rotateCa,
} from "./environmentsSlice";

const initial = environmentsReducer(undefined, { type: "@@init" });

const listing = (...environments: ReturnType<typeof remoteEnvironment>[]) => ({ environments, warnings: [] });

/** Starts a fetch with the given request id, as the thunk does. */
const started = (state: typeof initial, id: string) => environmentsReducer(state, fetchEnvironments.pending(id, undefined));

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("environmentsReducer", () => {
  it("starts empty with local chosen", () => {
    expect(initial).toEqual({ items: [], loadedAt: null, error: null, lastChoice: "local", latestRequestId: null });
  });

  it("reads the last choice from localStorage", () => {
    window.localStorage.setItem(lastChoiceKey, "win-rig");
    expect(environmentsReducer(undefined, { type: "@@init" }).lastChoice).toBe("win-rig");
  });

  it("falls back to local when storage throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(environmentsReducer(undefined, { type: "@@init" }).lastChoice).toBe("local");
  });

  it("stores a listing and when it loaded", () => {
    const env = remoteEnvironment();
    const next = environmentsReducer(
      started(initial, "r"),
      fetchEnvironments.fulfilled({ listing: { environments: [env], warnings: [] }, at: 1234 }, "r", undefined),
    );
    expect(next).toMatchObject({ items: [env], loadedAt: 1234, error: null });
  });

  it("keeps the last listing and records the error when a fetch fails", () => {
    const env = remoteEnvironment();
    const loaded = environmentsReducer(
      started(initial, "r"),
      fetchEnvironments.fulfilled({ listing: { environments: [env], warnings: [] }, at: 1 }, "r", undefined),
    );
    const next = environmentsReducer(
      started(loaded, "r2"),
      fetchEnvironments.rejected(null, "r2", undefined, { code: "ws_failed", message: "ws env list failed" }),
    );
    expect(next.items).toEqual([env]);
    expect(next.loadedAt).toBe(1);
    expect(next.error).toBe("ws env list failed");
  });

  it("clears the error on the next good fetch", () => {
    const failed = environmentsReducer(
      started(initial, "r"),
      fetchEnvironments.rejected(null, "r", undefined, { code: "ws_failed", message: "nope" }),
    );
    const next = environmentsReducer(
      started(failed, "r2"),
      fetchEnvironments.fulfilled({ listing: { environments: [], warnings: [] }, at: 2 }, "r2", undefined),
    );
    expect(next.error).toBeNull();
  });

  it("ignores a slower, earlier fetch that settles after a newer one", () => {
    const fresh = remoteEnvironment({ name: "fresh" });
    const stale = remoteEnvironment({ name: "stale" });
    let state = started(started(initial, "old"), "new");
    state = environmentsReducer(state, fetchEnvironments.fulfilled({ listing: listing(fresh), at: 2 }, "new", undefined));
    state = environmentsReducer(state, fetchEnvironments.fulfilled({ listing: listing(stale), at: 1 }, "old", undefined));
    expect(state.items.map((e) => e.name)).toEqual(["fresh"]);
    expect(state.loadedAt).toBe(2);
  });

  it("ignores an error from a slower, earlier fetch after a newer one succeeded", () => {
    let state = started(started(initial, "old"), "new");
    state = environmentsReducer(state, fetchEnvironments.fulfilled({ listing: listing(), at: 2 }, "new", undefined));
    state = environmentsReducer(state, fetchEnvironments.rejected(null, "old", undefined, { code: "x", message: "late" }));
    expect(state.error).toBeNull();
  });

  it("ignores a stale response end to end through the store", async () => {
    const resolvers: ((l: ReturnType<typeof listing>) => void)[] = [];
    const api = fakeApi({
      environmentsList: vi.fn(() => new Promise<ReturnType<typeof listing>>((resolve) => resolvers.push(resolve))),
    });
    const store = testStore({ api });
    const first = store.dispatch(fetchEnvironments());
    const second = store.dispatch(fetchEnvironments());
    resolvers[1]?.(listing(remoteEnvironment({ name: "fresh" })));
    await second;
    resolvers[0]?.(listing(remoteEnvironment({ name: "stale" })));
    await first;
    expect(store.getState().environments.items.map((e) => e.name)).toEqual(["fresh"]);
  });

  it("remembers the last choice and writes it to localStorage", () => {
    const next = environmentsReducer(initial, lastChoiceSet("linux-server"));
    expect(next.lastChoice).toBe("linux-server");
    expect(window.localStorage.getItem(lastChoiceKey)).toBe("linux-server");
  });

  it("keeps the choice when storage throws on write", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("full");
    });
    expect(environmentsReducer(initial, lastChoiceSet("win-rig")).lastChoice).toBe("win-rig");
  });
});

describe("environment thunks", () => {
  it("fetchEnvironments loads the listing into the store", async () => {
    const env = remoteEnvironment();
    const api = fakeApi({ environmentsList: vi.fn(async () => ({ environments: [env], warnings: [] })) });
    const store = testStore({ api });
    await store.dispatch(fetchEnvironments());
    expect(store.getState().environments.items).toEqual([env]);
    expect(store.getState().environments.loadedAt).not.toBeNull();
  });

  it("fetchEnvironments records an api failure", async () => {
    const api = fakeApi({
      environmentsList: vi.fn(async () => {
        throw { code: "ws_failed", message: "boom" };
      }),
    });
    const store = testStore({ api });
    await store.dispatch(fetchEnvironments());
    expect(store.getState().environments.error).toBe("boom");
  });

  it("removeEnvironment removes by name and refreshes the listing", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(removeEnvironment("win-rig"));
    expect(api.environmentRemove).toHaveBeenCalledWith("win-rig");
    expect(api.environmentsList).toHaveBeenCalled();
  });

  it("removeEnvironment rejects with the app error", async () => {
    const api = fakeApi({
      environmentRemove: vi.fn(async () => {
        throw { code: "not_found", message: "no such environment" };
      }),
    });
    const result = await testStore({ api }).dispatch(removeEnvironment("gone"));
    expect(result.type).toBe("environments/remove/rejected");
  });

  it("rotateCa passes finish and force, then refreshes", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(rotateCa({ finish: true, force: false }));
    expect(api.environmentsCaRotate).toHaveBeenCalledWith(true, false);
    expect(api.environmentsList).toHaveBeenCalled();
  });
});

describe("the enrollment code (D5)", () => {
  const secret = "ws-enroll-SECRET-never-in-the-store";

  it("keeps only declared fields, so a code in a listing never reaches the store", async () => {
    const leaky = { ...remoteEnvironment(), code: secret, hello: { ...remoteEnvironment().hello, code: secret } };
    const api = fakeApi({ environmentsList: vi.fn(async () => listing(leaky as ReturnType<typeof remoteEnvironment>)) });
    const store = testStore({ api });
    await store.dispatch(fetchEnvironments());
    expect(store.getState().environments.items).toHaveLength(1);
    expect(JSON.stringify(store.getState())).not.toContain(secret);
  });

  it("has no state field for a code", () => {
    expect(Object.keys(initial)).not.toContain("code");
  });

  it("does not put what the add call returns into localStorage", async () => {
    const api = fakeApi({
      environmentAdd: vi.fn(async (name: string) => ({
        id: "env_1",
        name,
        state: "pending" as const,
        code: secret,
        caCreated: true,
        revokedSerials: [],
        warnings: [],
      })),
    });
    const store = testStore({ api });
    await api.environmentAdd("new-box");
    store.dispatch(lastChoiceSet("new-box"));
    expect(JSON.stringify({ ...window.localStorage })).not.toContain(secret);
  });
});
