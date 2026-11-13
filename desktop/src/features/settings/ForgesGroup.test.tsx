import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api, HostView } from "../../api";
import { fakeApi, hostView, reviewsView, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchReviews } from "../reviews/reviewsSlice";
import { ForgesGroup } from "./ForgesGroup";

const forgejoHost = (overrides: Partial<HostView> = {}): HostView =>
  hostView({
    host: "git.example.com",
    forge: "forgejo",
    choice: "auto",
    login: "bob",
    token: "file",
    tokenEnv: "WS_FORGEJO_TOKEN_GIT_EXAMPLE_COM",
    ...overrides,
  });

const renderGroup = (hosts = [forgejoHost(), hostView({ host: "github.com", forge: "github", login: "bobcob7" })], api: Api = fakeApi()) => {
  const store = testStore({ api });
  store.dispatch(fetchReviews.fulfilled(reviewsView({ hosts }), "req"));
  renderWithStore(<ForgesGroup />, store);
  return store;
};

describe("ForgesGroup", () => {
  it("lists every host with its forge choice", () => {
    renderGroup();
    const table = screen.getByRole("table", { name: "Forges" });
    expect(within(table).getByText("git.example.com")).toBeInTheDocument();
    expect(within(table).getByText("github.com")).toBeInTheDocument();
    expect(screen.getByLabelText("Forge for git.example.com")).toHaveTextContent("Auto (detected: Forgejo)");
    expect(screen.getByLabelText("Forge for github.com")).toHaveTextContent("Auto (detected: GitHub)");
  });

  it("says none in the Auto choice when no forge is detected", () => {
    renderGroup([hostView({ host: "gitlab.example.com", forge: null })]);
    expect(screen.getByLabelText("Forge for gitlab.example.com")).toHaveTextContent("Auto (detected: none)");
  });

  it("changing the forge calls reviewsSetForge", async () => {
    const api = fakeApi();
    renderGroup(undefined, api);
    await userEvent.click(screen.getByLabelText("Forge for git.example.com"));
    await userEvent.click(screen.getByRole("option", { name: "GitHub" }));
    expect(api.reviewsSetForge).toHaveBeenCalledWith("git.example.com", "github");
  });

  it("saving a token calls reviewsSetToken and closes the dialog", async () => {
    const hosts = [forgejoHost(), hostView({ host: "github.com", forge: "github", login: "bobcob7" })];
    const api = fakeApi({ reviewsSetToken: vi.fn(async () => reviewsView({ hosts })) });
    renderGroup(hosts, api);
    await userEvent.click(screen.getByRole("button", { name: "Set token" }));
    await userEvent.type(screen.getByLabelText("Token for git.example.com"), "abc123");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(api.reviewsSetToken).toHaveBeenCalledWith("git.example.com", "abc123");
    await waitFor(() => expect(screen.queryByLabelText("Token for git.example.com")).not.toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "Set token" }));
    expect(screen.getByLabelText("Token for git.example.com")).toHaveValue("");
  });

  it("Remove token calls reviewsSetToken with an empty token", async () => {
    const api = fakeApi();
    renderGroup(undefined, api);
    await userEvent.click(screen.getByRole("button", { name: "Remove token" }));
    expect(api.reviewsSetToken).toHaveBeenCalledWith("git.example.com", "");
  });

  it("shows a host's error", () => {
    renderGroup([forgejoHost({ error: { code: "forge_auth", message: "Forgejo rejected the token for git.example.com." } })]);
    expect(screen.getByText("Forgejo rejected the token for git.example.com.")).toBeInTheDocument();
  });
});
