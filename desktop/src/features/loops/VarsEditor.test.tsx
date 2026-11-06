import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithStore } from "../../test/render";
import { testStore } from "../../test/fixtures";
import { VarsEditor } from "./VarsEditor";

describe("VarsEditor", () => {
  it("adds an empty row", async () => {
    const onChange = vi.fn();
    renderWithStore(<VarsEditor rows={[]} onChange={onChange} />, testStore());
    await userEvent.click(screen.getByRole("button", { name: "Add variable" }));
    expect(onChange).toHaveBeenCalledWith([{ key: "", value: "" }]);
  });

  it("changes a row's key", async () => {
    const onChange = vi.fn();
    renderWithStore(<VarsEditor rows={[{ key: "", value: "" }]} onChange={onChange} />, testStore());
    await userEvent.type(screen.getByRole("textbox", { name: "Key 1" }), "R");
    expect(onChange).toHaveBeenLastCalledWith([{ key: "R", value: "" }]);
  });

  it("removes a row", async () => {
    const onChange = vi.fn();
    const rows = [
      { key: "REPO", value: "buff-bear" },
      { key: "LABEL", value: "bug" },
    ];
    renderWithStore(<VarsEditor rows={rows} onChange={onChange} />, testStore());
    const group = screen.getByRole("group", { name: "Variables" });
    expect(within(group).getByRole("textbox", { name: "Value 2" })).toHaveValue("bug");
    await userEvent.click(screen.getByRole("button", { name: "Remove variable 1" }));
    expect(onChange).toHaveBeenCalledWith([{ key: "LABEL", value: "bug" }]);
  });
});
