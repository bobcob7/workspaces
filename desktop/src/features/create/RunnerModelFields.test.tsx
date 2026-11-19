import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { runnerSettings, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { RunnerModelFields, type RunnerModelFieldsProps } from "./RunnerModelFields";

const claudeValues = { runner: "claude", model: "", endpoint: "", permissionMode: "" } as const;

const renderFields = (props: Partial<RunnerModelFieldsProps> = {}) => {
  const onChange = vi.fn();
  renderWithStore(
    <RunnerModelFields {...claudeValues} settings={runnerSettings()} onChange={onChange} {...props} />,
    testStore(),
  );
  return { onChange };
};

const chooseOption = async (field: string, option: string): Promise<void> => {
  await userEvent.click(screen.getByRole("combobox", { name: field }));
  await userEvent.click(screen.getByRole("option", { name: option }));
};

describe("RunnerModelFields", () => {
  it("shows the Claude model and permission mode, and no endpoint, for Claude Code", () => {
    renderFields();
    expect(screen.getByRole("combobox", { name: "Model" })).toHaveTextContent("Default");
    expect(screen.getByRole("combobox", { name: "Permission mode" })).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Endpoint" })).not.toBeInTheDocument();
  });

  it("shows the ws models with the default first, and an endpoint, and no permission mode, for the ws runner", async () => {
    renderFields({ runner: "ws" });
    expect(screen.queryByRole("combobox", { name: "Permission mode" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("combobox", { name: "Model" }));
    const options = screen.getAllByRole("option").map((option) => option.textContent);
    expect(options).toEqual(["Default (runner.model: qwen9b)", "qwen9b · qwen/qwen3.5-9b", "qwen27b · qwen/qwen3.5-27b"]);
    await userEvent.keyboard("{Escape}");
    expect(screen.getByRole("combobox", { name: "Endpoint" })).toHaveTextContent("The model's endpoint");
  });

  it("clears the model, endpoint and permission mode when the runner is switched", async () => {
    const { onChange } = renderFields({ model: "opus", permissionMode: "acceptEdits" });
    await chooseOption("Runner", "ws runner");
    expect(onChange).toHaveBeenCalledWith({ runner: "ws", model: "", endpoint: "", permissionMode: "" });
  });

  it("does not clear anything when the same runner is chosen again", async () => {
    const { onChange } = renderFields({ model: "opus" });
    await chooseOption("Runner", "Claude Code");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("disables the ws runner and says where to add a model when there are no models", async () => {
    renderFields({ settings: runnerSettings({ models: [] }) });
    expect(screen.getByText("Add a model in Settings › Models")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("combobox", { name: "Runner" }));
    expect(screen.getByRole("option", { name: "ws runner" })).toHaveAttribute("aria-disabled", "true");
  });

  it("reports the endpoint the person picks for the ws runner", async () => {
    const { onChange } = renderFields({ runner: "ws", model: "qwen9b" });
    await chooseOption("Endpoint", "gpu");
    expect(onChange).toHaveBeenCalledWith({ endpoint: "gpu" });
  });

  it("reports the ws model the person picks", async () => {
    const { onChange } = renderFields({ runner: "ws" });
    const list = screen.getByRole("combobox", { name: "Model" });
    await userEvent.click(list);
    await userEvent.click(within(screen.getByRole("listbox")).getByRole("option", { name: "qwen27b · qwen/qwen3.5-27b" }));
    expect(onChange).toHaveBeenCalledWith({ model: "qwen27b" });
  });
});
