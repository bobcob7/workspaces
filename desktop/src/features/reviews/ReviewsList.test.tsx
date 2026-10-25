import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ReviewsView } from "../../api";
import { pullRequest, reviewsView, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchReviews } from "./reviewsSlice";
import { ReviewsList } from "./ReviewsList";

const renderList = (view: ReviewsView): ReturnType<typeof testStore> => {
  const store = testStore();
  store.dispatch(fetchReviews.fulfilled(view, "req"));
  renderWithStore(<ReviewsList />, store);
  return store;
};

const twoGroups = (): ReviewsView =>
  reviewsView({
    prs: [
      pullRequest(),
      pullRequest({ key: "k17", number: 17, title: "Paginate the audit log", author: { login: "carol" }, requested: false }),
      pullRequest({ key: "k40", number: 40, title: "WIP: try sqlc", group: "dismissed", isDraft: true }),
    ],
  });

describe("ReviewsList", () => {
  it("lists pull requests under To review with their summary", () => {
    renderList(twoGroups());
    const group = screen.getByRole("region", { name: "To review" });
    expect(within(group).getByText("#42 Add rate limiting to login")).toBeInTheDocument();
    expect(within(group).getByText("buff-bear · alice · review requested")).toBeInTheDocument();
  });

  it("collapses Dismissed until it's opened", async () => {
    renderList(twoGroups());
    expect(screen.queryByText("#40 WIP: try sqlc")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Dismissed/ }));
    expect(screen.getByText("#40 WIP: try sqlc")).toBeInTheDocument();
  });

  it("filters by author", async () => {
    renderList(twoGroups());
    await userEvent.type(screen.getByLabelText("Filter pull requests"), "carol");
    expect(screen.queryByText("#42 Add rate limiting to login")).not.toBeInTheDocument();
    expect(screen.getByText("#17 Paginate the audit log")).toBeInTheDocument();
  });

  it("selects a pull request when clicked", async () => {
    const store = renderList(twoGroups());
    await userEvent.click(screen.getByText("#17 Paginate the audit log"));
    expect(store.getState().reviews.selectedKey).toBe("k17");
  });

  it("moves the selection with the arrow keys", async () => {
    const store = renderList(twoGroups());
    await userEvent.click(screen.getByText("#42 Add rate limiting to login"));
    await userEvent.keyboard("{ArrowDown}");
    expect(store.getState().reviews.selectedKey).toBe("k17");
  });

  it("shows a warning row for a repo whose check failed", () => {
    const view = reviewsView();
    const failing = { ...view, repos: view.repos.map((r, i) => (i === 0 ? { ...r, error: "gh: HTTP 404" } : r)) };
    renderList(failing);
    expect(screen.getByRole("status")).toHaveTextContent("buff-bear: gh: HTTP 404");
  });

  it("says so when there are no pull requests", () => {
    renderList(reviewsView({ prs: [] }));
    expect(screen.getByText("No pull requests to show.")).toBeInTheDocument();
  });
});
