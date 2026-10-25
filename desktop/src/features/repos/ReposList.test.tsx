import { fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { CachedRepo } from "../../api";
import { cachedRepo, fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { ReposList } from "./ReposList";

const repos = [
  cachedRepo({ id: "github.com/bobcob7/buff-bear", branches: 14 }),
  cachedRepo({ id: "github.com/bobcob7/api" }),
  cachedRepo({ id: "github.com/rust-lang/cargo", sizeBytes: 412_000_000 }),
];

const renderList = (list: readonly CachedRepo[] = repos) => {
  const api = fakeApi({ cacheList: vi.fn(async () => ({ repos: list, warnings: [] })) });
  const store = testStore({ api });
  renderWithStore(<ReposList />, store);
  return { api, store };
};

describe("ReposList", () => {
  it("reads the cache when shown", async () => {
    const { api } = renderList();
    await screen.findByText("buff-bear");
    expect(api.cacheList).toHaveBeenCalledTimes(1);
  });

  it("groups repos under their host/owner", async () => {
    renderList();
    const group = await screen.findByRole("region", { name: "github.com/rust-lang" });
    expect(within(group).getByText("cargo")).toBeInTheDocument();
  });

  it("summarizes each repo", async () => {
    renderList();
    expect(await screen.findByText("main · 14 branches")).toBeInTheDocument();
  });

  it("filters by id", async () => {
    renderList();
    await screen.findByText("buff-bear");
    await userEvent.type(screen.getByRole("searchbox", { name: "Filter repos" }), "rust");
    expect(screen.queryByText("buff-bear")).not.toBeInTheDocument();
  });

  it("selects a repo and reads its refs", async () => {
    const { api, store } = renderList();
    await userEvent.click(await screen.findByRole("button", { name: /buff-bear/ }));
    expect(store.getState().repos.selectedId).toBe("github.com/bobcob7/buff-bear");
    expect(api.cacheRefs).toHaveBeenCalledWith("github.com/bobcob7/buff-bear");
  });

  it("moves down the list with the arrow key", async () => {
    const { store } = renderList();
    const api = await screen.findByRole("button", { name: /api/ });
    await userEvent.click(api);
    fireEvent.keyDown(api, { key: "ArrowDown" });
    expect(store.getState().repos.selectedId).toBe("github.com/bobcob7/buff-bear");
  });

  it("says so when the cache is empty", async () => {
    renderList([]);
    expect(await screen.findByText("No cached repos yet.")).toBeInTheDocument();
  });
});
