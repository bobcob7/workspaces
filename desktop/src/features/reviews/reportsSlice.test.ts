import { describe, expect, it, vi } from "vitest";
import { fakeApi, testStore } from "../../test/fixtures";
import { realReport } from "../../test/reportFixture";
import { decideFindings, fetchReport, filterChanged, openFullReport, reportKey, reportReplaced, reportsReducer, tabChosen } from "./reportsSlice";

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
    expect(api.reviewDecide).toHaveBeenNthCalledWith(1, repo, ref, { findings: ["a:1"], decision: "comment", suggestion: null, note: "x" }, "local", undefined);
    expect(api.reviewDecide).toHaveBeenNthCalledWith(2, repo, ref, { findings: ["b:1"], decision: "comment", suggestion: null, note: "" }, "local", undefined);
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

describe("reportKey", () => {
  it("keeps a local key as repo@ref", () => {
    expect(reportKey(repo, ref, "local")).toBe(`${repo}@${ref}`);
    expect(reportKey(repo, ref)).toBe(`${repo}@${ref}`);
  });

  it("prefixes a remote key with its environment", () => {
    expect(reportKey(repo, ref, "linux-server")).toBe(`env:linux-server:${repo}@${ref}`);
  });
});

describe("remote reports", () => {
  it("a remote report has its own key", async () => {
    const api = fakeApi({ reviewReport: vi.fn(async () => realReport) });
    const store = testStore({ api });
    await store.dispatch(fetchReport({ repo, ref, refresh: false, environment: "linux-server", reviewId: "review-1" }));
    const { byKey } = store.getState().reports;
    expect(byKey[reportKey(repo, ref, "linux-server")]?.report).toEqual(realReport);
    expect(byKey[reportKey(repo, ref)]).toBeUndefined();
  });

  it("reads a remote report on its environment and review", async () => {
    const api = fakeApi({ reviewReport: vi.fn(async () => realReport) });
    await testStore({ api }).dispatch(fetchReport({ repo, ref, refresh: true, environment: "linux-server", reviewId: "review-1" }));
    expect(api.reviewReport).toHaveBeenCalledWith(repo, ref, true, "linux-server", "review-1");
  });

  it("reads a local report on local", async () => {
    const api = fakeApi({ reviewReport: vi.fn(async () => realReport) });
    await testStore({ api }).dispatch(fetchReport({ repo, ref, refresh: false }));
    expect(api.reviewReport).toHaveBeenCalledWith(repo, ref, false, "local", undefined);
  });

  it("does not let a local fetch block a remote one for the same commit", async () => {
    const api = fakeApi({ reviewReport: vi.fn(async () => realReport) });
    const store = testStore({ api });
    store.dispatch(fetchReport.pending("req", { repo, ref, refresh: false }));
    await store.dispatch(fetchReport({ repo, ref, refresh: false, environment: "linux-server" }));
    expect(api.reviewReport).toHaveBeenCalledTimes(1);
  });

  it("decides on the review's environment and keeps the report under its key", async () => {
    const api = fakeApi({ reviewDecide: vi.fn(async () => realReport) });
    const store = testStore({ api });
    await store.dispatch(
      decideFindings({ repo, ref, findings: [{ id: "a:1", note: "" }], decision: "skip", environment: "linux-server", reviewId: "review-1" }),
    );
    expect(api.reviewDecide).toHaveBeenCalledWith(
      repo,
      ref,
      { findings: ["a:1"], decision: "skip", suggestion: null, note: "" },
      "linux-server",
      "review-1",
    );
    expect(store.getState().reports.byKey[reportKey(repo, ref, "linux-server")]?.report).toEqual(realReport);
    expect(store.getState().reports.byKey[reportKey(repo, ref)]).toBeUndefined();
  });

  it("opens the full report of a remote review", async () => {
    const api = fakeApi();
    await testStore({ api }).dispatch(openFullReport({ repo, ref, environment: "linux-server", reviewId: "review-1" }));
    expect(api.reviewOpenReport).toHaveBeenCalledWith(repo, ref, "linux-server", "review-1");
  });

  it("replaces a remote report under its own key", () => {
    const state = reportsReducer(undefined, reportReplaced({ repo, ref, report: realReport, environment: "linux-server" }));
    expect(state.byKey[reportKey(repo, ref, "linux-server")]?.report).toEqual(realReport);
    expect(state.byKey[reportKey(repo, ref)]).toBeUndefined();
  });

  it("keeps when a report was read after a later read fails", () => {
    const arg = { repo: "r", ref: "x", refresh: true, environment: "linux-server" };
    const read = reportsReducer(undefined, fetchReport.fulfilled(realReport, "a", arg));
    const failed = reportsReducer(read, fetchReport.rejected(new Error("offline"), "b", arg));
    const key = reportKey("r", "x", "linux-server");
    expect(read.byKey[key]?.loadedAt).toBeTypeOf("number");
    expect(failed.byKey[key]?.loadedAt).toBe(read.byKey[key]?.loadedAt);
    expect(failed.byKey[key]?.report).toBe(realReport);
  });
});
