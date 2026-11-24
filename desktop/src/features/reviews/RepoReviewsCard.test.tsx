import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, reviewsView, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { RepoReviewsCard } from "./RepoReviewsCard";
import { fetchReviews } from "./reviewsSlice";

const renderCard = (repoId: string, api = fakeApi()): ReturnType<typeof testStore> => {
  const store = testStore({ api });
  store.dispatch(fetchReviews.fulfilled(reviewsView(), "req"));
  renderWithStore(<RepoReviewsCard repoId={repoId} />, store);
  return store;
};

describe("RepoReviewsCard", () => {
  it("shows the open count for a watched repo", () => {
    renderCard("github.com/bobcob7/buff-bear");
    expect(screen.getByText(/1 open/)).toBeInTheDocument();
  });

  it("turns on auto-review", async () => {
    const api = fakeApi();
    renderCard("github.com/bobcob7/buff-bear", api);
    await userEvent.click(screen.getByRole("switch", { name: /Review new pull requests automatically/ }));
    expect(api.reviewsSetWatch).toHaveBeenCalledWith("github.com/bobcob7/buff-bear", true, true, "local");
  });

  it("keeps the environment when either switch is toggled on a remote repo's card", async () => {
    const view = reviewsView();
    const [first, ...rest] = view.repos;
    const remote = { ...view, repos: [{ ...first!, environment: "linux-server" }, ...rest] };
    const api = fakeApi({ reviewsSetWatch: vi.fn(async () => remote) });
    const store = testStore({ api });
    store.dispatch(fetchReviews.fulfilled(remote, "req"));
    renderWithStore(<RepoReviewsCard repoId="github.com/bobcob7/buff-bear" />, store);
    await userEvent.click(screen.getByRole("switch", { name: /Watch pull requests/ }));
    expect(api.reviewsSetWatch).toHaveBeenLastCalledWith("github.com/bobcob7/buff-bear", false, false, "linux-server");
    await userEvent.click(screen.getByRole("switch", { name: /Review new pull requests automatically/ }));
    expect(api.reviewsSetWatch).toHaveBeenLastCalledWith("github.com/bobcob7/buff-bear", true, true, "linux-server");
  });

  it("shows the repo's pull requests in Reviews", async () => {
    const store = renderCard("github.com/bobcob7/buff-bear");
    await userEvent.click(screen.getByRole("button", { name: "Show in Reviews" }));
    expect(store.getState().ui.section).toBe("reviews");
    expect(store.getState().reviews.filter).toBe("buff-bear");
  });

  it("renders nothing for a repo that isn't on GitHub", () => {
    renderCard("gitlab.example.com/team/svc");
    expect(screen.queryByRole("region", { name: "Pull request reviews" })).not.toBeInTheDocument();
  });
});
