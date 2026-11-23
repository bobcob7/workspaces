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

beforeEach(() => {
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("environmentsReducer", () => {
  it("starts empty with local chosen", () => {
    expect(initial).toEqual({ items: [], loadedAt: null, error: null, lastChoice: "local" });
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
      initial,
      fetchEnvironments.fulfilled({ listing: { environments: [env], warnings: [] }, at: 1234 }, "r", undefined),
    );
    expect(next).toMatchObject({ items: [env], loadedAt: 1234, error: null });
  });

  it("keeps the last listing and records the error when a fetch fails", () => {
    const env = remoteEnvironment();
    const loaded = environmentsReducer(
      initial,
      fetchEnvironments.fulfilled({ listing: { environments: [env], warnings: [] }, at: 1 }, "r", undefined),
    );
    const next = environmentsReducer(
      loaded,
      fetchEnvironments.rejected(null, "r", undefined, { code: "ws_failed", message: "ws env list failed" }),
    );
    expect(next.items).toEqual([env]);
    expect(next.loadedAt).toBe(1);
    expect(next.error).toBe("ws env list failed");
  });

  it("clears the error on the next good fetch", () => {
    const failed = environmentsReducer(
      initial,
      fetchEnvironments.rejected(null, "r", undefined, { code: "ws_failed", message: "nope" }),
    );
    const next = environmentsReducer(
      failed,
      fetchEnvironments.fulfilled({ listing: { environments: [], warnings: [] }, at: 2 }, "r", undefined),
    );
    expect(next.error).toBeNull();
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

  it("never reaches the store or localStorage", async () => {
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
    const code = await api.environmentAdd("new-box");
    expect(code.code).toBe(secret);
    await store.dispatch(fetchEnvironments());
    await store.dispatch(removeEnvironment("other"));
    await store.dispatch(rotateCa({ finish: false, force: false }));
    store.dispatch(lastChoiceSet("new-box"));
    expect(JSON.stringify(store.getState())).not.toContain(secret);
    expect(JSON.stringify({ ...window.localStorage })).not.toContain(secret);
  });

  it("has no environment-add thunk or state field for a code", () => {
    expect(Object.keys(initial)).not.toContain("code");
  });
});
