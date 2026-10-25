import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, reviewSettings, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { saveReviewSettings, settingsReducer } from "./settingsSlice";
import { SettingsPane } from "./SettingsPane";

describe("SettingsPane", () => {
  it("saves a rule as soon as it's changed", async () => {
    const api = fakeApi();
    renderWithStore(<SettingsPane />, testStore({ api }));
    await userEvent.click(await screen.findByRole("checkbox", { name: "Only when my review is requested" }));
    expect(api.settingsSetReviews).toHaveBeenCalledWith(reviewSettings({ onlyRequested: true }));
  });

  it("saves the review task when it loses focus", async () => {
    const api = fakeApi();
    renderWithStore(<SettingsPane />, testStore({ api }));
    const task = await screen.findByRole("textbox", { name: "Review task" });
    await userEvent.type(task, " now");
    expect(api.settingsSetReviews).not.toHaveBeenCalled();
    await userEvent.tab();
    expect(api.settingsSetReviews).toHaveBeenCalledWith(reviewSettings({ taskTemplate: "Review pull request {repo}#{number} now" }));
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

describe("settingsReducer", () => {
  it("clears a control's error once it saves", () => {
    const failed = settingsReducer(
      undefined,
      saveReviewSettings.rejected(null, "r1", { skipDrafts: false }, { code: "x", message: "nope" }),
    );
    expect(failed.fieldErrors.skipDrafts).toBe("nope");
    const saved = settingsReducer(failed, saveReviewSettings.fulfilled({ reviews: reviewSettings() }, "r2", { skipDrafts: false }));
    expect(saved.fieldErrors.skipDrafts).toBeUndefined();
  });
});
