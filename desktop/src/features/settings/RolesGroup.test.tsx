import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Api, ListedRole } from "../../api";
import { fakeApi, listedRole, listedRoles, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { RolesGroup } from "./RolesGroup";

const withReviewerEdited = (): readonly ListedRole[] =>
  listedRoles().map((role) => (role.name === "reviewer" ? listedRole({ ...role, source: "override", model: "opus" }) : role));

const open = async (overrides: Partial<Api> = {}): Promise<Api> => {
  const api = fakeApi(overrides);
  renderWithStore(<RolesGroup />, testStore({ api }));
  await screen.findByText("implementer");
  return api;
};

const rowOf = (name: string): HTMLElement => {
  const row = screen.getByText(name).closest("tr");
  if (row === null) {
    throw new Error(`no row for ${name}`);
  }
  return row;
};

describe("RolesGroup", () => {
  it("lists the three built-in roles, with the reviewer edited on opus", async () => {
    await open({ rolesList: vi.fn(async () => withReviewerEdited()) });
    expect(screen.getByRole("table", { name: "Roles" })).toBeInTheDocument();
    expect(within(rowOf("implementer")).queryByText(/custom|edited/)).not.toBeInTheDocument();
    expect(within(rowOf("reviewer")).getByText("edited")).toBeInTheDocument();
    expect(within(rowOf("reviewer")).getByText("opus")).toBeInTheDocument();
    expect(within(rowOf("verifier")).getByText("PASS")).toBeInTheDocument();
  });

  it("offers no Remove for a built-in role", async () => {
    await open();
    expect(screen.queryByRole("button", { name: /^Remove / })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit implementer" })).toBeInTheDocument();
  });

  it("asks before resetting an edited role, then removes its file", async () => {
    const api = await open({ rolesList: vi.fn(async () => withReviewerEdited()) });
    await userEvent.click(screen.getByRole("button", { name: "Remove reviewer" }));
    const dialog = screen.getByRole("dialog", { name: "Reset reviewer to built-in?" });
    expect(dialog).toHaveTextContent("The built-in reviewer comes back");
    expect(api.rolesRemove).not.toHaveBeenCalled();
    await userEvent.click(within(dialog).getByRole("button", { name: "Reset to built-in" }));
    expect(api.rolesRemove).toHaveBeenCalledWith("reviewer");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("removes a custom role for good", async () => {
    const api = await open({
      rolesList: vi.fn(async () => [...listedRoles(), listedRole({ name: "docs-writer", description: "Docs", source: "user" })]),
    });
    await userEvent.click(screen.getByRole("button", { name: "Remove docs-writer" }));
    const dialog = screen.getByRole("dialog", { name: "Remove docs-writer?" });
    expect(dialog).toHaveTextContent("nothing comes back");
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove" }));
    expect(api.rolesRemove).toHaveBeenCalledWith("docs-writer");
  });

  it("keeps the dialog open with ws's message when removing fails", async () => {
    await open({
      rolesList: vi.fn(async () => withReviewerEdited()),
      rolesRemove: vi.fn(async () => Promise.reject({ code: "usage", message: "reviewer is in use" })),
    });
    await userEvent.click(screen.getByRole("button", { name: "Remove reviewer" }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Reset to built-in" }));
    expect(await within(screen.getByRole("dialog")).findByText("reviewer is in use")).toBeInTheDocument();
  });
});
