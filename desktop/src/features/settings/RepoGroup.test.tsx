import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Api, ConfigListing, Resolution } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
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

const open = async (api = fakeApi()): Promise<Api> => {
  const store = testStore({ api });
  store.dispatch(groupChosen("repos"));
  renderWithStore(<SettingsPane />, store);
  await screen.findByRole("group", { name: "Default host" });
  return api;
};

const spec = (): HTMLElement => screen.getByRole("textbox", { name: "Repo spec" });

describe("Repos", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows the default host and the owners from config.toml", async () => {
    await open();
    expect(screen.getByRole("textbox", { name: "Default host" })).toHaveValue("github.com");
    expect(screen.getByText("bobcob7")).toBeInTheDocument();
    expect(screen.getByText("acme")).toBeInTheDocument();
  });

  it("saves the default host on blur, trimmed", async () => {
    const api = await open();
    const host = screen.getByRole("textbox", { name: "Default host" });
    await userEvent.clear(host);
    await userEvent.type(host, "  gitlab.example.com ");
    await userEvent.tab();
    expect(api.configSet).toHaveBeenCalledWith("defaults.host", ["gitlab.example.com"]);
  });

  it("shows a rejected host under its control and puts the old value back", async () => {
    await open(
      fakeApi({
        configSet: vi.fn(async () => Promise.reject({ code: "config_value_invalid", message: "not a host name" })),
      }),
    );
    const host = screen.getByRole("textbox", { name: "Default host" });
    await userEvent.clear(host);
    await userEvent.type(host, "bad host");
    await userEvent.tab();
    expect(await screen.findByRole("alert")).toHaveTextContent("not a host name");
    expect(host).toHaveValue("github.com");
  });

  it("resets the default host to its default", async () => {
    const api = await open(
      fakeApi({
        configList: vi.fn(async (): Promise<ConfigListing> => ({
          path: "/c/config.toml",
          settings: [{ key: "defaults.host", type: "string", value: "gitlab.example.com", default: "github.com", set: true }],
        })),
      }),
    );
    await userEvent.click(await screen.findByRole("button", { name: "Reset default host" }));
    expect(api.configUnset).toHaveBeenCalledWith("defaults.host");
  });

  it("moves an owner up the list", async () => {
    const api = await open();
    await userEvent.click(screen.getByRole("button", { name: "Move acme up" }));
    expect(api.configSet).toHaveBeenCalledWith("defaults.owners", ["acme", "bobcob7"]);
  });

  it("removes an owner from the list", async () => {
    const api = await open();
    await userEvent.click(screen.getByRole("button", { name: "Remove bobcob7" }));
    expect(api.configSet).toHaveBeenCalledWith("defaults.owners", ["acme"]);
  });

  it("adds an owner when Enter is pressed", async () => {
    const api = await open();
    await userEvent.type(screen.getByRole("textbox", { name: "Add owner" }), "platform{Enter}");
    expect(api.configSet).toHaveBeenCalledWith("defaults.owners", ["bobcob7", "acme", "platform"]);
  });

  it("ignores a blank owner", async () => {
    const api = await open();
    await userEvent.type(screen.getByRole("textbox", { name: "Add owner" }), "  {Enter}");
    expect(api.configSet).not.toHaveBeenCalled();
  });

  it("says where a cached spec was found", async () => {
    const api = await open(fakeApi({ cacheResolve: vi.fn(async () => resolution()) }));
    await userEvent.type(spec(), "buff-bear");
    await userEvent.click(screen.getByRole("button", { name: "Resolve" }));
    expect(await screen.findByRole("status", { name: "Resolution" })).toHaveTextContent("Found by name in the cache.");
    expect(api.cacheResolve).toHaveBeenCalledWith("buff-bear");
  });

  it("lists the candidates of an ambiguous spec, and resolves the one clicked", async () => {
    const ambiguous = { code: "repo_ambiguous", message: "more than one", details: { candidates: ["github.com/bobcob7/api", "github.com/bobcob7/buff-bear"] } };
    const api = await open(fakeApi({ cacheResolve: vi.fn(async () => Promise.reject(ambiguous)) }));
    await userEvent.type(spec(), "api{Enter}");
    await userEvent.click(await screen.findByRole("button", { name: "github.com/bobcob7/api" }));
    expect(screen.getByRole("button", { name: "github.com/bobcob7/buff-bear" })).toBeInTheDocument();
    expect(api.cacheResolve).toHaveBeenLastCalledWith("github.com/bobcob7/api");
  });

  it("searches the cache for what is typed, after a pause", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const api = await open();
    await userEvent.type(spec(), "bea");
    await waitFor(() => expect(api.cacheSearch).toHaveBeenCalledWith("bea", true, 8));
  });
});
