import { describe, expect, it, vi } from "vitest";
import { fakeApi, head, pullRequest, reviewPlan, reviewsView, testStore } from "../../test/fixtures";
import {
  dismissPr,
  dismissedToggled,
  fetchReviews,
  openStartDialog,
  reviewsReducer,
  startReview,
  type ReviewsState,
} from "./reviewsSlice";

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
