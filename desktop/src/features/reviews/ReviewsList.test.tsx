import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ReviewsView } from "../../api";
import { hostView, pullRequest, reviewsView, testStore } from "../../test/fixtures";
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

const choose = async (field: string, option: string): Promise<void> => {
  await userEvent.click(screen.getByRole("combobox", { name: new RegExp(field) }));
  await userEvent.click(screen.getByRole("option", { name: option }));
};

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

  it("searches pull requests by author", async () => {
    renderList(twoGroups());
    await userEvent.type(screen.getByLabelText("Filter pull requests"), "carol");
    expect(screen.queryByText("#42 Add rate limiting to login")).not.toBeInTheDocument();
    expect(screen.getByText("#17 Paginate the audit log")).toBeInTheDocument();
  });

  it("filters by author", async () => {
    renderList(twoGroups());
    await choose("Author", "carol");
    expect(screen.queryByText("#42 Add rate limiting to login")).not.toBeInTheDocument();
    expect(screen.getByText("#17 Paginate the audit log")).toBeInTheDocument();
  });

  it("filters to pull requests requested from me", async () => {
    renderList(twoGroups());
    await choose("Involves", "Review requested from me");
    expect(screen.getByText("#42 Add rate limiting to login")).toBeInTheDocument();
    expect(screen.queryByText("#17 Paginate the audit log")).not.toBeInTheDocument();
  });

  it("filters by merge status", async () => {
    const view = twoGroups();
    const failing = {
      ...view,
      prs: view.prs.map((pr) =>
        pr.key === "k17" ? { ...pr, merge: { status: "failing" as const, reasons: ["failing" as const], checks: { total: 2, passed: 1, failed: 1, pending: 0 } } } : pr,
      ),
    };
    renderList(failing);
    await choose("Merge status", "Checks failing");
    expect(screen.getByText("#17 Paginate the audit log")).toBeInTheDocument();
    expect(screen.queryByText("#42 Add rate limiting to login")).not.toBeInTheDocument();
  });

  it("Clear filters resets every filter", async () => {
    renderList(twoGroups());
    await choose("Author", "carol");
    await userEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(screen.getByText("#42 Add rate limiting to login")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Clear filters" })).not.toBeInTheDocument();
  });

  it("says which hosts have no login when filtering by me", async () => {
    renderList(reviewsView({ hosts: [hostView({ host: "github.com", login: null })] }));
    await choose("Involves", "Me: mine or requested");
    expect(screen.getByRole("status")).toHaveTextContent("Mine and requested don't include github.com: no login there.");
  });

  it("shows a merge badge on each row", () => {
    renderList(twoGroups());
    const row = screen.getByRole("button", { name: /#42 Add rate limiting/ });
    expect(within(row).getByText("Ready to merge")).toBeInTheDocument();
  });

  it("lists host errors above the groups", () => {
    const error = { code: "forge_unknown", message: "gitlab.example.com isn't a GitHub or Forgejo host. Choose its forge in Settings › Forges." };
    renderList(reviewsView({ hosts: [hostView({ host: "gitlab.example.com", error })] }));
    expect(screen.getByRole("status")).toHaveTextContent("gitlab.example.com: gitlab.example.com isn't a GitHub or Forgejo host.");
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
