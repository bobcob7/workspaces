import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, type Artifact } from "../../api";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { dialogOpened } from "../ui/uiSlice";
import { NewAgentDialog } from "./NewAgentDialog";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(async () => []),
  Channel: class {},
}));

afterEach(() => {
  vi.restoreAllMocks();
});

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

  it("offers to leave the task empty for Claude Code only", async () => {
    openDialog();
    expect(screen.getByPlaceholderText(/Leave empty to start it waiting for you/)).toBeInTheDocument();
    await chooseWsRunner();
    expect(screen.queryByPlaceholderText(/Leave empty/)).not.toBeInTheDocument();
    expect(screen.getByPlaceholderText("What should the agent do?")).toBeInTheDocument();
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

  it("puts a reference to an attached file in the task", async () => {
    vi.spyOn(api, "artifactAdd").mockResolvedValue(storedArtifact("art-00000009", "shot.png"));
    openDialog();

    await userEvent.upload(pickerInput(), new File(["png"], "shot.png", { type: "image/png" }));

    await waitFor(() => expect(screen.getByLabelText("Task")).toHaveValue("@artifact:art-00000009 "));
  });

  it("keeps Start agent in the dialog, with the fields in the scrolling content", () => {
    openDialog(fakeApi());
    const dialog = screen.getByRole("dialog");
    const content = dialog.querySelector(".content");

    expect(dialog.querySelector("form")).toHaveClass("form");
    expect(content).toHaveClass("content");
    expect(content).toContainElement(screen.getByLabelText("Task"));
    expect(content).not.toContainElement(screen.getByRole("button", { name: "Start agent" }));
  });

  it("holds Start agent while a file uploads", async () => {
    let finish: (value: Artifact) => void = () => undefined;
    vi.spyOn(api, "artifactAdd").mockImplementation(() => new Promise<Artifact>((resolve) => (finish = resolve)));
    openDialog();

    await userEvent.upload(pickerInput(), new File(["png"], "shot.png", { type: "image/png" }));

    expect(screen.getByRole("button", { name: "Start agent" })).toBeDisabled();
    finish(storedArtifact("art-00000009", "shot.png"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Start agent" })).toBeEnabled());
  });
});

const pickerInput = (): HTMLInputElement => {
  const picker = document.querySelector<HTMLInputElement>('input[type="file"]');
  if (picker === null) {
    throw new Error("no file picker");
  }
  return picker;
};

const storedArtifact = (id: string, name: string): Artifact => ({
  id,
  reference: `@artifact:${id}`,
  name,
  title: null,
  kind: "image",
  contentType: "image/png",
  latest: 1,
  size: 3,
  createdAt: "2026-11-23T18:00:00Z",
  updatedAt: "2026-11-23T18:00:00Z",
  createdBy: { kind: "person", workspace: null, name: null },
  threads: { open: 0, resolved: 0, detached: 0 },
  revisions: [],
});
