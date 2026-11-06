import { describe, expect, it, vi } from "vitest";
import { fakeApi, loop, testStore } from "../../test/fixtures";
import { addLoop, creatingStarted, editingStarted, editLoop, fetchLoops, loopSelected, runLoopNow } from "./loopsSlice";

const editRequest = { name: "deps", every: "2h", prompt: "Update.", specs: [], keep: 3 };

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

  it("replaces the loop, closes the edit form, and clears formError when an edit succeeds", async () => {
    const edited = loop({ name: "deps", every: "2h", keep: 3 });
    const api = fakeApi({
      loopsEdit: vi.fn(async () => edited),
      loopsList: vi
        .fn()
        .mockResolvedValueOnce({ loops: [loop({ name: "deps" })], warnings: [] })
        .mockResolvedValue({ loops: [edited], warnings: [] }),
    });
    const store = testStore({ api });
    await store.dispatch(fetchLoops());
    store.dispatch(editingStarted());
    await store.dispatch(editLoop(editRequest));
    expect(store.getState().loops.loops).toEqual([edited]);
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
