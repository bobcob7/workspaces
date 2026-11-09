import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { RolesGroup } from "./RolesGroup";

const open = async (overrides: Partial<Api> = {}): Promise<Api> => {
  const api = fakeApi(overrides);
  renderWithStore(<RolesGroup />, testStore({ api }));
  await screen.findByText("implementer");
  return api;
};

const dialog = (): HTMLElement => screen.getByRole("dialog");

const fillNew = async (): Promise<void> => {
  await userEvent.click(screen.getByRole("button", { name: "New role" }));
  await userEvent.type(screen.getByLabelText("Name"), "docs-writer");
  await userEvent.type(screen.getByLabelText("Description"), "Writes the docs");
  await userEvent.type(screen.getByLabelText("Prompt template"), "Write the docs.");
};

describe("RoleEditor", () => {
  it("adds a role with its verdicts and prompt", async () => {
    const api = await open();
    await fillNew();
    await userEvent.click(within(dialog()).getByRole("button", { name: "Save" }));
    expect(api.rolesAdd).toHaveBeenCalledWith("docs-writer", {
      description: "Writes the docs",
      model: "",
      pass: ["PASS"],
      fail: ["FAIL"],
      vars: [],
      claudeArgs: [],
      prompt: "Write the docs.",
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(api.rolesList).toHaveBeenCalledTimes(2);
  });

  it("refuses a name that isn't lowercase letters, digits, and hyphens, and saves nothing", async () => {
    const api = await open();
    await fillNew();
    await userEvent.clear(screen.getByLabelText("Name"));
    await userEvent.type(screen.getByLabelText("Name"), "Docs Writer");
    await userEvent.click(within(dialog()).getByRole("button", { name: "Save" }));
    expect(within(dialog()).getByText("Lowercase letters, digits, and hyphens, up to 64, starting with a letter or digit")).toBeInTheDocument();
    expect(api.rolesAdd).not.toHaveBeenCalled();
  });

  it("sends no vars when the vars of a role are cleared", async () => {
    const api = await open();
    await userEvent.click(screen.getByRole("button", { name: "Edit verifier" }));
    await userEvent.clear(await screen.findByLabelText("Vars"));
    await userEvent.click(within(dialog()).getByRole("button", { name: "Save" }));
    expect(api.rolesEdit).toHaveBeenCalledWith("verifier", expect.objectContaining({ vars: [] }));
  });

  it("says that saving a built-in writes a copy", async () => {
    await open();
    await userEvent.click(screen.getByRole("button", { name: "Edit implementer" }));
    expect(await within(dialog()).findByText("Saving writes your own copy; Remove brings the built-in back.")).toBeInTheDocument();
  });

  it("keeps the dialog open and shows ws's message when saving fails", async () => {
    await open({
      rolesAdd: vi.fn(async () => Promise.reject({ code: "role_invalid", message: "invalid role: prompt doesn't render" })),
    });
    await fillNew();
    await userEvent.click(within(dialog()).getByRole("button", { name: "Save" }));
    expect(await within(dialog()).findByText("invalid role: prompt doesn't render")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });
});
