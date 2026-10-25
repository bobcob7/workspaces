import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api } from "../../api";
import { cachedRepo, entry, fakeApi, repoRef, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchEntries } from "../agents/agentsSlice";
import { ReposPane } from "./ReposPane";
import { fetchRepos, selectRepo } from "./reposSlice";

const id = "github.com/bobcob7/buff-bear";

const refs = [
  repoRef({ name: "main", subject: "Merge pull request #39" }),
  repoRef({ name: "feat/rate-limit", subject: "Add a token bucket per client IP", author: "alice" }),
  repoRef({ name: "v1.0.0", kind: "tag", subject: "Release 1.0" }),
];

const fixLogin = entry({
  key: "fix-login",
  name: "fix-login",
  repos: [{ id, path: "/w/fix-login/buff-bear", ref: "feat/rate-limit", refKind: "branch" }],
});

const renderPane = async (overrides: Partial<Api> = {}) => {
  const api = fakeApi({
    cacheList: vi.fn(async () => ({ repos: [cachedRepo({ id })], warnings: [] })),
    cacheRefs: vi.fn(async () => ({ id, defaultBranch: "main", refs })),
    ...overrides,
  });
  const store = testStore({ api });
  store.dispatch(fetchRepos.fulfilled({ repos: [cachedRepo({ id })], warnings: [] }, "r"));
  store.dispatch(fetchEntries.fulfilled({ entries: [fixLogin], warnings: [] }, "r"));
  await store.dispatch(selectRepo(id));
  renderWithStore(<ReposPane />, store);
  return { api, store };
};

describe("ReposPane", () => {
  it("titles the repo with its owner muted", async () => {
    await renderPane();
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("github.com/bobcob7/buff-bear");
  });

  it("shows the upstream, default branch, and size", async () => {
    await renderPane();
    expect(screen.getByText("https://github.com/bobcob7/buff-bear.git")).toBeInTheDocument();
    expect(screen.getByText("18.4 MB")).toBeInTheDocument();
  });

  it("lists branches with the default first and marked", async () => {
    await renderPane();
    const rows = within(screen.getByRole("table", { name: "Branches" })).getAllByRole("row");
    expect(rows[1]).toHaveTextContent("maindefault");
  });

  it("shows the workspaces on a branch", async () => {
    await renderPane();
    const row = screen.getByRole("row", { name: /feat\/rate-limit/ });
    expect(within(row).getByText("fix-login")).toBeInTheDocument();
  });

  it("opens a workspace chip in Agents", async () => {
    const { store } = await renderPane();
    const row = screen.getByRole("row", { name: /feat\/rate-limit/ });
    await userEvent.click(within(row).getByText("fix-login"));
    expect(store.getState().ui).toMatchObject({ section: "agents", selectedKey: "fix-login" });
  });

  it("starts an agent on a branch", async () => {
    const { store } = await renderPane();
    await userEvent.click(screen.getByRole("button", { name: "New agent on feat/rate-limit" }));
    expect(store.getState().ui).toMatchObject({ dialog: "agent", dialogSpecs: [`${id}@feat/rate-limit`] });
  });

  it("starts a shell on the repo from the header", async () => {
    const { store } = await renderPane();
    await userEvent.click(screen.getByRole("button", { name: "New shell" }));
    expect(store.getState().ui).toMatchObject({ dialog: "shell", dialogSpecs: [id] });
  });

  it("lists tags on the Tags tab", async () => {
    await renderPane();
    await userEvent.click(screen.getByRole("tab", { name: /Tags/ }));
    expect(within(screen.getByRole("table", { name: "Tags" })).getByText("v1.0.0")).toBeInTheDocument();
  });

  it("filters branches by name", async () => {
    await renderPane();
    await userEvent.type(screen.getByRole("searchbox", { name: "Filter branches" }), "rate");
    expect(screen.queryByText("Merge pull request #39")).not.toBeInTheDocument();
  });

  it("lists the repo's workspaces on the Workspaces tab", async () => {
    await renderPane();
    await userEvent.click(screen.getByRole("tab", { name: /Workspaces/ }));
    expect(within(screen.getByRole("list", { name: "Workspaces" })).getByText("fix-login")).toBeInTheDocument();
  });

  it("fetches the repo", async () => {
    const { api } = await renderPane();
    await userEvent.click(screen.getByRole("button", { name: "Fetch" }));
    expect(api.cacheFetch).toHaveBeenCalledWith([id], false);
  });

  it("shows a failed fetch above the tabs", async () => {
    await renderPane({
      cacheFetch: vi.fn(async () => [{ id, error: { code: "fetch_failed", message: "could not resolve host" } }]),
    });
    await userEvent.click(screen.getByRole("button", { name: "Fetch" }));
    expect(await screen.findByText(/could not resolve host/)).toBeInTheDocument();
  });

  it("says which workspaces keep working before removing", async () => {
    await renderPane();
    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(screen.getByRole("dialog")).toHaveTextContent("Its 1 workspace, fix-login, keeps working");
  });

  it("removes the repo once confirmed", async () => {
    const { api } = await renderPane();
    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Remove" }));
    expect(api.cacheRemove).toHaveBeenCalledWith(id);
  });
});

describe("ReposPane empty states", () => {
  it("teaches what the section is for when the cache is empty", async () => {
    const store = testStore();
    store.dispatch(fetchRepos.fulfilled({ repos: [], warnings: [] }, "r"));
    renderWithStore(<ReposPane />, store);
    await userEvent.click(screen.getByRole("button", { name: "Add repo" }));
    expect(store.getState().repos.addOpen).toBe(true);
  });

  it("shows ws failing as an alert", () => {
    const store = testStore();
    store.dispatch(fetchRepos.rejected(null, "r", undefined, { code: "ws_missing", message: "`ws` isn't on PATH" }));
    renderWithStore(<ReposPane />, store);
    expect(screen.getByRole("alert")).toHaveTextContent("Install ws");
  });
});
