import { describe, expect, it, vi } from "vitest";
import { fakeApi, loop, testStore } from "../../test/fixtures";
import { addLoop, creatingStarted, fetchLoops, loopSelected, runLoopNow } from "./loopsSlice";

describe("loops", () => {
  it("fills the loops and warnings when a fetch succeeds", async () => {
    const api = fakeApi({ loopsList: vi.fn(async () => ({ loops: [loop()], warnings: ["loop skipped"] })) });
    const store = testStore({ api });
    await store.dispatch(fetchLoops());
    expect(store.getState().loops.loops).toEqual([loop()]);
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
      loopsList: vi.fn(async () => ({ loops: [added], warnings: [] })),
    });
    const store = testStore({ api });
    store.dispatch(creatingStarted());
    await store.dispatch(addLoop({ name: "deps", every: "1h", prompt: "Update.", specs: [] }));
    expect(store.getState().loops.creating).toBe(false);
    expect(store.getState().loops.selected).toBe("deps");
  });

  it("shows a success notice with the workspace to open when a run starts", async () => {
    const store = testStore();
    await store.dispatch(runLoopNow("deps"));
    expect(store.getState().ui.notice).toEqual({
      severity: "success",
      message: "Started loop-deps-1",
      action: { label: "Open", agent: "loop-deps-1" },
    });
  });
});
