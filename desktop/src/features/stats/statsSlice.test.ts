import { describe, expect, it } from "vitest";
import { statsSnapshot } from "../../test/fixtures";
import { fetchStats, sinceFor, statsReducer, windowChosen, type StatsState, type StatsWindow } from "./statsSlice";

const initial = statsReducer(undefined, { type: "init" });
const requestId = "request-1";
const arg = (window: StatsWindow): { readonly window: StatsWindow; readonly sinceMs: number | null } => ({ window, sinceMs: null });

describe("stats slice", () => {
  it("starts on today with nothing loaded", () => {
    expect(initial.window).toBe("today");
    expect(initial.results.today).toEqual({ data: null, error: null, loading: false });
    expect(initial.results.all).toEqual({ data: null, error: null, loading: false });
  });

  it("marks only the requested window as loading while its fetch is pending", () => {
    const state = statsReducer(initial, fetchStats.pending(requestId, arg("7d")));
    expect(state.results["7d"].loading).toBe(true);
    expect(state.results.today.loading).toBe(false);
    expect(state.results["30d"].loading).toBe(false);
  });

  it("keeps the held data and error while a window is pending", () => {
    const data = statsSnapshot();
    const held: StatsState = {
      ...initial,
      results: { ...initial.results, "7d": { data, error: { code: "internal", message: "old" }, loading: false } },
    };
    const state = statsReducer(held, fetchStats.pending(requestId, arg("7d")));
    expect(state.results["7d"]).toEqual({ data, error: { code: "internal", message: "old" }, loading: true });
  });

  it("stores the stats for the requested window when the fetch succeeds", () => {
    const data = statsSnapshot();
    const state = statsReducer(initial, fetchStats.fulfilled(data, requestId, arg("30d")));
    expect(state.results["30d"]).toEqual({ data, error: null, loading: false });
  });

  it("leaves the other windows untouched when one window's fetch succeeds", () => {
    const pending = statsReducer(initial, fetchStats.pending(requestId, arg("today")));
    const state = statsReducer(pending, fetchStats.fulfilled(statsSnapshot(), requestId, arg("all")));
    expect(state.results.today.loading).toBe(true);
    expect(state.results.today.data).toBeNull();
  });

  it("records the error and keeps the last data when a fetch is rejected", () => {
    const data = statsSnapshot();
    const loaded = statsReducer(initial, fetchStats.fulfilled(data, requestId, arg("today")));
    const error = { code: "internal", message: "scan failed" };
    const state = statsReducer(loaded, fetchStats.rejected(null, requestId, arg("today"), error));
    expect(state.results.today).toEqual({ data, error, loading: false });
  });

  it("falls back to an internal error with the thrown message when a fetch is rejected without a payload", () => {
    const state = statsReducer(initial, fetchStats.rejected(new Error("boom"), requestId, arg("today")));
    expect(state.results.today.error).toEqual({ code: "internal", message: "boom" });
  });

  it("clears the error when a later fetch succeeds", () => {
    const failed = statsReducer(initial, fetchStats.rejected(null, requestId, arg("all"), { code: "internal", message: "x" }));
    const state = statsReducer(failed, fetchStats.fulfilled(statsSnapshot(), requestId, arg("all")));
    expect(state.results.all.error).toBeNull();
  });

  it("chooses the window the Stats section shows", () => {
    const state = statsReducer(initial, windowChosen("30d"));
    expect(state.window).toBe("30d");
  });
});

describe("sinceFor", () => {
  const now = new Date(2026, 10, 12, 15, 30);

  it("gives local midnight for today", () => {
    expect(sinceFor("today", now)).toBe(new Date(2026, 10, 12).getTime());
  });

  it("gives seven days before now for the last 7 days", () => {
    expect(sinceFor("7d", now)).toBe(now.getTime() - 7 * 86_400_000);
  });

  it("gives thirty days before now for the last 30 days", () => {
    expect(sinceFor("30d", now)).toBe(now.getTime() - 30 * 86_400_000);
  });

  it("gives null for all time", () => {
    expect(sinceFor("all", now)).toBeNull();
  });
});
