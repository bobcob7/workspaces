import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api, ListedProfile } from "../../api";
import { fakeApi, listedProfile, listedProfiles, reviewSettings, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { SettingsPane } from "./SettingsPane";

const docs = listedProfile({ name: "docs", description: "Docs", builtin: false, source: "user" });
const editedTests = listedProfile({ name: "tests", description: "Tests, ours", builtin: false, source: "override" });

const profiles = (): readonly ListedProfile[] => [
  ...listedProfiles().map((p) => (p.name === "tests" ? editedTests : p)),
  docs,
];

const open = async (overrides: Partial<Api> = {}): Promise<Api> => {
  const api = fakeApi({ reviewsProfiles: vi.fn(async () => profiles()), ...overrides });
  renderWithStore(<SettingsPane />, testStore({ api }));
  await screen.findByText("docs");
  return api;
};

const rowOf = (name: string): HTMLElement => {
  const row = screen.getByText(name).closest("tr");
  if (row === null) {
    throw new Error(`no row for ${name}`);
  }
  return row;
};

describe("Reviewer profiles actions", () => {
  it("badges custom and edited profiles, and not built-ins", async () => {
    await open();
    expect(within(rowOf("docs")).getByText("custom")).toBeInTheDocument();
    expect(within(rowOf("tests")).getByText("edited")).toBeInTheDocument();
    expect(within(rowOf("security")).queryByText(/custom|edited/)).not.toBeInTheDocument();
  });

  it("offers Edit on every row, Delete for custom, and Reset to built-in for edited", async () => {
    await open();
    for (const name of ["security", "tests", "go", "efficiency", "docs"]) {
      expect(screen.getByRole("button", { name: `Edit ${name}` })).toBeInTheDocument();
    }
    expect(screen.getByRole("button", { name: "Delete docs" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reset tests to built-in" })).toBeInTheDocument();
    expect(within(rowOf("security")).queryByRole("button", { name: /Delete|Reset/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete tests" })).not.toBeInTheDocument();
  });

  it("asks before deleting, saying nothing comes back", async () => {
    const api = await open();
    await userEvent.click(screen.getByRole("button", { name: "Delete docs" }));
    const dialog = screen.getByRole("dialog", { name: "Delete docs?" });
    expect(dialog).toHaveTextContent("It isn't built in, so nothing comes back");
    expect(api.reviewsProfileRemove).not.toHaveBeenCalled();
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(api.reviewsProfileRemove).not.toHaveBeenCalled();
  });

  it("deletes a custom profile, lists the profiles again, and takes the settings rm left", async () => {
    const api = await open({
      settingsGet: vi.fn(async () => ({
        reviews: reviewSettings({ profiles: { docs: { runs: "off", model: "" }, go: { runs: "always", model: "" } } }),
      })),
      reviewsProfileRemove: vi.fn(async () => ({
        restored: false,
        settings: { reviews: reviewSettings({ profiles: { go: { runs: "always", model: "" } } }) },
      })),
    });
    expect(screen.getByLabelText("Runs for docs")).toHaveTextContent("Off");
    vi.mocked(api.reviewsProfiles).mockResolvedValue(listedProfiles());
    await userEvent.click(screen.getByRole("button", { name: "Delete docs" }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete" }));
    expect(api.reviewsProfileRemove).toHaveBeenCalledWith("docs");
    await waitFor(() => expect(screen.queryByText("docs")).not.toBeInTheDocument());
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(api.reviewsProfiles).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("button", { name: "Reset to defaults" })).toBeEnabled();
  });

  it("asks before resetting, saying the built-in comes back", async () => {
    const api = await open({
      reviewsProfileRemove: vi.fn(async () => ({ restored: true, settings: { reviews: reviewSettings() } })),
    });
    await userEvent.click(screen.getByRole("button", { name: "Reset tests to built-in" }));
    const dialog = screen.getByRole("dialog", { name: "Reset tests to built-in?" });
    expect(dialog).toHaveTextContent("The built-in tests comes back");
    expect(dialog).toHaveTextContent("Its Runs and Model here stay");
    await userEvent.click(within(dialog).getByRole("button", { name: "Reset to built-in" }));
    expect(api.reviewsProfileRemove).toHaveBeenCalledWith("tests");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("keeps the dialog open with ws's message when removing fails", async () => {
    await open({
      reviewsProfileRemove: vi.fn(async () =>
        Promise.reject({ code: "profile_builtin", message: "profile is built in: set it to off instead" }),
      ),
    });
    await userEvent.click(screen.getByRole("button", { name: "Delete docs" }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete" }));
    expect(await within(screen.getByRole("dialog")).findByText("profile is built in: set it to off instead")).toBeInTheDocument();
  });

  it("opens an empty editor from New profile, beside Reset to defaults", async () => {
    await open();
    const newProfile = screen.getByRole("button", { name: "New profile" });
    expect(newProfile.nextElementSibling).toBe(screen.getByRole("button", { name: "Reset to defaults" }));
    await userEvent.click(newProfile);
    const dialog = screen.getByRole("dialog", { name: "New reviewer profile" });
    expect(within(dialog).getByLabelText("Name")).toHaveValue("");
    expect(within(dialog).getByLabelText("Guide")).toHaveValue("");
  });
});
