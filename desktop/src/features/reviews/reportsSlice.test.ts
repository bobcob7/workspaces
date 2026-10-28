import { describe, expect, it, vi } from "vitest";
import { fakeApi, testStore } from "../../test/fixtures";
import { realReport } from "../../test/reportFixture";
import { decideFindings, filterChanged, reportKey, tabChosen } from "./reportsSlice";

const repo = realReport.repo;
const ref = realReport.ref;

describe("decideFindings", () => {
  it("decides findings with the same note in one call", async () => {
    const api = fakeApi({ reviewDecide: vi.fn(async () => realReport) });
    const store = testStore({ api });
    await store.dispatch(
      decideFindings({ repo, ref, findings: [{ id: "a:1", note: "" }, { id: "b:1", note: "" }], decision: "skip" }),
    );
    expect(api.reviewDecide).toHaveBeenCalledTimes(1);
    expect(store.getState().reports.byKey[reportKey(repo, ref)]?.report).toEqual(realReport);
    expect(store.getState().reports.deciding).toEqual([]);
  });

  it("makes one call per note, since a call carries one", async () => {
    const api = fakeApi({ reviewDecide: vi.fn(async () => realReport) });
    await testStore({ api }).dispatch(
      decideFindings({ repo, ref, findings: [{ id: "a:1", note: "x" }, { id: "b:1", note: "" }], decision: "comment" }),
    );
    expect(api.reviewDecide).toHaveBeenNthCalledWith(1, repo, ref, { findings: ["a:1"], decision: "comment", suggestion: null, note: "x" });
    expect(api.reviewDecide).toHaveBeenNthCalledWith(2, repo, ref, { findings: ["b:1"], decision: "comment", suggestion: null, note: "" });
  });

  it("drops notes when clearing", async () => {
    const api = fakeApi({ reviewDecide: vi.fn(async () => realReport) });
    await testStore({ api }).dispatch(
      decideFindings({ repo, ref, findings: [{ id: "a:1", note: "x" }, { id: "b:1", note: "" }], decision: "clear" }),
    );
    expect(api.reviewDecide).toHaveBeenCalledTimes(1);
  });

  it("shows ws's error and keeps the report", async () => {
    const api = fakeApi({ reviewDecide: vi.fn(async () => Promise.reject({ code: "finding_not_found", message: "no a:1" })) });
    const store = testStore({ api });
    await store.dispatch(decideFindings({ repo, ref, findings: [{ id: "a:1", note: "" }], decision: "skip" }));
    expect(store.getState().ui.notice).toEqual({ severity: "error", message: "no a:1" });
    expect(store.getState().reports.deciding).toEqual([]);
  });
});

describe("triage", () => {
  it("keeps filters and the tab per pull request", () => {
    const store = testStore();
    store.dispatch(filterChanged({ pr: "a#1", value: { profile: "security" } }));
    store.dispatch(tabChosen({ pr: "a#1", value: "description" }));
    expect(store.getState().reports.triage["a#1"]?.filter.profile).toBe("security");
    expect(store.getState().reports.triage["a#1"]?.tab).toBe("description");
    expect(store.getState().reports.triage["b#2"]).toBeUndefined();
  });
});
