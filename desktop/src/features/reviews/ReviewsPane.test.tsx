import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api, ReviewsView } from "../../api";
import { entry, fakeApi, pullRequest, reviewRun, reviewsView, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchEntries } from "../agents/agentsSlice";
import { fetchReviews, reviewSelected } from "./reviewsSlice";
import { ReviewsPane } from "./ReviewsPane";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(async () => undefined),
  Channel: class {},
}));

const renderPane = (view: ReviewsView, api: Api = fakeApi()): ReturnType<typeof testStore> => {
  const store = testStore({ api });
  store.dispatch(fetchReviews.fulfilled(view, "req"));
  const first = view.prs[0];
  if (first !== undefined) {
    store.dispatch(reviewSelected(first.key));
  }
  renderWithStore(<ReviewsPane />, store);
  return store;
};

const unwatched = (view: ReviewsView): ReviewsView => ({
  ...view,
  repos: view.repos.map((r) => ({ ...r, watch: false })),
  prs: [],
});

describe("ReviewsPane", () => {
  it("says a removed review's agent is gone, with no agent actions", () => {
    renderPane(reviewsView({ prs: [pullRequest({ group: "reviewed", reviews: [reviewRun({ removed: true, sessionId: null })] })] }));
    expect(screen.getByText("agent removed after finishing")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Open agent" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
  });

  it("offers to choose repos when none are watched", async () => {
    const store = renderPane(unwatched(reviewsView()));
    await userEvent.click(screen.getByRole("button", { name: "Choose repos to watch" }));
    expect(store.getState().reviews.watchedOpen).toBe(true);
  });

  it("says what to run when gh isn't signed in", () => {
    const error = { code: "gh_auth", message: "gh isn't signed in. Run gh auth login in a terminal, then Check now." };
    renderPane({ ...unwatched(reviewsView()), error });
    expect(screen.getByText("gh auth login").tagName).toBe("CODE");
  });

  it("says when watched repos have no open pull requests", () => {
    renderPane(reviewsView({ prs: [] }));
    expect(screen.getByText(/No open pull requests in 1 watched repo\./)).toBeInTheDocument();
  });

  it("shows a pull request's description as markdown", () => {
    renderPane(reviewsView());
    expect(screen.getByText("token bucket").tagName).toBe("STRONG");
  });

  it("shows the command a review will run", async () => {
    renderPane(reviewsView());
    expect(await screen.findByText("ws review start --pr 42 github.com/bobcob7/buff-bear")).toBeInTheDocument();
  });

  it("starts a review with Review", async () => {
    const api = fakeApi();
    renderPane(reviewsView(), api);
    await userEvent.click(screen.getByRole("button", { name: "Review" }));
    expect(api.reviewsStart).toHaveBeenCalled();
  });

  it("opens the edit dialog with Edit before starting", async () => {
    const store = renderPane(reviewsView());
    await userEvent.click(screen.getByRole("button", { name: "Edit before starting" }));
    expect(store.getState().reviews.startDialog?.key).toBe("github.com/bobcob7/buff-bear#42");
  });

  it("restores a dismissed pull request", async () => {
    const api = fakeApi();
    renderPane(reviewsView({ prs: [pullRequest({ group: "dismissed" })] }), api);
    await userEvent.click(screen.getByRole("button", { name: "Restore" }));
    expect(api.reviewsRestore).toHaveBeenCalledWith("github.com/bobcob7/buff-bear", 42);
  });

  it("names the commits pushed since an out-of-date review and offers Review again", () => {
    const pr = pullRequest({
      group: "reviewed",
      outOfDate: true,
      reviews: [reviewRun({ sha: "a41c9f2d0b6d4a2f9e8c7b6a5d4c3b2a1f0e9d8c" })],
      newCommits: { count: 2, subjects: ["Evict on logout", "Cap the cache"] },
    });
    renderPane(reviewsView({ prs: [pr] }));
    expect(screen.getByRole("status")).toHaveTextContent(
      'alice pushed 2 commits after this review: "Evict on logout" and "Cap the cache". These decisions stay with a41c9f2; Review again reviews 3f2a9c1 from scratch.',
    );
    expect(screen.getByRole("button", { name: "Review again" })).toBeInTheDocument();
  });

  it("shows a finished review's result", () => {
    renderPane(reviewsView({ prs: [pullRequest({ group: "reviewed", reviews: [reviewRun()] })] }));
    expect(screen.getByText("Two problems worth fixing before merge, one nit.")).toBeInTheDocument();
  });

  it("labels an auto-started review", () => {
    renderPane(reviewsView({ prs: [pullRequest({ group: "reviewed", reviews: [reviewRun({ auto: true })] })] }));
    expect(screen.getByText("auto")).toBeInTheDocument();
  });

  it("opens the review agent in Agents", async () => {
    const store = renderPane(reviewsView({ prs: [pullRequest({ group: "reviewed", reviews: [reviewRun()] })] }));
    await userEvent.click(await screen.findByRole("tab", { name: "Review agent" }));
    await userEvent.click(screen.getByRole("button", { name: "Open agent" }));
    expect(store.getState().ui.section).toBe("agents");
    expect(store.getState().ui.selectedKey).toBe("review-buff-bear-42-3f2a9c1");
  });

  it("removes a review after confirming", async () => {
    const api = fakeApi();
    const store = renderPane(reviewsView({ prs: [pullRequest({ group: "reviewed", reviews: [reviewRun()] })] }), api);
    const workspace = entry({ key: "review-buff-bear-42-3f2a9c1", workspaceId: "review-buff-bear-42-3f2a9c1" });
    store.dispatch(fetchEntries.fulfilled({ entries: [workspace], warnings: [] }, "req"));
    await userEvent.click(await screen.findByRole("tab", { name: "Review agent" }));
    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    await userEvent.click(screen.getAllByRole("button", { name: "Remove" }).at(-1) as HTMLElement);
    expect(api.removeEntry).toHaveBeenCalledWith("review-buff-bear-42-3f2a9c1", "e8cdf5d8", false);
  });
});
