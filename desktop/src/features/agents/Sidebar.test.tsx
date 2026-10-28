import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { entry, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { selected } from "../ui/uiSlice";
import { fetchEntries } from "./agentsSlice";
import { Sidebar } from "./Sidebar";

const loadedStore = (entries: Parameters<typeof entry>[0][]): ReturnType<typeof testStore> => {
  const store = testStore();
  store.dispatch(fetchEntries.fulfilled({ entries: entries.map(entry), warnings: [] }, "req"));
  return store;
};

describe("Sidebar", () => {
  it("lists an agent that needs input under Needs input", () => {
    const store = loadedStore([{ key: "a", name: "agent-a", status: "needsInput" }]);
    renderWithStore(<Sidebar />, store);
    const group = screen.getByRole("region", { name: "Needs input" });
    expect(within(group).getByText("agent-a")).toBeInTheDocument();
  });

  it("shows what the agent is doing", () => {
    const store = loadedStore([{ key: "a", detail: "running tests" }]);
    renderWithStore(<Sidebar />, store);
    expect(screen.getByText("running tests")).toBeInTheDocument();
  });

  it("selects an entry when clicked", async () => {
    const store = loadedStore([{ key: "a", name: "agent-a" }]);
    renderWithStore(<Sidebar />, store);
    await userEvent.click(screen.getByRole("button", { name: /agent-a/ }));
    expect(store.getState().ui.selectedKey).toBe("a");
  });

  it("says so when there are no workspaces", () => {
    renderWithStore(<Sidebar />, loadedStore([]));
    expect(screen.getByText("No workspaces yet.")).toBeInTheDocument();
  });

  it("keeps hidden entries in a collapsed Hidden group", async () => {
    const store = loadedStore([
      { key: "a", name: "agent-a" },
      { key: "r", name: "review-41", hidden: true },
    ]);
    renderWithStore(<Sidebar />, store);
    const group = screen.getByRole("region", { name: "Hidden" });
    const toggle = within(group).getByRole("button", { name: /Hidden/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("review-41")).not.toBeInTheDocument();
    await userEvent.click(toggle);
    expect(store.getState().agents.showHidden).toBe(true);
    expect(within(group).getByText("review-41")).toBeInTheDocument();
  });

  it("opens the Hidden group while a hidden entry is selected", () => {
    const store = loadedStore([{ key: "r", name: "review-41", hidden: true }]);
    store.dispatch(selected("r"));
    renderWithStore(<Sidebar />, store);
    expect(screen.getByRole("button", { name: /Hidden/ })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("review-41")).toBeInTheDocument();
  });

  it("has no Hidden group without hidden entries", () => {
    renderWithStore(<Sidebar />, loadedStore([{ key: "a" }]));
    expect(screen.queryByRole("region", { name: "Hidden" })).not.toBeInTheDocument();
  });

  it("isn't empty when every workspace is hidden", () => {
    renderWithStore(<Sidebar />, loadedStore([{ key: "r", hidden: true }]));
    expect(screen.queryByText("No workspaces yet.")).not.toBeInTheDocument();
  });
});
