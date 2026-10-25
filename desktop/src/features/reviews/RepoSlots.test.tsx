import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, reviewsView, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { addRepo, removeRepo } from "../repos/reposSlice";
import { reviewsSlot } from "../repos/reviewsSlot";
import { AddRepoWatchSwitch, isGitHubRepo, RemoveRepoReviewsNote, RepoReviewsIcons } from "./RepoSlots";
import { fetchReviews } from "./reviewsSlice";

const loaded = (api = fakeApi()): ReturnType<typeof testStore> => {
  const store = testStore({ api });
  store.dispatch(fetchReviews.fulfilled(reviewsView(), "req"));
  return store;
};

const added = { id: "github.com/bobcob7/new-repo", path: "/c/new-repo.git", upstream: "u", added: true };

describe("Repos slots", () => {
  it("are all found by Repos", () => {
    expect(Object.keys(reviewsSlot).toSorted()).toEqual(
      ["AddRepoWatchSwitch", "RemoveRepoReviewsNote", "RepoReviewsCard", "RepoReviewsIcons"].toSorted(),
    );
    expect(Object.values(reviewsSlot).every((c) => c !== undefined)).toBe(true);
  });

  it("marks a watched repo with an eye", () => {
    renderWithStore(<RepoReviewsIcons repoId="github.com/bobcob7/buff-bear" />, loaded());
    expect(screen.getByRole("img", { name: "Watched in Reviews" })).toBeInTheDocument();
    expect(screen.queryByRole("img", { name: "Auto-review on" })).not.toBeInTheDocument();
  });

  it("offers Watch in Add repo for a GitHub repo", async () => {
    const onChange = vi.fn();
    renderWithStore(<AddRepoWatchSwitch repoId="github.com/o/new" checked onChange={onChange} />, loaded());
    await userEvent.click(screen.getByRole("switch", { name: "Watch pull requests in Reviews" }));
    expect(onChange).toHaveBeenCalledWith(false);
  });

  it("hides Watch in Add repo for another host", () => {
    renderWithStore(<AddRepoWatchSwitch repoId="gitlab.example.com/team/x" checked onChange={vi.fn()} />, loaded());
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  it("says in the Remove dialog that a watched repo leaves Reviews", () => {
    renderWithStore(<RemoveRepoReviewsNote repoId="github.com/bobcob7/buff-bear" />, loaded());
    expect(screen.getByText(/stop being checked/)).toBeInTheDocument();
  });
});

describe("isGitHubRepo", () => {
  it("trusts the last check for a cached repo", () => {
    expect(isGitHubRepo("gitlab.example.com/team/svc", reviewsView())).toBe(false);
  });

  it("accepts an uncached github.com repo", () => {
    expect(isGitHubRepo("github.com/o/r", null)).toBe(true);
  });
});

describe("following Repos", () => {
  it("watches a repo added with Watch on", async () => {
    const api = fakeApi();
    const store = loaded(api);
    store.dispatch(addRepo.fulfilled(added, "req", { spec: "o/new-repo", watch: true }));
    await vi.waitFor(() => expect(api.reviewsSetWatch).toHaveBeenCalledWith(added.id, true, false));
  });

  it("leaves a repo added with Watch off unwatched", async () => {
    const api = fakeApi();
    const store = loaded(api);
    store.dispatch(addRepo.fulfilled(added, "req", { spec: "o/new-repo", watch: false }));
    await Promise.resolve();
    expect(api.reviewsSetWatch).not.toHaveBeenCalled();
  });

  it("re-reads the repos after one is removed", async () => {
    const api = fakeApi();
    const store = loaded(api);
    store.dispatch(removeRepo.fulfilled(undefined, "req", "github.com/bobcob7/buff-bear"));
    await vi.waitFor(() => expect(api.reviewsLoadRepos).toHaveBeenCalled());
  });
});
