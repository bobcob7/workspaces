import { describe, expect, it, vi } from "vitest";
import { fakeApi, loopHistoryRun, loopMemoryEntry, testStore } from "../../test/fixtures";
import { clearLoopHistory, fetchLoopHistory, fetchLoopMemory, removeLoopMemory, saveLoopMemory } from "./loopDataSlice";

const history = { loop: "deps", runs: [loopHistoryRun({ seq: 2 })], total: 2, warnings: [] };
const memory = { loop: "deps", entries: [loopMemoryEntry()], bytes: 12, warnings: [] };

describe("loop data", () => {
  it("fills the history when a fetch succeeds", async () => {
    const store = testStore({ api: fakeApi({ loopsHistory: vi.fn(async () => history) }) });
    await store.dispatch(fetchLoopHistory({ name: "deps", limit: 20 }));
    expect(store.getState().loopData.history).toEqual(history);
    expect(store.getState().loopData.historyLimit).toBe(20);
    expect(store.getState().loopData.historyError).toBeNull();
  });

  it("keeps the error when a history fetch is rejected", async () => {
    const error = { code: "loop_not_found", message: "no loop named deps" };
    const api = fakeApi({
      loopsHistory: vi.fn(async () => {
        throw error;
      }),
    });
    const store = testStore({ api });
    await store.dispatch(fetchLoopHistory({ name: "deps", limit: 20 }));
    expect(store.getState().loopData.historyError).toEqual(error);
    expect(store.getState().loopData.history).toBeNull();
  });

  it("drops the held data when another loop's history loads", async () => {
    const api = fakeApi({
      loopsMemory: vi.fn(async () => memory),
      loopsHistory: vi.fn(async (name: string) => ({ loop: name, runs: [], total: 0, warnings: [] })),
    });
    const store = testStore({ api });
    await store.dispatch(fetchLoopMemory("deps"));
    await store.dispatch(fetchLoopHistory({ name: "triage", limit: 20 }));
    expect(store.getState().loopData.loop).toBe("triage");
    expect(store.getState().loopData.memory).toBeNull();
  });

  it("sets saveError to the memory_conflict code when a save conflicts", async () => {
    const api = fakeApi({
      loopsMemorySet: vi.fn(async () => {
        throw { code: "memory_conflict", message: "seen changed (rev 3)" };
      }),
    });
    const store = testStore({ api });
    await store.dispatch(saveLoopMemory({ name: "deps", key: "seen", value: "#420", expectRev: 2 }));
    expect(store.getState().loopData.saveError?.code).toBe("memory_conflict");
    expect(store.getState().loopData.saving).toBe(false);
  });

  it("loads the memory again after a successful save", async () => {
    const loopsMemory = vi.fn(async () => memory);
    const api = fakeApi({ loopsMemory });
    const store = testStore({ api });
    await store.dispatch(saveLoopMemory({ name: "deps", key: "seen", value: "#420", expectRev: 2 }));
    expect(loopsMemory).toHaveBeenCalledWith("deps");
    expect(store.getState().loopData.memory).toEqual(memory);
    expect(store.getState().loopData.saveError).toBeNull();
  });

  it("shows an error notice when clearing the history fails", async () => {
    const api = fakeApi({
      loopsHistoryClear: vi.fn(async () => {
        throw { code: "loop_not_found", message: "no loop named deps" };
      }),
    });
    const store = testStore({ api });
    await store.dispatch(clearLoopHistory("deps"));
    expect(store.getState().ui.notice).toEqual({ severity: "error", message: "no loop named deps" });
  });

  it("shows an error notice when removing a memory value fails", async () => {
    const api = fakeApi({
      loopsMemoryRemove: vi.fn(async () => {
        throw { code: "memory_not_found", message: "no key seen" };
      }),
    });
    const store = testStore({ api });
    await store.dispatch(removeLoopMemory({ name: "deps", key: "seen" }));
    expect(store.getState().ui.notice).toEqual({ severity: "error", message: "no key seen" });
  });
});
