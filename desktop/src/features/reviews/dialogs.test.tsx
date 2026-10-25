import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, pullRequest, reviewPlan, reviewsView, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchReviews, openStartDialog, watchedOpened } from "./reviewsSlice";
import { StartReviewDialog } from "./StartReviewDialog";
import { WatchedReposDialog } from "./WatchedReposDialog";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(async () => undefined),
  Channel: class {},
}));

describe("WatchedReposDialog", () => {
  const openDialog = (api = fakeApi()): ReturnType<typeof testStore> => {
    const store = testStore({ api });
    store.dispatch(fetchReviews.fulfilled(reviewsView(), "req"));
    store.dispatch(watchedOpened());
    renderWithStore(<WatchedReposDialog />, store);
    return store;
  };

  it("re-reads the cached repos when opened", () => {
    const api = fakeApi();
    openDialog(api);
    expect(api.reviewsLoadRepos).toHaveBeenCalled();
  });

  it("turns on auto-review for a watched repo", async () => {
    const api = fakeApi();
    openDialog(api);
    await userEvent.click(screen.getByLabelText("Auto-review github.com/bobcob7/buff-bear"));
    expect(api.reviewsSetWatch).toHaveBeenCalledWith("github.com/bobcob7/buff-bear", true, true);
  });

  it("stops watching a repo", async () => {
    const api = fakeApi();
    openDialog(api);
    await userEvent.click(screen.getByLabelText("Watch github.com/bobcob7/buff-bear"));
    expect(api.reviewsSetWatch).toHaveBeenCalledWith("github.com/bobcob7/buff-bear", false, false);
  });

  it("lists other hosts as GitHub repos only", () => {
    openDialog();
    expect(screen.getByText("GitHub repos only")).toBeInTheDocument();
  });

  it("goes to the review rules in Settings", async () => {
    const store = openDialog();
    await userEvent.click(screen.getByRole("button", { name: "Review rules…" }));
    expect(store.getState().ui.section).toBe("settings");
  });
});

describe("StartReviewDialog", () => {
  const openDialog = async (api = fakeApi()): Promise<ReturnType<typeof testStore>> => {
    const store = testStore({ api });
    store.dispatch(fetchReviews.fulfilled(reviewsView(), "req"));
    renderWithStore(<StartReviewDialog />, store);
    await store.dispatch(openStartDialog(pullRequest()));
    return store;
  };

  it("starts the review with the edited task", async () => {
    const api = fakeApi();
    await openDialog(api);
    const task = await screen.findByLabelText("Task");
    await userEvent.clear(task);
    await userEvent.type(task, "Only check the SQL");
    await userEvent.click(screen.getByRole("button", { name: "Start review" }));
    expect(api.reviewsStart).toHaveBeenCalledWith({ ...reviewPlan(), prompt: "Only check the SQL" });
  });

  it("warns that a different name won't be connected to the pull request", async () => {
    await openDialog();
    await userEvent.type(await screen.findByLabelText("Workspace name"), "-x");
    expect(screen.getByText(/won't connect the agent to this pull request/)).toBeInTheDocument();
  });

  it("shows the repo at the head commit, read-only", async () => {
    await openDialog();
    expect(await screen.findByText("github.com/bobcob7/buff-bear@3f2a9c1")).toBeInTheDocument();
  });
});
