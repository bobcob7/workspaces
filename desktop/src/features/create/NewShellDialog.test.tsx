import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { fakeHost, remoteEnvironment, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { dialogOpened } from "../ui/uiSlice";
import { NewShellDialog } from "./NewShellDialog";

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(async () => []),
  Channel: class {},
}));

const open = (items = [remoteEnvironment()], lastChoice = "local") => {
  const terminals = fakeHost();
  const store = testStore({
    terminals,
    state: { environments: { items, loadedAt: Date.now(), error: null, latestRequestId: null, warnings: [], lastChoice } },
  });
  store.dispatch(dialogOpened("shell"));
  renderWithStore(<NewShellDialog />, store);
  return { store, terminals };
};

describe("NewShellDialog", () => {
  it("hides the picker without environments", () => {
    open([]);
    expect(screen.queryByLabelText("Environment")).not.toBeInTheDocument();
  });

  it("opens a local shell without environment", async () => {
    const { terminals } = open();
    await userEvent.click(screen.getByRole("button", { name: "Open shell" }));
    await waitFor(() => expect(terminals.open).toHaveBeenCalled());
    expect(vi.mocked(terminals.open).mock.calls[0]?.[1]).toEqual({ kind: "shell", name: "", specs: [] });
  });

  it("opens the shell on the chosen environment and remembers it", async () => {
    const { store, terminals } = open();
    await userEvent.click(screen.getByRole("combobox", { name: "Environment" }));
    await userEvent.click(screen.getByRole("option", { name: "linux-server" }));
    await userEvent.click(screen.getByRole("button", { name: "Open shell" }));
    await waitFor(() => expect(terminals.open).toHaveBeenCalled());
    expect(vi.mocked(terminals.open).mock.calls[0]?.[1]).toEqual({ kind: "shell", name: "", specs: [], environment: "linux-server" });
    expect(store.getState().environments.lastChoice).toBe("linux-server");
  });
});
