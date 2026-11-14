import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api, HostView } from "../../api";
import { fakeApi, hostView, reviewsView, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchReviews } from "../reviews/reviewsSlice";
import { ForgesGroup, hostProblem, moved } from "./ForgesGroup";

const githubHost = (overrides: Partial<HostView> = {}): HostView =>
  hostView({ host: "github.com", forge: "github", configured: true, primary: true, login: "bobcob7", ...overrides });

const forgejoHost = (overrides: Partial<HostView> = {}): HostView =>
  hostView({
    host: "git.example.com",
    forge: "forgejo",
    choice: "auto",
    configured: true,
    login: "bob",
    token: "file",
    tokenEnv: "WS_FORGEJO_TOKEN_GIT_EXAMPLE_COM",
    ...overrides,
  });

const renderGroup = (hosts: readonly HostView[] = [githubHost(), forgejoHost()], api: Api = fakeApi()) => {
  const store = testStore({ api });
  store.dispatch(fetchReviews.fulfilled(reviewsView({ hosts }), "req"));
  renderWithStore(<ForgesGroup />, store);
  return store;
};

describe("ForgesGroup", () => {
  it("lists configured hosts in order with the primary marked", () => {
    renderGroup([githubHost(), forgejoHost()]);
    const table = screen.getByRole("table", { name: "Hosts" });
    const rows = within(table).getAllByRole("row");
    expect(rows).toHaveLength(3);
    expect(screen.getByText("primary").closest("tr")).toHaveTextContent("github.com");
    expect(rows[2]).toHaveTextContent("git.example.com");
  });

  it("says none in the Auto choice when no forge is detected", () => {
    renderGroup([hostView({ host: "gitlab.example.com", forge: null })]);
    expect(screen.getByLabelText("Forge for gitlab.example.com")).toHaveTextContent("Auto (detected: none)");
  });

  it("changing a kind sends the whole list", async () => {
    const api = fakeApi();
    renderGroup(undefined, api);
    await userEvent.click(screen.getByLabelText("Forge for git.example.com"));
    await userEvent.click(screen.getByRole("option", { name: "GitHub" }));
    expect(api.reviewsSetHosts).toHaveBeenCalledWith([
      { name: "github.com", kind: "auto" },
      { name: "git.example.com", kind: "github" },
    ]);
  });

  it("moves a host up", async () => {
    const api = fakeApi();
    renderGroup(undefined, api);
    await userEvent.click(screen.getByRole("button", { name: "Move git.example.com up" }));
    expect(api.reviewsSetHosts).toHaveBeenCalledWith([
      { name: "git.example.com", kind: "auto" },
      { name: "github.com", kind: "auto" },
    ]);
  });

  it("has no move up on the first row and no move down on the last", () => {
    renderGroup();
    expect(screen.queryByRole("button", { name: "Move github.com up" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Move git.example.com down" })).not.toBeInTheDocument();
  });

  it("removes a host", async () => {
    const api = fakeApi();
    renderGroup(undefined, api);
    await userEvent.click(screen.getByRole("button", { name: "Remove git.example.com" }));
    expect(api.reviewsSetHosts).toHaveBeenCalledWith([{ name: "github.com", kind: "auto" }]);
  });

  it("adds a typed host, lowercased, with its kind", async () => {
    const api = fakeApi();
    renderGroup(undefined, api);
    await userEvent.type(screen.getByRole("textbox", { name: "New host" }), "Git.Example.com:3000");
    await userEvent.click(screen.getByLabelText("Forge for new host"));
    await userEvent.click(screen.getByRole("option", { name: "Forgejo" }));
    await userEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(api.reviewsSetHosts).toHaveBeenCalledWith([
      { name: "github.com", kind: "auto" },
      { name: "git.example.com", kind: "auto" },
      { name: "git.example.com:3000", kind: "forgejo" },
    ]);
  });

  it("adds a host when Enter is pressed", async () => {
    const api = fakeApi();
    renderGroup(undefined, api);
    await userEvent.type(screen.getByRole("textbox", { name: "New host" }), "other.example.com{Enter}");
    expect(api.reviewsSetHosts).toHaveBeenCalledWith([
      { name: "github.com", kind: "auto" },
      { name: "git.example.com", kind: "auto" },
      { name: "other.example.com", kind: "auto" },
    ]);
  });

  it("refuses a bad or repeated host without saving", async () => {
    const api = fakeApi();
    renderGroup(undefined, api);
    const field = screen.getByRole("textbox", { name: "New host" });
    await userEvent.type(field, "a/b");
    expect(screen.getByText("Not a host name")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Add" }));
    await userEvent.clear(field);
    await userEvent.type(field, "github.com");
    expect(screen.getByText("Already in the list")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(api.reviewsSetHosts).not.toHaveBeenCalled();
  });

  it("adds an other host with auto", async () => {
    const api = fakeApi();
    renderGroup([githubHost(), hostView({ host: "ghe.example.com", forge: "github", configured: false })], api);
    await userEvent.click(screen.getByRole("button", { name: "Add ghe.example.com" }));
    expect(api.reviewsSetHosts).toHaveBeenCalledWith([
      { name: "github.com", kind: "auto" },
      { name: "ghe.example.com", kind: "auto" },
    ]);
  });

  it("shows the error when saving the hosts fails", async () => {
    const api = fakeApi({
      reviewsSetHosts: vi.fn(async () => Promise.reject({ code: "config_value_invalid", message: "not a host name" })),
    });
    renderGroup(undefined, api);
    await userEvent.click(screen.getByRole("button", { name: "Remove git.example.com" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("not a host name");
  });

  it("refreshes the config after saving", async () => {
    const api = fakeApi();
    renderGroup(undefined, api);
    await userEvent.click(screen.getByRole("button", { name: "Remove git.example.com" }));
    await waitFor(() => expect(api.configList).toHaveBeenCalled());
  });

  it("saving a token calls reviewsSetToken and closes the dialog", async () => {
    const hosts = [forgejoHost(), githubHost({ primary: false })];
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
    renderGroup([githubHost(), forgejoHost({ error: { code: "forge_auth", message: "Forgejo rejected the token for git.example.com." } })]);
    expect(screen.getByText("Forgejo rejected the token for git.example.com.")).toBeInTheDocument();
  });
});

describe("moved", () => {
  it("swaps the item at index with its neighbour", () => {
    expect(moved(["a", "b", "c"], 2, -1)).toEqual(["a", "c", "b"]);
    expect(moved(["a", "b", "c"], 0, 1)).toEqual(["b", "a", "c"]);
  });

  it("returns the list unchanged when the item can't move", () => {
    const list = ["a", "b"];
    expect(moved(list, 0, -1)).toBe(list);
    expect(moved(list, 1, 1)).toBe(list);
  });
});

describe("hostProblem", () => {
  it("is empty for a new host name", () => {
    expect(hostProblem("git.example.com:3000", [{ name: "github.com", kind: "auto" }])).toBe("");
  });

  it("reports a name that is not a host", () => {
    expect(hostProblem("a/b", [])).toBe("Not a host name");
  });

  it("reports a host already in the list", () => {
    expect(hostProblem("github.com", [{ name: "github.com", kind: "auto" }])).toBe("Already in the list");
  });
});

describe("token help", () => {
  it("lists the GitHub and Forgejo access a user needs", () => {
    renderGroup();
    const help = screen.getByText("What access do the credentials need?").closest("details");
    expect(help).not.toBeNull();
    expect(help).toHaveTextContent("gh auth login");
    expect(help).toHaveTextContent("Pull requests (read)");
    expect(help).toHaveTextContent("read:repository");
    expect(help).toHaveTextContent("read:user");
    expect(help).toHaveTextContent("WS_FORGEJO_TOKEN_");
  });
});
