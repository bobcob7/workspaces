import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api } from "../../api";
import { fakeApi, listedProfile, listedProfiles, profileDetail, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { draftOf, formErrors, nameError, type ProfileForm } from "./ProfileEditor";
import { SettingsPane } from "./SettingsPane";

const form = (overrides: Partial<ProfileForm> = {}): ProfileForm => ({
  name: "docs",
  description: "Docs: accuracy and links",
  patterns: ["*.md"],
  guide: "Check every link.",
  runs: "auto",
  ...overrides,
});

describe("nameError", () => {
  it("takes lowercase letters, digits, and hyphens", () => {
    expect(nameError("go-2", [])).toBeNull();
  });

  it.each([
    ["", "Give the profile a name"],
    ["Docs", "Lowercase letters, digits, and hyphens only"],
    ["my docs", "Lowercase letters, digits, and hyphens only"],
    ["../x", "Lowercase letters, digits, and hyphens only"],
    ["decisions", "decisions is reserved; choose another name"],
    ["security", "A profile named security already exists"],
  ])("refuses %j", (name, message) => {
    expect(nameError(name, ["security"])).toBe(message);
  });
});

describe("formErrors", () => {
  it("has none for a complete new profile", () => {
    expect(formErrors(form(), true, [])).toEqual({});
  });

  it("needs a description of at most 120 characters, and a guide", () => {
    expect(formErrors(form({ description: " ", guide: "\n" }), true, [])).toEqual({
      description: "Say in one line what it reviews",
      guide: "The guide tells the reviewer what to look for; it can't be empty",
    });
    expect(formErrors(form({ description: "x".repeat(121) }), true, []).description).toBe("At most 120 characters");
  });

  it("needs a pattern for a new profile that runs when files match", () => {
    expect(formErrors(form({ patterns: [] }), true, []).patterns).toBe("When files match needs a file pattern");
  });

  it("doesn't check the name of a profile being edited", () => {
    expect(formErrors(form({ name: "security" }), false, ["security"])).toEqual({});
  });
});

describe("draftOf", () => {
  const loaded = profileDetail({ name: "go", description: "Go", when: "auto", patterns: ["*.go"], guide: "Errors." });

  it("passes every field of a new profile, with its Runs as when", () => {
    expect(draftOf(form({ description: " Docs " }), null)).toEqual({
      description: "Docs",
      when: "auto",
      patterns: ["*.md"],
      guide: "Check every link.",
    });
  });

  it("passes only what an edit changed", () => {
    const edited = { name: "go", description: "Go, ours", patterns: ["*.go"], guide: "Errors.", runs: "auto" } as const;
    expect(draftOf(edited, loaded)).toEqual({ description: "Go, ours" });
  });

  it("is null for an edit that changed nothing", () => {
    expect(draftOf({ name: "go", description: "Go", patterns: ["*.go"], guide: "Errors.\n", runs: "auto" }, loaded)).toBeNull();
  });

  it("runs a profile always once its last pattern is removed", () => {
    expect(draftOf({ name: "go", description: "Go", patterns: [], guide: "Errors.", runs: "auto" }, loaded)).toEqual({
      patterns: [],
      when: "always",
    });
  });
});

describe("ProfileEditor", () => {
  const open = async (overrides: Partial<Api> = {}): Promise<Api> => {
    const api = fakeApi(overrides);
    renderWithStore(<SettingsPane />, testStore({ api }));
    await screen.findByText("efficiency");
    return api;
  };

  const dialog = (): HTMLElement => screen.getByRole("dialog");

  const fillNew = async (): Promise<void> => {
    await userEvent.click(screen.getByRole("button", { name: "New profile" }));
    await userEvent.type(screen.getByLabelText("Name"), "docs");
    await userEvent.type(screen.getByLabelText("Description"), "Docs: accuracy and links");
    await userEvent.type(screen.getByLabelText("Add a file pattern"), "*.md{Enter}docs/**,");
    await userEvent.type(screen.getByLabelText("Guide"), "Check every link.");
  };

  it("adds a profile with its patterns, guide, and Runs", async () => {
    const api = await open();
    await fillNew();
    await userEvent.click(screen.getByLabelText("Runs"));
    await userEvent.click(screen.getByRole("option", { name: "When files match" }));
    await userEvent.click(within(dialog()).getByRole("button", { name: "Save" }));
    expect(api.reviewsProfileAdd).toHaveBeenCalledWith("docs", {
      description: "Docs: accuracy and links",
      when: "auto",
      patterns: ["*.md", "docs/**"],
      guide: "Check every link.",
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(api.reviewsProfiles).toHaveBeenCalledTimes(2);
  });

  it("shows errors under the fields and saves nothing until they're fixed", async () => {
    const api = await open();
    await userEvent.click(screen.getByRole("button", { name: "New profile" }));
    await userEvent.type(screen.getByLabelText("Name"), "Go");
    await userEvent.click(within(dialog()).getByRole("button", { name: "Save" }));
    expect(within(dialog()).getByText("Lowercase letters, digits, and hyphens only")).toBeInTheDocument();
    expect(within(dialog()).getByText("Say in one line what it reviews")).toBeInTheDocument();
    await userEvent.clear(screen.getByLabelText("Name"));
    await userEvent.type(screen.getByLabelText("Name"), "go");
    expect(within(dialog()).getByText("A profile named go already exists")).toBeInTheDocument();
    expect(api.reviewsProfileAdd).not.toHaveBeenCalled();
  });

  it("keeps the dialog open with ws's message when saving fails", async () => {
    await open({
      reviewsProfileAdd: vi.fn(async () =>
        Promise.reject({ code: "profile_invalid", message: "invalid profile: match: bad pattern \"[\"" }),
      ),
    });
    await fillNew();
    await userEvent.click(within(dialog()).getByRole("button", { name: "Save" }));
    expect(await within(dialog()).findByText('invalid profile: match: bad pattern "["')).toBeInTheDocument();
    expect(screen.getByLabelText("Guide")).toHaveValue("Check every link.");
  });

  it("shows a taken name from ws under the Name field", async () => {
    await open({
      reviewsProfileAdd: vi.fn(async () => Promise.reject({ code: "profile_exists", message: "a profile named docs already exists" })),
    });
    await fillNew();
    await userEvent.click(within(dialog()).getByRole("button", { name: "Save" }));
    const name = screen.getByLabelText("Name");
    await waitFor(() => expect(name).toHaveAccessibleDescription("a profile named docs already exists"));
  });

  it("edits a built-in, saying saving makes an edited copy, and passes only the changes", async () => {
    const api = await open({
      reviewsProfileShow: vi.fn(async () => profileDetail({ guide: "Anchor it." })),
    });
    await userEvent.click(screen.getByRole("button", { name: "Edit security" }));
    expect(await within(dialog()).findByText(/is built in\. Saving makes an edited copy/)).toBeInTheDocument();
    expect(api.reviewsProfileShow).toHaveBeenCalledWith("security");
    expect(within(dialog()).queryByLabelText("Name")).not.toBeInTheDocument();
    expect(within(dialog()).queryByLabelText("Runs")).not.toBeInTheDocument();
    const guide = screen.getByLabelText("Guide");
    expect(guide).toHaveValue("Anchor it.");
    await userEvent.type(guide, " Webhooks too.");
    await userEvent.click(within(dialog()).getByRole("button", { name: "Save" }));
    expect(api.reviewsProfileEdit).toHaveBeenCalledWith("security", { guide: "Anchor it. Webhooks too." });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("closes without running ws when nothing changed", async () => {
    const api = await open();
    await userEvent.click(screen.getByRole("button", { name: "Edit security" }));
    await within(dialog()).findByLabelText("Guide");
    await userEvent.click(within(dialog()).getByRole("button", { name: "Save" }));
    expect(api.reviewsProfileEdit).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("has no built-in note for a custom profile, and removes a pattern chip", async () => {
    const api = await open({
      reviewsProfiles: vi.fn(async () => [
        ...listedProfiles(),
        listedProfile({ name: "docs", source: "user", builtin: false, when: "always", patterns: ["*.md", "*.mdx"] }),
      ]),
      reviewsProfileShow: vi.fn(async () =>
        profileDetail({ name: "docs", source: "user", builtin: false, when: "always", patterns: ["*.md", "*.mdx"] }),
      ),
    });
    await userEvent.click(screen.getByRole("button", { name: "Edit docs" }));
    const patterns = await within(dialog()).findByRole("group", { name: "File patterns" });
    expect(within(dialog()).queryByText(/is built in/)).not.toBeInTheDocument();
    const chip = within(patterns).getByRole("button", { name: "*.mdx" });
    await userEvent.click(within(chip).getByTestId("CancelIcon"));
    await userEvent.click(within(dialog()).getByRole("button", { name: "Save" }));
    expect(api.reviewsProfileEdit).toHaveBeenCalledWith("docs", { patterns: ["*.md"] });
  });

  it("previews the guide as Markdown", async () => {
    await open();
    await userEvent.click(screen.getByRole("button", { name: "New profile" }));
    await userEvent.type(screen.getByLabelText("Guide"), "Look at **auth**");
    await userEvent.click(screen.getByRole("tab", { name: "Preview" }));
    const preview = screen.getByLabelText("Guide preview");
    expect(within(preview).getByText("auth").tagName).toBe("STRONG");
    await userEvent.click(screen.getByRole("tab", { name: "Write" }));
    expect(screen.getByLabelText("Guide")).toHaveValue("Look at **auth**");
  });

  it("shows why a profile couldn't be read", async () => {
    await open({
      reviewsProfileShow: vi.fn(async () => Promise.reject({ code: "config_invalid", message: "docs.md doesn't parse" })),
    });
    await userEvent.click(screen.getByRole("button", { name: "Edit security" }));
    expect(await within(dialog()).findByText("docs.md doesn't parse")).toBeInTheDocument();
  });
});
