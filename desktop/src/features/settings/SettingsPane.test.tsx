import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api } from "../../api";
import { fakeApi, notificationSettings, openSettings, reviewSettings, testStore } from "../../test/fixtures";
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
      "securityAlwaysDefaultEdit",
      "testsAlwayshaikuEdit",
      "goWhen files matchDefault*.go, go.modEdit",
      "efficiencyOffDefaultEdit",
    ]);
  });

  it("saves a changed row, keeping its model", async () => {
    const api = await open();
    await choose("Runs for go", "Always");
    expect(api.settingsSetReviews).toHaveBeenCalledWith(reviewSettings({ profiles: { go: { runs: "always", model: "" } } }));
  });

  it("saves a model alias for a profile", async () => {
    const api = await open();
    await choose("Model for security", "opus");
    expect(api.settingsSetReviews).toHaveBeenCalledWith(
      reviewSettings({ profiles: { security: { runs: "always", model: "opus" } } }),
    );
  });

  it("saves a typed model id", async () => {
    const api = await open();
    await choose("Model for security", "Model id…");
    await userEvent.type(screen.getByLabelText("Model id for security"), "claude-opus-5-5{Enter}");
    expect(api.settingsSetReviews).toHaveBeenCalledWith(
      reviewSettings({ profiles: { security: { runs: "always", model: "claude-opus-5-5" } } }),
    );
  });

  it("shows a typed model id as the row's model", async () => {
    const api = fakeApi({
      settingsGet: vi.fn(async () => ({
        reviews: reviewSettings({ profiles: { security: { runs: "always", model: "claude-opus-5-5" } } }),
        notifications: notificationSettings(), open: openSettings(),
      })),
    });
    await open(api);
    expect(screen.getByLabelText("Model for security")).toHaveTextContent("claude-opus-5-5");
  });

  it("drops a row set back to what ws lists", async () => {
    const api = fakeApi({
      settingsGet: vi.fn(async () => ({
        reviews: reviewSettings({
          profiles: { go: { runs: "always", model: "" }, tests: { runs: "off", model: "haiku" } },
        }),
        notifications: notificationSettings(), open: openSettings(),
      })),
    });
    await open(api);
    await choose("Runs for go", "When files match");
    expect(api.settingsSetReviews).toHaveBeenCalledWith(
      reviewSettings({ profiles: { tests: { runs: "off", model: "haiku" } } }),
    );
  });

  it("can't run a profile without file patterns when files match", async () => {
    await open();
    await userEvent.click(screen.getByLabelText("Runs for security"));
    expect(screen.getByRole("option", { name: "When files match" })).toHaveAttribute("aria-disabled", "true");
  });

  it("resets to defaults by dropping every change", async () => {
    const api = fakeApi({
      settingsGet: vi.fn(async () => ({
        reviews: reviewSettings({ model: "opus", profiles: { go: { runs: "always", model: "sonnet" } } }),
        notifications: notificationSettings(), open: openSettings(),
      })),
    });
    await open(api);
    await userEvent.click(screen.getByRole("button", { name: "Reset to defaults" }));
    expect(api.settingsSetReviews).toHaveBeenCalledWith(reviewSettings({ model: "opus", profiles: {} }));
  });

  it("has nothing to reset without changes", async () => {
    await open();
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

  it("lists the six groups in order", () => {
    renderWithStore(<SettingsList />, testStore());
    expect(groupNames()).toEqual(["Repos", "Reviews", "Agent roles", "Notifications", "Workspaces", "About"]);
  });

  it("still lists Workspaces in the web app", () => {
    Reflect.deleteProperty(window, "__TAURI_INTERNALS__");
    renderWithStore(<SettingsList />, testStore());
    expect(groupNames()).toContain("Workspaces");
  });
});
