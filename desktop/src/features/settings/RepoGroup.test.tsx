import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Api, ConfigListing, Resolution } from "../../api";
import { configListing, fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { groupChosen } from "./settingsSlice";
import { SettingsPane } from "./SettingsPane";

const resolution = (overrides: Partial<Resolution> = {}): Resolution => ({
  id: "github.com/bobcob7/buff-bear",
  ref: null,
  upstream: "https://github.com/bobcob7/buff-bear.git",
  cached: true,
  source: "cache",
  ...overrides,
});

const twoHostsListing = (): ConfigListing => {
  const listing = configListing();
  return {
    ...listing,
    settings: listing.settings.map((setting) =>
      setting.key === "hosts"
        ? { ...setting, value: [{ name: "github.com", kind: "auto" }, { name: "git.example.com", kind: "forgejo" }], set: true }
        : setting,
    ),
  };
};

const open = async (api: Api = fakeApi()) => {
  const store = testStore({ api });
  store.dispatch(groupChosen("repos"));
  renderWithStore(<SettingsPane />, store);
  await screen.findByRole("group", { name: "Default owners" });
  return { api, store };
};

const spec = (): HTMLElement => screen.getByRole("textbox", { name: "Repo spec" });

describe("Repos", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows the owners from config.toml", async () => {
    await open();
    expect(screen.getByText("bobcob7")).toBeInTheDocument();
    expect(screen.getByText("acme")).toBeInTheDocument();
  });

  it("has no default host field", async () => {
    await open();
    expect(screen.queryByRole("textbox", { name: "Default host" })).not.toBeInTheDocument();
  });

  it("lists the hosts in order with the primary marked", async () => {
    await open(fakeApi({ configList: vi.fn(async () => twoHostsListing()) }));
    const hosts = await screen.findByRole("list", { name: "Hosts" });
    expect(within(hosts).getAllByRole("listitem")).toHaveLength(2);
    expect(screen.getByText("primary").closest("li")).toHaveTextContent("github.com");
    expect(hosts).toHaveTextContent("git.example.com");
  });

  it("Edit in Forges opens the Forges group", async () => {
    const { store } = await open();
    await userEvent.click(screen.getByRole("button", { name: "Edit in Forges" }));
    expect(store.getState().settings.group).toBe("forges");
  });

  it("moves an owner up the list", async () => {
    const { api } = await open();
    await userEvent.click(screen.getByRole("button", { name: "Move acme up" }));
    expect(api.configSet).toHaveBeenCalledWith("defaults.owners", ["acme", "bobcob7"]);
  });

  it("removes an owner from the list", async () => {
    const { api } = await open();
    await userEvent.click(screen.getByRole("button", { name: "Remove bobcob7" }));
    expect(api.configSet).toHaveBeenCalledWith("defaults.owners", ["acme"]);
  });

  it("adds an owner when Enter is pressed", async () => {
    const { api } = await open();
    await userEvent.type(screen.getByRole("textbox", { name: "Add owner" }), "platform{Enter}");
    expect(api.configSet).toHaveBeenCalledWith("defaults.owners", ["bobcob7", "acme", "platform"]);
  });

  it("ignores a blank owner", async () => {
    const { api } = await open();
    await userEvent.type(screen.getByRole("textbox", { name: "Add owner" }), "  {Enter}");
    expect(api.configSet).not.toHaveBeenCalled();
  });

  it("says where a cached spec was found", async () => {
    const { api } = await open(fakeApi({ cacheResolve: vi.fn(async () => resolution()) }));
    await userEvent.type(spec(), "buff-bear");
    await userEvent.click(screen.getByRole("button", { name: "Resolve" }));
    expect(await screen.findByRole("status", { name: "Resolution" })).toHaveTextContent("Found by name in the cache.");
    expect(api.cacheResolve).toHaveBeenCalledWith("buff-bear");
  });

  it("says a spec not cached is taken to be on the only host", async () => {
    await open(fakeApi({ cacheResolve: vi.fn(async () => resolution({ cached: false, source: "default" })) }));
    await userEvent.type(spec(), "buff-bear{Enter}");
    expect(await screen.findByRole("status", { name: "Resolution" })).toHaveTextContent("Not cached: taken to be on the only host.");
  });

  it("says a spec found upstream is on a configured host", async () => {
    await open(fakeApi({ cacheResolve: vi.fn(async () => resolution({ cached: false, source: "probe" })) }));
    await userEvent.type(spec(), "buff-bear{Enter}");
    expect(await screen.findByRole("status", { name: "Resolution" })).toHaveTextContent("Found upstream on a configured host.");
  });

  it("lists the candidates of an ambiguous spec, and resolves the one clicked", async () => {
    const ambiguous = { code: "repo_ambiguous", message: "more than one", details: { candidates: ["github.com/bobcob7/api", "github.com/bobcob7/buff-bear"] } };
    const { api } = await open(fakeApi({ cacheResolve: vi.fn(async () => Promise.reject(ambiguous)) }));
    await userEvent.type(spec(), "api{Enter}");
    await userEvent.click(await screen.findByRole("button", { name: "github.com/bobcob7/api" }));
    expect(screen.getByRole("button", { name: "github.com/bobcob7/buff-bear" })).toBeInTheDocument();
    expect(api.cacheResolve).toHaveBeenLastCalledWith("github.com/bobcob7/api");
  });

  it("searches the cache for what is typed, after a pause", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { api } = await open();
    await userEvent.type(spec(), "bea");
    await waitFor(() => expect(api.cacheSearch).toHaveBeenCalledWith("bea", true, 8));
  });
});
