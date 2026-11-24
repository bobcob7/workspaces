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
  fetchPlan,
  selectReviewEnvironment,
  setHosts,
  setWatch,
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

describe("review environments", () => {
  const repoId = "github.com/bobcob7/buff-bear";
  const remoteView = reviewsView({
    repos: [{ id: repoId, github: true, forge: "github", watch: true, autoReview: false, environment: "linux-server", open: 1, checkedAtMs: 1, error: null }],
  });
  const remoteStore = (api = fakeApi()): ReturnType<typeof testStore> =>
    testStore({ api, state: { reviews: { ...initial(), view: remoteView } } });

  it("selectReviewEnvironment names the environment a repo is watched on", () => {
    const store = remoteStore();
    expect(selectReviewEnvironment(store.getState(), repoId)).toBe("linux-server");
  });

  it("selectReviewEnvironment is local for a repo with no environment", () => {
    const store = testStore({ state: { reviews: { ...initial(), view: reviewsView() } } });
    expect(selectReviewEnvironment(store.getState(), repoId)).toBe("local");
  });

  it("selectReviewEnvironment is local for an unknown repo and before the view loads", () => {
    expect(selectReviewEnvironment(testStore().getState(), repoId)).toBe("local");
    expect(selectReviewEnvironment(remoteStore().getState(), "other/repo")).toBe("local");
  });

  it("sets a watch on an environment", async () => {
    const api = fakeApi();
    await testStore({ api }).dispatch(setWatch({ repo: repoId, watch: true, autoReview: false, environment: "linux-server" }));
    expect(api.reviewsSetWatch).toHaveBeenCalledWith(repoId, true, false, "linux-server");
  });

  it("sets a watch on local by default", async () => {
    const api = fakeApi();
    await testStore({ api }).dispatch(setWatch({ repo: repoId, watch: true, autoReview: false }));
    expect(api.reviewsSetWatch).toHaveBeenCalledWith(repoId, true, false, "local");
  });

  it("plans a review on the environment its repo is watched on", async () => {
    const api = fakeApi();
    await remoteStore(api).dispatch(fetchPlan(pullRequest()));
    expect(api.reviewsPlan).toHaveBeenCalledWith(repoId, 42, "linux-server");
  });

  it("opens the start dialog with a plan from the repo's environment", async () => {
    const api = fakeApi();
    await remoteStore(api).dispatch(openStartDialog(pullRequest()));
    expect(api.reviewsPlan).toHaveBeenCalledWith(repoId, 42, "linux-server");
  });

  it("starts a remote review from a plan made on its environment", async () => {
    const api = fakeApi();
    await remoteStore(api).dispatch(startReview({ pr: pullRequest() }));
    expect(api.reviewsPlan).toHaveBeenCalledWith(repoId, 42, "linux-server");
  });

  it("quick-starts a remote review without bypassPermissions", async () => {
    const api = fakeApi({ reviewsPlan: vi.fn(async () => reviewPlan({ permissionMode: "bypassPermissions", environment: "linux-server" })) });
    await remoteStore(api).dispatch(startReview({ pr: pullRequest() }));
    expect(api.reviewsStart).toHaveBeenCalledWith(reviewPlan({ permissionMode: "", environment: "linux-server" }));
  });

  it("keeps the planned permission mode of a remote quick start", async () => {
    const api = fakeApi({ reviewsPlan: vi.fn(async () => reviewPlan({ permissionMode: "plan", environment: "linux-server" })) });
    await remoteStore(api).dispatch(startReview({ pr: pullRequest() }));
    expect(api.reviewsStart).toHaveBeenCalledWith(reviewPlan({ permissionMode: "plan", environment: "linux-server" }));
  });

  it("keeps bypassPermissions on a local quick start", async () => {
    const api = fakeApi({ reviewsPlan: vi.fn(async () => reviewPlan({ permissionMode: "bypassPermissions" })) });
    await testStore({ api, state: { reviews: { ...initial(), view: reviewsView() } } }).dispatch(startReview({ pr: pullRequest() }));
    expect(api.reviewsStart).toHaveBeenCalledWith(reviewPlan({ permissionMode: "bypassPermissions" }));
  });
});
