import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { loop, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { LoopForm, type LoopFormProps } from "./LoopForm";

const renderForm = (props: Partial<LoopFormProps> = {}) => {
  const onSubmit = vi.fn();
  renderWithStore(
    <LoopForm error={null} submitting={false} onCancel={vi.fn()} onSubmit={onSubmit} {...props} />,
    testStore(),
  );
  return { onSubmit };
};

const typeRequiredFields = async (): Promise<void> => {
  await userEvent.type(screen.getByRole("textbox", { name: /Name/ }), "deps");
  await userEvent.type(screen.getByRole("textbox", { name: /Prompt/ }), "Update the dependencies.");
};

describe("LoopForm", () => {
  it("adds a loop with a period in minutes, repos, and the default keep", async () => {
    const { onSubmit } = renderForm();
    await typeRequiredFields();
    const every = screen.getByRole("spinbutton", { name: "Every" });
    await userEvent.clear(every);
    await userEvent.type(every, "90");
    await userEvent.click(screen.getByRole("combobox", { name: "Every unit" }));
    await userEvent.click(screen.getByRole("option", { name: "minutes" }));
    await userEvent.type(screen.getByRole("textbox", { name: /Repos/ }), "buff-bear{Enter}api{Enter}");
    await userEvent.click(screen.getByRole("button", { name: "Add loop" }));
    expect(onSubmit).toHaveBeenCalledWith({
      name: "deps",
      every: "1h30m",
      prompt: "Update the dependencies.",
      specs: ["buff-bear", "api"],
      keep: 5,
      model: "",
      permissionMode: "",
    });
  });

  it("edits a loop, keeping its name, script, timeout, and variables", async () => {
    const { onSubmit } = renderForm({
      loop: loop({ every: "48h", script: "true", scriptTimeout: "45s", vars: { A: "1" }, specs: ["buff-bear"] }),
    });
    expect(screen.getByRole("heading", { name: "Edit deps" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: /Name/ })).toBeDisabled();
    expect(screen.getByRole("spinbutton", { name: "Every" })).toHaveValue(2);
    expect(screen.getByRole("combobox", { name: "Every unit" })).toHaveTextContent("days");
    expect(screen.getByRole("textbox", { name: /Script/ })).toHaveValue("true");
    expect(screen.getByRole("spinbutton", { name: /Timeout/ })).toHaveValue(45);
    expect(screen.getByRole("combobox", { name: "Timeout unit" })).toHaveTextContent("seconds");
    expect(screen.getByRole("textbox", { name: "Key 1" })).toHaveValue("A");
    expect(screen.getByRole("button", { name: "Remove buff-bear" })).toBeInTheDocument();
    await userEvent.clear(screen.getByRole("spinbutton", { name: "Keep runs" }));
    await userEvent.type(screen.getByRole("spinbutton", { name: "Keep runs" }), "3");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(onSubmit).toHaveBeenCalledWith({
      name: "deps",
      every: "48h",
      prompt: "Update the dependencies and open a PR.",
      specs: ["buff-bear"],
      keep: 3,
      model: "",
      permissionMode: "",
      script: "true",
      scriptTimeout: "45s",
      vars: [{ key: "A", value: "1" }],
    });
  });

  it("shows the period error and doesn't submit an invalid period", async () => {
    const { onSubmit } = renderForm();
    await typeRequiredFields();
    const every = screen.getByRole("spinbutton", { name: "Every" });
    await userEvent.clear(every);
    await userEvent.type(every, "4");
    await userEvent.click(screen.getByRole("combobox", { name: "Every unit" }));
    await userEvent.click(screen.getByRole("option", { name: "minutes" }));
    expect(screen.getByText("A whole number, 5 minutes to 7 days")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Add loop" }));
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("drops a repo chip when it is removed", async () => {
    const { onSubmit } = renderForm();
    await typeRequiredFields();
    await userEvent.type(screen.getByRole("textbox", { name: /Repos/ }), "buff-bear{Enter}");
    await userEvent.click(screen.getByRole("button", { name: "Remove buff-bear" }));
    await userEvent.click(screen.getByRole("button", { name: "Add loop" }));
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ specs: [] }));
  });

  it("makes the prompt and script multiline textareas", () => {
    renderForm();
    expect(screen.getByRole("textbox", { name: /Prompt/ }).tagName).toBe("TEXTAREA");
    expect(screen.getByRole("textbox", { name: /Script/ }).tagName).toBe("TEXTAREA");
  });

  it("has a group for each section", () => {
    renderForm();
    for (const section of ["Schedule", "Prompt", "Repos", "Agent", "Gate script"]) {
      expect(screen.getByRole("group", { name: section })).toBeInTheDocument();
    }
    expect(screen.getByText("Variables", { selector: "legend" }).closest("fieldset")).toBeInTheDocument();
  });

  it("sets the prompt and script in a monospace font", () => {
    renderForm();
    expect(screen.getByRole("textbox", { name: /Prompt/ })).toHaveClass("mono");
    expect(screen.getByRole("textbox", { name: /Script/ })).toHaveClass("mono");
  });

  it("shows the error and disables submit while submitting", () => {
    renderForm({ error: { code: "loop_invalid", message: "name is taken" }, submitting: true });
    expect(screen.getByRole("alert")).toHaveTextContent("name is taken");
    expect(screen.getByRole("button", { name: "Add loop" })).toBeDisabled();
  });

  it("calls onCancel from Cancel", async () => {
    const onCancel = vi.fn();
    renderWithStore(<LoopForm error={null} submitting={false} onSubmit={vi.fn()} onCancel={onCancel} />, testStore());
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalledOnce();
  });
});
