import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Versions } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { commitOf } from "./AboutGroup";
import { SettingsList } from "./SettingsList";
import { groupChosen } from "./settingsSlice";
import { SettingsPane } from "./SettingsPane";

const versions = (overrides: Partial<Versions> = {}): Versions => ({
  app: { version: "0.1.0", commit: "v0.3.0-4-gdc313a2" },
  ws: { version: "v0.3.0-4-gdc313a2", path: "/usr/local/bin/ws", error: null },
  claude: { version: "2.1.303", path: "/opt/homebrew/bin/claude", error: null },
  ...overrides,
});

const renderAbout = (v: Versions): ReturnType<typeof fakeApi> => {
  const api = fakeApi({ versions: vi.fn(async () => v) });
  const store = testStore({ api });
  store.dispatch(groupChosen("about"));
  renderWithStore(<SettingsPane />, store);
  return api;
};

describe("commitOf", () => {
  it.each([
    ["v0.3.0-4-gdc313a2", "dc313a2"],
    ["v0.3.0-4-gdc313a2-dirty", "dc313a2"],
    ["dc313a2", "dc313a2"],
    ["v0.0.0-20261028223004-c9fbb6607371", "c9fbb66"],
    ["v0.0.0-20261028223004-c9fbb6607371+dirty", "c9fbb66"],
    ["dev-f513b70", "f513b70"],
    ["v0.3.0", null],
    ["dev", null],
    [null, null],
  ])("%s is %s", (version, want) => {
    expect(commitOf(version)).toBe(want);
  });
});

describe("About", () => {
  it("is in the settings list", async () => {
    const store = testStore();
    renderWithStore(<SettingsList />, store);
    await userEvent.click(screen.getByRole("button", { name: "About" }));
    expect(store.getState().settings.group).toBe("about");
  });

  it("shows the app's, ws's, and claude's versions and paths", async () => {
    renderAbout(versions());
    const app = await screen.findByRole("group", { name: "Workspaces app" });
    expect(within(app).getByText("0.1.0")).toBeInTheDocument();
    expect(within(app).getByText("v0.3.0-4-gdc313a2")).toBeInTheDocument();
    const ws = screen.getByRole("group", { name: "ws" });
    expect(within(ws).getByText("v0.3.0-4-gdc313a2")).toBeInTheDocument();
    expect(within(ws).getByText("/usr/local/bin/ws")).toBeInTheDocument();
    expect(within(screen.getByRole("group", { name: "claude" })).getByText("2.1.303")).toBeInTheDocument();
    expect(screen.queryByText(/different commits/)).not.toBeInTheDocument();
  });

  it("warns when ws and the app were built from different commits", async () => {
    renderAbout(versions({ ws: { version: "v0.0.0-20261028223004-c9fbb6607371", path: "/w/ws", error: null } }));
    expect(await screen.findByText(/ws \(c9fbb66\) and the app \(dc313a2\) were built from different commits/)).toBeInTheDocument();
  });

  it("says why a version couldn't be read", async () => {
    renderAbout(versions({ ws: { version: null, path: null, error: "ws isn't installed" } }));
    const ws = await screen.findByRole("group", { name: "ws" });
    expect(within(ws).getByText("ws isn't installed")).toBeInTheDocument();
    expect(within(ws).getByText("Not found on the app's PATH")).toBeInTheDocument();
  });

  it("checks again", async () => {
    const api = renderAbout(versions());
    await userEvent.click(await screen.findByRole("button", { name: "Check again" }));
    expect(api.versions).toHaveBeenCalledTimes(2);
  });
});
