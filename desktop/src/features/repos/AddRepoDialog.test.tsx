import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api } from "../../api";
import { cachedRepo, fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { AddRepoDialog } from "./AddRepoDialog";
import { addOpened } from "./reposSlice";

const openDialog = (overrides: Partial<Api> = {}) => {
  const api = fakeApi(overrides);
  const store = testStore({ api });
  store.dispatch(addOpened());
  renderWithStore(<AddRepoDialog />, store);
  return { api, store };
};

const field = (): HTMLElement => screen.getByLabelText("Repo spec or URL");

describe("AddRepoDialog", () => {
  it("previews what the spec resolves to", async () => {
    openDialog();
    await userEvent.type(field(), "payments");
    expect(await screen.findByTestId("resolve-preview")).toHaveTextContent(
      "github.com/bobcob7/payments · found on github.com via defaults.owners · not cached yet",
    );
  });

  it("resolves once typing pauses", async () => {
    const { api } = openDialog();
    await userEvent.type(field(), "payments");
    await screen.findByTestId("resolve-preview");
    expect(api.cacheResolve).toHaveBeenCalledTimes(1);
  });

  it("offers the candidates of an ambiguous name", async () => {
    openDialog({
      cacheResolve: vi.fn(async () =>
        Promise.reject({ code: "repo_ambiguous", message: "ambiguous", details: { candidates: ["github.com/a/api", "github.com/b/api"] } }),
      ),
    });
    await userEvent.type(field(), "api");
    await userEvent.click(await screen.findByText("github.com/b/api"));
    expect(field()).toHaveValue("github.com/b/api");
  });

  it("disables Add repo for a repo that isn't found", async () => {
    openDialog({ cacheResolve: vi.fn(async () => Promise.reject({ code: "repo_not_found", message: "no repo nope" })) });
    await userEvent.type(field(), "nope");
    await screen.findByText("no repo nope");
    expect(screen.getByRole("button", { name: "Add repo" })).toBeDisabled();
  });

  it("adds the repo and selects it", async () => {
    const payments = cachedRepo({ id: "github.com/bobcob7/payments" });
    const { api, store } = openDialog({ cacheList: vi.fn(async () => ({ repos: [payments], warnings: [] })) });
    await userEvent.type(field(), "payments");
    await screen.findByTestId("resolve-preview");
    await userEvent.click(screen.getByRole("button", { name: "Add repo" }));
    expect(api.cacheAdd).toHaveBeenCalledWith("payments");
    expect(store.getState().repos).toMatchObject({ addOpen: false, selectedId: "github.com/bobcob7/payments" });
  });

  it("shows a failed clone and stays open", async () => {
    const { store } = openDialog({ cacheAdd: vi.fn(async () => Promise.reject({ code: "clone_failed", message: "denied" })) });
    await userEvent.type(field(), "payments");
    await userEvent.click(screen.getByRole("button", { name: "Add repo" }));
    expect(await screen.findByText("denied")).toBeInTheDocument();
    expect(store.getState().repos.addOpen).toBe(true);
  });
});
