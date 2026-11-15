import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { dialogOpened } from "../ui/uiSlice";
import { NewAgentDialog } from "./NewAgentDialog";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(async () => []),
  Channel: class {},
}));

const openDialog = (api = fakeApi()): ReturnType<typeof testStore> => {
  const store = testStore({ api });
  store.dispatch(dialogOpened("agent"));
  renderWithStore(<NewAgentDialog />, store);
  return store;
};

/** Picks the ws runner once the runner's models have loaded and enabled it. */
const chooseWsRunner = async (): Promise<void> => {
  await userEvent.click(screen.getByRole("combobox", { name: "Runner" }));
  await waitFor(() => expect(screen.getByRole("option", { name: "ws runner" })).not.toHaveAttribute("aria-disabled", "true"));
  await userEvent.click(screen.getByRole("option", { name: "ws runner" }));
};

describe("NewAgentDialog", () => {
  it("starts an agent with the name and task", async () => {
    const api = fakeApi();
    openDialog(api);
    await userEvent.type(screen.getByLabelText("Name"), "fix-login");
    await userEvent.type(screen.getByLabelText("Task"), "Fix the bug");
    await userEvent.click(screen.getByRole("button", { name: "Start agent" }));
    expect(api.createAgent).toHaveBeenCalledWith(
      expect.objectContaining({ name: "fix-login", prompt: "Fix the bug", specs: [] }),
    );
  });

  it("starts a ws runner agent with its model and endpoint, and no permission mode", async () => {
    const api = fakeApi();
    openDialog(api);
    await chooseWsRunner();
    await userEvent.type(screen.getByLabelText("Task"), "Fix the bug");
    await userEvent.click(screen.getByRole("combobox", { name: "Model" }));
    await userEvent.click(screen.getByRole("option", { name: "qwen9b · qwen/qwen3.5-9b" }));
    await userEvent.click(screen.getByRole("button", { name: "Start agent" }));
    expect(api.createAgent).toHaveBeenCalledWith(
      expect.objectContaining({ runner: "ws", model: "qwen9b", endpoint: "", permissionMode: "", prompt: "Fix the bug" }),
    );
  });

  it("requires a task for the ws runner, and starts once one is typed", async () => {
    openDialog();
    await chooseWsRunner();
    const start = screen.getByRole("button", { name: "Start agent" });
    expect(start).toBeDisabled();
    await userEvent.type(screen.getByLabelText("Task"), "Fix the bug");
    expect(start).toBeEnabled();
  });

  it("closes once the agent has started", async () => {
    const store = openDialog();
    await userEvent.click(screen.getByRole("button", { name: "Start agent" }));
    expect(store.getState().ui.dialog).toBeNull();
  });

  it("explains how to trust the workspace root", async () => {
    const api = fakeApi({
      createAgent: vi.fn(async () =>
        Promise.reject({ code: "claude_untrusted", message: "not trusted", details: { directory: "/w" } }),
      ),
    });
    openDialog(api);
    await userEvent.click(screen.getByRole("button", { name: "Start agent" }));
    expect(await screen.findByText(/cd "\/w" && claude/)).toBeInTheDocument();
  });
});
