import { describe, expect, it, vi } from "vitest";
import { fakeApi, head, pullRequest, reviewPlan, reviewsView, testStore } from "../../test/fixtures";
import {
  askAboutPr,
  authorFiltered,
  dismissPr,
  dismissedToggled,
  fetchReviews,
  filtersCleared,
  involvesFiltered,
  mergeFiltered,
  openStartDialog,
  reviewsReducer,
  setHosts,
  startReview,
  type ReviewsState,
} from "./reviewsSlice";
import { noFilters } from "./groups";

const initial = (): ReviewsState => reviewsReducer(undefined, { type: "init" });

describe("reviewsReducer", () => {
  it("stores the view when a fetch succeeds", () => {
    const view = reviewsView();
    expect(reviewsReducer(initial(), fetchReviews.fulfilled(view, "req")).view).toEqual(view);
  });

  it("keeps the last view when a fetch fails", () => {
    const loaded = reviewsReducer(initial(), fetchReviews.fulfilled(reviewsView(), "r1"));
    const state = reviewsReducer(loaded, fetchReviews.rejected(null, "r2", undefined, { code: "internal", message: "x" }));
    expect(state.view).not.toBeNull();
    expect(state.error?.message).toBe("x");
  });

  it("starts with Dismissed collapsed", () => {
    expect(initial().dismissedOpen).toBe(false);
    expect(reviewsReducer(initial(), dismissedToggled()).dismissedOpen).toBe(true);
  });
});

describe("thunks", () => {
  it("dismisses at the pull request's head commit", async () => {
    const api = fakeApi();
    await testStore({ api }).dispatch(dismissPr(pullRequest()));
    expect(api.reviewsDismiss).toHaveBeenCalledWith("github.com/bobcob7/buff-bear", 42, head);
  });

  it("starts a review with the default plan and selects the pull request", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(startReview({ pr: pullRequest() }));
    expect(api.reviewsStart).toHaveBeenCalledWith(reviewPlan());
    expect(store.getState().reviews.selectedKey).toBe("github.com/bobcob7/buff-bear#42");
  });

  it("refreshes agents after starting so the review shows", async () => {
    const api = fakeApi();
    await testStore({ api }).dispatch(startReview({ pr: pullRequest() }));
    expect(api.listEntries).toHaveBeenCalled();
  });

  it("treats an existing review as already started, not an error", async () => {
    const api = fakeApi({ reviewsStart: vi.fn(async () => ({ id: "review-buff-bear-42-3f2a9c1", already: true })) });
    const store = testStore({ api });
    await store.dispatch(startReview({ pr: pullRequest() }));
    expect(store.getState().ui.notice).toEqual({ severity: "info", message: "review-buff-bear-42-3f2a9c1 is already reviewing it" });
  });

  it("shows a failed one-click review as a notice", async () => {
    const api = fakeApi({ reviewsStart: vi.fn(async () => Promise.reject({ code: "claude_untrusted", message: "not trusted" })) });
    const store = testStore({ api });
    await store.dispatch(startReview({ pr: pullRequest() }));
    expect(store.getState().ui.notice?.message).toBe("not trusted");
  });

  it("keeps the edit dialog open with the error when starting fails", async () => {
    const api = fakeApi({ reviewsStart: vi.fn(async () => Promise.reject({ code: "claude_untrusted", message: "not trusted" })) });
    const store = testStore({ api });
    await store.dispatch(openStartDialog(pullRequest()));
    await store.dispatch(startReview({ pr: pullRequest(), plan: reviewPlan() }));
    expect(store.getState().reviews.startDialog?.error?.message).toBe("not trusted");
  });

  it("closes the edit dialog once the review starts", async () => {
    const store = testStore();
    await store.dispatch(openStartDialog(pullRequest()));
    await store.dispatch(startReview({ pr: pullRequest(), plan: reviewPlan() }));
    expect(store.getState().reviews.startDialog).toBeNull();
  });
});

describe("askAboutPr", () => {
  it("selects the new agent in Agents", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    await store.dispatch(askAboutPr(pullRequest()));
    expect(api.reviewsAsk).toHaveBeenCalledWith("github.com/bobcob7/buff-bear", 42);
    expect(api.listEntries).toHaveBeenCalled();
    expect(store.getState().ui.section).toBe("agents");
    expect(store.getState().ui.selectedKey).toBe("ask-buff-bear-42-3f2a9c1-0a1b2c3d");
    expect(store.getState().ui.notice).toEqual({
      severity: "success",
      message: "Started ask-buff-bear-42-3f2a9c1-0a1b2c3d to answer questions about bobcob7/buff-bear#42",
    });
  });

  it("says when the agent already exists", async () => {
    const api = fakeApi({
      reviewsAsk: vi.fn(async () => ({ id: "ask-buff-bear-42-3f2a9c1-0a1b2c3d", session: null, path: "/w/a", already: true })),
    });
    const store = testStore({ api });
    await store.dispatch(askAboutPr(pullRequest()));
    expect(store.getState().ui.notice).toEqual({
      severity: "info",
      message: "ask-buff-bear-42-3f2a9c1-0a1b2c3d already answers questions about bobcob7/buff-bear#42",
    });
  });

  it("shows a failure and stops holding the pull request", async () => {
    const api = fakeApi({ reviewsAsk: vi.fn(async () => Promise.reject({ code: "pr_not_found", message: "gone" })) });
    const store = testStore({ api });
    await store.dispatch(askAboutPr(pullRequest()));
    expect(store.getState().ui.notice).toEqual({ severity: "error", message: "gone" });
    expect(store.getState().reviews.asking).toEqual([]);
  });

  it("holds the pull request while it starts", () => {
    const state = reviewsReducer(initial(), askAboutPr.pending("req", pullRequest()));
    expect(state.asking).toContain("github.com/bobcob7/buff-bear#42");
  });
});

describe("filter reducers", () => {
  it("starts with no filters", () => {
    expect(initial().filters).toEqual(noFilters);
  });

  it("sets the author, involves, and merge filters", () => {
    let state = reviewsReducer(initial(), authorFiltered("alice"));
    state = reviewsReducer(state, involvesFiltered("requested"));
    state = reviewsReducer(state, mergeFiltered("failing"));
    expect(state.filters).toEqual({ author: "alice", involves: "requested", merge: "failing" });
  });

  it("clears the author and merge filters with null", () => {
    const set = reviewsReducer(reviewsReducer(initial(), authorFiltered("alice")), mergeFiltered("failing"));
    const state = reviewsReducer(reviewsReducer(set, authorFiltered(null)), mergeFiltered(null));
    expect(state.filters).toEqual(noFilters);
  });

  it("clears every filter", () => {
    const set = reviewsReducer(reviewsReducer(initial(), authorFiltered("alice")), involvesFiltered("mine"));
    expect(reviewsReducer(set, filtersCleared()).filters).toEqual(noFilters);
  });
});

describe("setHosts", () => {
  it("stores the returned view", async () => {
    const view = reviewsView({ checkedAtMs: 42 });
    const api = fakeApi({ reviewsSetHosts: vi.fn(async () => view) });
    const store = testStore({ api });
    const hosts = [{ name: "git.example.com", kind: "forgejo" as const }];
    await store.dispatch(setHosts(hosts));
    expect(api.reviewsSetHosts).toHaveBeenCalledWith(hosts);
    expect(store.getState().reviews.view).toEqual(view);
  });
});
