import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api, ConfigListing } from "../../api";
import { fakeApi, listedProfiles, notificationSettings, openSettings, reviewSettings, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { SettingsList } from "./SettingsList";
import { groupChosen, saveReviewSettings, settingsReducer } from "./settingsSlice";
import { SettingsPane } from "./SettingsPane";

describe("SettingsPane", () => {
  it("saves a rule as soon as it's changed", async () => {
    const api = fakeApi();
    renderWithStore(<SettingsPane />, testStore({ api }));
    await userEvent.click(await screen.findByRole("checkbox", { name: "Only when my review is requested" }));
    expect(api.settingsSetReviews).toHaveBeenCalledWith(reviewSettings({ onlyRequested: true }));
  });

  it("has no Review task setting", async () => {
    renderWithStore(<SettingsPane />, testStore());
    await screen.findByRole("group", { name: "Review agent" });
    expect(screen.queryByRole("group", { name: "Review task" })).not.toBeInTheDocument();
  });

  it("saves the review agent's model and permission mode", async () => {
    const api = fakeApi();
    renderWithStore(<SettingsPane />, testStore({ api }));
    await userEvent.click(await screen.findByLabelText("Review agent model"));
    await userEvent.click(screen.getByRole("option", { name: "opus" }));
    expect(api.settingsSetReviews).toHaveBeenCalledWith(reviewSettings({ model: "opus" }));
  });

  it("shows a failed save under its control and keeps the old value", async () => {
    const api = fakeApi({
      settingsSetReviews: vi.fn(async () => Promise.reject({ code: "setting_invalid", message: "at most 1 to 20" })),
    });
    renderWithStore(<SettingsPane />, testStore({ api }));
    const max = await screen.findByLabelText("Auto-reviews at once");
    await userEvent.clear(max);
    await userEvent.type(max, "0");
    await userEvent.tab();
    expect(await screen.findByRole("alert")).toHaveTextContent("at most 1 to 20");
    expect(max).toHaveValue(2);
  });
});

describe("Reviewer profiles", () => {
  const open = async (api = fakeApi()): Promise<Api> => {
    renderWithStore(<SettingsPane />, testStore({ api }));
    await screen.findByText("efficiency");
    return api;
  };

  const choose = async (label: string, option: string): Promise<void> => {
    await userEvent.click(screen.getByLabelText(label));
    await userEvent.click(screen.getByRole("option", { name: option }));
  };

  it("lists every profile ws lists, with when it runs, its model, and its files", async () => {
    await open();
    const rows = within(screen.getByRole("table")).getAllByRole("row").slice(1);
    expect(rows.map((r) => r.textContent?.replaceAll("\u200b", ""))).toEqual([
      "securityAlwaysDefaultEditReset",
      "testsAlwayshaikuEdit",
      "goWhen files matchDefault*.go, go.modEdit",
      "efficiencyOffDefaultEdit",
    ]);
  });

  it("saves a changed Runs to config.toml, then lists the profiles again", async () => {
    const api = await open();
    vi.mocked(api.reviewsProfiles).mockClear();
    await choose("Runs for go", "Always");
    expect(api.configSet).toHaveBeenCalledWith("review.profiles.go.when", ["always"]);
    await waitFor(() => expect(api.reviewsProfiles).toHaveBeenCalledTimes(1));
    expect(api.settingsSetReviews).not.toHaveBeenCalled();
  });

  it("saves a model alias for a profile", async () => {
    const api = await open();
    await choose("Model for security", "opus");
    expect(api.configSet).toHaveBeenCalledWith("review.profiles.security.model", ["opus"]);
  });

  it("saves a typed model id", async () => {
    const api = await open();
    await choose("Model for security", "Model id…");
    await userEvent.type(screen.getByLabelText("Model id for security"), "claude-opus-5-5{Enter}");
    expect(api.configSet).toHaveBeenCalledWith("review.profiles.security.model", ["claude-opus-5-5"]);
  });

  it("unsets a model set back to Default", async () => {
    const api = await open();
    await choose("Model for tests", "Default");
    expect(api.configUnset).toHaveBeenCalledWith("review.profiles.tests.model");
    expect(api.configSet).not.toHaveBeenCalledWith("review.profiles.tests.model", expect.anything());
  });

  it("shows a model id from config.toml as the row's model", async () => {
    const api = fakeApi({
      reviewsProfiles: vi.fn(async () =>
        listedProfiles().map((p) => (p.name === "security" ? { ...p, model: "claude-opus-5-5" } : p)),
      ),
    });
    await open(api);
    expect(screen.getByLabelText("Model for security")).toHaveTextContent("claude-opus-5-5");
  });

  it("resets one row's keys with its Reset button, and only for a row config.toml sets", async () => {
    const api = await open();
    expect(screen.queryByRole("button", { name: "Reset go" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Reset security" }));
    expect(api.configUnset).toHaveBeenCalledWith("review.profiles.security.model");
    expect(api.configUnset).not.toHaveBeenCalledWith("review.profiles.security.when");
  });

  it("can't run a profile without file patterns when files match", async () => {
    await open();
    await userEvent.click(screen.getByLabelText("Runs for security"));
    expect(screen.getByRole("option", { name: "When files match" })).toHaveAttribute("aria-disabled", "true");
  });

  it("resets to defaults by unsetting every review.profiles key config.toml sets", async () => {
    const api = await open();
    await userEvent.click(screen.getByRole("button", { name: "Reset to defaults" }));
    expect(api.configUnset).toHaveBeenCalledWith("review.profiles.security.model");
    expect(api.configUnset).toHaveBeenCalledTimes(1);
    expect(api.settingsSetReviews).not.toHaveBeenCalled();
  });

  it("has nothing to reset when config.toml sets no profile", async () => {
    await open(fakeApi({ configList: vi.fn(async (): Promise<ConfigListing> => ({ path: "/c/config.toml", settings: [] })) }));
    expect(screen.getByRole("button", { name: "Reset to defaults" })).toBeDisabled();
  });

  it("shows why the profiles couldn't be listed", async () => {
    const api = fakeApi({
      reviewsProfiles: vi.fn(async () => Promise.reject({ code: "ws_missing", message: "`ws` isn't on PATH" })),
    });
    renderWithStore(<SettingsPane />, testStore({ api }));
    expect(await screen.findByText("`ws` isn't on PATH")).toBeInTheDocument();
  });
});

describe("settingsReducer", () => {
  it("clears a control's error once it saves", () => {
    const failed = settingsReducer(
      undefined,
      saveReviewSettings.rejected(null, "r1", { skipDrafts: false }, { code: "x", message: "nope" }),
    );
    expect(failed.fieldErrors.skipDrafts).toBe("nope");
    const saved = settingsReducer(failed, saveReviewSettings.fulfilled({ reviews: reviewSettings(), notifications: notificationSettings(), open: openSettings() }, "r2", { skipDrafts: false }));
    expect(saved.fieldErrors.skipDrafts).toBeUndefined();
  });
});

describe("Notifications", () => {
  const open = async (api = fakeApi()): Promise<Api> => {
    const store = testStore({ api });
    store.dispatch(groupChosen("notifications"));
    renderWithStore(<SettingsPane />, store);
    await screen.findByRole("group", { name: "Notify me" });
    return api;
  };

  it("shows the three checkboxes, checked by default", async () => {
    await open();
    for (const name of ["Show notifications", "When an agent needs input", "When an agent is done"]) {
      expect(screen.getByRole("checkbox", { name })).toBeChecked();
    }
  });

  it("saves a change to When an agent is done", async () => {
    const api = await open();
    await userEvent.click(screen.getByRole("checkbox", { name: "When an agent is done" }));
    expect(api.settingsSetNotifications).toHaveBeenCalledWith({ enabled: true, needsInput: true, done: false });
  });

  it("disables the other two checkboxes when notifications are off", async () => {
    await open(fakeApi({ settingsGet: vi.fn(async () => ({ reviews: reviewSettings(), notifications: notificationSettings({ enabled: false }), open: openSettings() })) }));
    expect(screen.getByRole("checkbox", { name: "Show notifications" })).toBeEnabled();
    expect(screen.getByRole("checkbox", { name: "When an agent needs input" })).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "When an agent is done" })).toBeDisabled();
  });

  it("shows why a change didn't save as an alert", async () => {
    await open(fakeApi({ settingsSetNotifications: vi.fn(async () => Promise.reject({ code: "state_newer", message: "newer" })) }));
    await userEvent.click(screen.getByRole("checkbox", { name: "When an agent is done" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("newer");
  });
});

describe("Workspaces", () => {
  const open = async (api = fakeApi()): Promise<Api> => {
    const store = testStore({ api });
    store.dispatch(groupChosen("workspaces"));
    renderWithStore(<SettingsPane />, store);
    await screen.findByRole("group", { name: "File manager" });
    return api;
  };

  it("shows the default commands as placeholders", async () => {
    await open();
    await waitFor(() => expect(screen.getByLabelText("File manager command")).toHaveAttribute("placeholder", "open"));
    expect(screen.getByLabelText("Editor command")).toHaveAttribute("placeholder", "code");
  });

  it("saves the editor command on blur, trimmed", async () => {
    const api = await open();
    await userEvent.type(screen.getByLabelText("Editor command"), "  zed  ");
    await userEvent.tab();
    expect(api.settingsSetOpen).toHaveBeenCalledWith({ fileManager: "", editor: "zed" });
  });

  it("shows a rejected command under its field and reverts it", async () => {
    await open(
      fakeApi({
        settingsSetOpen: vi.fn(async () =>
          Promise.reject({ code: "setting_invalid", message: 'the editor command has an unclosed "' }),
        ),
      }),
    );
    const editor = screen.getByLabelText("Editor command");
    await userEvent.type(editor, '"');
    await userEvent.tab();
    expect(await screen.findByRole("alert")).toHaveTextContent('the editor command has an unclosed "');
    expect(editor).toHaveValue("");
  });
});

describe("SettingsList", () => {
  const groupNames = (): (string | null)[] => {
    const items = within(screen.getByRole("navigation", { name: "Settings groups" })).getAllByRole("button");
    return items.map((item) => item.textContent);
  };

  it("lists the seven groups in order", () => {
    renderWithStore(<SettingsList />, testStore());
    expect(groupNames()).toEqual(["Repos", "Reviews", "Forges", "Agent roles", "Notifications", "Workspaces", "About"]);
  });

  it("still lists Workspaces in the web app", () => {
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
    renderWithStore(<SettingsList />, testStore());
    expect(groupNames()).toContain("Workspaces");
  });
});

describe("Reviews config", () => {
  const open = async (api = fakeApi()): Promise<Api> => {
    renderWithStore(<SettingsPane />, testStore({ api }));
    await screen.findByRole("group", { name: "Wait for a review" });
    return api;
  };

  it("saves the review wait timeout to config.toml", async () => {
    const api = await open();
    await userEvent.click(screen.getByLabelText("Review wait timeout"));
    await userEvent.click(screen.getByRole("option", { name: "2 hours" }));
    expect(api.configSet).toHaveBeenCalledWith("review.timeout", ["2h"]);
  });

  it("shows a wait timeout that isn't a choice as its raw value", async () => {
    await open(
      fakeApi({
        configList: vi.fn(async (): Promise<ConfigListing> => ({
          path: "/c/config.toml",
          settings: [{ key: "review.timeout", type: "duration", value: "45m", default: "1h", set: true }],
        })),
      }),
    );
    expect(screen.getByLabelText("Review wait timeout")).toHaveTextContent("45m");
  });

  it("resets the wait timeout only when config.toml sets it", async () => {
    const api = await open(
      fakeApi({
        configList: vi.fn(async (): Promise<ConfigListing> => ({
          path: "/c/config.toml",
          settings: [{ key: "review.timeout", type: "duration", value: "2h", default: "1h", set: true }],
        })),
      }),
    );
    await userEvent.click(within(screen.getByRole("group", { name: "Wait for a review" })).getByRole("button", { name: "Reset" }));
    expect(api.configUnset).toHaveBeenCalledWith("review.timeout");
  });

  it("has no wait timeout Reset when config.toml leaves it at its default", async () => {
    await open();
    expect(within(screen.getByRole("group", { name: "Wait for a review" })).queryByRole("button", { name: "Reset" })).not.toBeInTheDocument();
  });

  it("unchecking clean-up saves review.remove_finished as false", async () => {
    const api = await open();
    await userEvent.click(screen.getByRole("checkbox", { name: "Remove a review's agent and workspace once every reviewer has reported" }));
    expect(api.configSet).toHaveBeenCalledWith("review.remove_finished", ["false"]);
  });

  it("moves legacy profile overrides into config.toml, then clears them", async () => {
    const api = await open(
      fakeApi({
        settingsGet: vi.fn(async () => ({
          reviews: reviewSettings({ profiles: { go: { runs: "off", model: "" } } }),
          notifications: notificationSettings(),
          open: openSettings(),
        })),
      }),
    );
    expect(screen.getByText(/This app still overrides 1 profile\(s\)/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Move to ws config" }));
    expect(api.configSet).toHaveBeenCalledWith("review.profiles.go.when", ["off"]);
    expect(api.configUnset).toHaveBeenCalledWith("review.profiles.go.model");
    expect(api.settingsSetReviews).toHaveBeenCalledWith(reviewSettings({ profiles: {} }));
    await waitFor(() => expect(screen.queryByText(/This app still overrides/)).not.toBeInTheDocument());
  });

  it("shows why the move stopped, and keeps the overrides", async () => {
    const api = await open(
      fakeApi({
        settingsGet: vi.fn(async () => ({
          reviews: reviewSettings({ profiles: { go: { runs: "off", model: "" } } }),
          notifications: notificationSettings(),
          open: openSettings(),
        })),
        configSet: vi.fn(async () => Promise.reject({ code: "config_value_invalid", message: "not a valid when" })),
      }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Move to ws config" }));
    expect(await screen.findByText("not a valid when")).toBeInTheDocument();
    expect(api.settingsSetReviews).not.toHaveBeenCalled();
    expect(screen.getByText(/This app still overrides 1 profile/)).toBeInTheDocument();
  });
});

describe("Workspaces in the web app", () => {
  it("notes that the web app has no Open buttons, and keeps both commands", async () => {
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
    const store = testStore();
    store.dispatch(groupChosen("workspaces"));
    renderWithStore(<SettingsPane />, store);
    expect(await screen.findByText(/The web app has no Open buttons/)).toBeInTheDocument();
    expect(screen.getByLabelText("File manager command")).toBeInTheDocument();
    expect(screen.getByLabelText("Editor command")).toBeInTheDocument();
  });
});
