import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { entry, testStore, treeEntries } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { selected } from "../ui/uiSlice";
import { fetchEntries } from "./agentsSlice";
import { Sidebar } from "./Sidebar";

const loadedStore = (entries: Parameters<typeof entry>[0][]): ReturnType<typeof testStore> => {
  const store = testStore();
  store.dispatch(fetchEntries.fulfilled({ entries: entries.map(entry), warnings: [] }, "req"));
  return store;
};

const loadedTree = (): ReturnType<typeof testStore> => {
  const store = testStore();
  store.dispatch(fetchEntries.fulfilled({ entries: [...treeEntries()], warnings: [] }, "req"));
  return store;
};

const tree = (): HTMLElement => screen.getByRole("region", { name: "All workspaces" });

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

  it("isn't empty when every workspace is hidden", () => {
    renderWithStore(<Sidebar />, loadedStore([{ key: "r", hidden: true }]));
    expect(screen.queryByText("No workspaces yet.")).not.toBeInTheDocument();
  });

  it("says every workspace is hidden when all are and none are shown", () => {
    renderWithStore(<Sidebar />, loadedStore([{ key: "r", hidden: true }]));
    expect(screen.getByText("Every workspace is hidden.")).toBeInTheDocument();
  });

  it("lists a blocked loop run under Needs input with its state first", () => {
    const store = loadedStore([
      {
        key: "loop-deps-bump-2b3c4d5e",
        name: "deps-bump",
        status: "needsInput",
        hidden: false,
        detail: "Which major version of eslint should I target?",
        loopRun: { loop: "deps-bump", attention: "blocked" },
      },
    ]);
    renderWithStore(<Sidebar />, store);
    const group = screen.getByRole("region", { name: "Needs input" });
    expect(within(group).getByText("deps-bump")).toBeInTheDocument();
    expect(within(group).getByText(/^Blocked · /)).toHaveTextContent("Blocked · Which major version of eslint should I target?");
  });

  it("marks a child in Needs input with the workspace it was started from", () => {
    const store = loadedStore([
      { key: "root", workspaceId: "root", name: "fix-login" },
      { key: "c", workspaceId: "c", name: "login-tests", parentId: "root", status: "needsInput", detail: "Writing tests" },
    ]);
    renderWithStore(<Sidebar />, store);
    const group = screen.getByRole("region", { name: "Needs input" });
    expect(within(group).getByRole("button")).toHaveTextContent("↳ fix-login · Writing tests");
  });

  it("includes a needs-input child in Needs input, with its parent", () => {
    renderWithStore(<Sidebar />, loadedTree());
    const group = screen.getByRole("region", { name: "Needs input" });
    expect(within(group).getByRole("button")).toHaveTextContent(/↳ t1-impl/);
  });

  it("has no Hidden group", () => {
    renderWithStore(<Sidebar />, loadedTree());
    expect(screen.queryByRole("region", { name: "Hidden" })).not.toBeInTheDocument();
  });

  it("shows only root workspaces at first", () => {
    renderWithStore(<Sidebar />, loadedTree());
    expect(within(tree()).getByText("orchestrate")).toBeInTheDocument();
    expect(within(tree()).getByText("solo")).toBeInTheDocument();
    expect(within(tree()).getByText("lint-sweep")).toBeInTheDocument();
    expect(within(tree()).getByText("scratch")).toBeInTheDocument();
    expect(within(tree()).queryByText("t1-impl")).not.toBeInTheDocument();
  });

  it("expands and collapses a node with its disclosure button", async () => {
    const store = loadedTree();
    renderWithStore(<Sidebar />, store);
    await userEvent.click(screen.getByRole("button", { name: "Expand orchestrate" }));
    expect(within(tree()).getByText("t1-impl")).toBeInTheDocument();
    expect(store.getState().agents.expanded.orch).toBe(true);
    const collapse = screen.getByRole("button", { name: "Collapse orchestrate" });
    expect(collapse).toHaveAttribute("aria-expanded", "true");
    await userEvent.click(collapse);
    expect(within(tree()).queryByText("t1-impl")).not.toBeInTheDocument();
  });

  it("keeps a hidden root with a visible child as a dimmed context row", () => {
    renderWithStore(<Sidebar />, loadedTree());
    expect(within(tree()).getByText("lint-sweep").closest("li")).toHaveAttribute("data-context", "true");
  });

  it("shows hidden entries when Show hidden is on", async () => {
    const store = loadedTree();
    renderWithStore(<Sidebar />, store);
    await userEvent.click(screen.getByLabelText(/Show hidden/));
    expect(store.getState().agents.showHidden).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Expand orchestrate" }));
    expect(within(tree()).getByText("review-42").closest("li")).toHaveAttribute("data-hidden", "true");
  });

  it("expands toward matches while searching, without clicking Expand", async () => {
    renderWithStore(<Sidebar />, loadedTree());
    await userEvent.type(screen.getByLabelText("Search"), "t1-review");
    expect(within(tree()).getByText("t1-review")).toBeInTheDocument();
    expect(within(tree()).getByText("orchestrate").closest("li")).toHaveAttribute("data-context", "true");
    expect(within(tree()).queryByText("solo")).not.toBeInTheDocument();
  });

  it("filters the tree by status", async () => {
    renderWithStore(<Sidebar />, loadedTree());
    await userEvent.selectOptions(screen.getByLabelText("Status"), "stopped");
    expect(within(tree()).getByText("solo")).toBeInTheDocument();
    expect(within(tree()).queryByText("orchestrate")).not.toBeInTheDocument();
  });

  it("says nothing matches when a search finds no workspace", async () => {
    renderWithStore(<Sidebar />, loadedTree());
    await userEvent.type(screen.getByLabelText("Search"), "zzz");
    expect(screen.getByText("No workspaces match the filters.")).toBeInTheDocument();
  });

  it("clears the search and shows the tree again on Clear filters", async () => {
    renderWithStore(<Sidebar />, loadedTree());
    const search = screen.getByLabelText("Search");
    await userEvent.type(search, "zzz");
    await userEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(search).toHaveValue("");
    expect(within(tree()).getByText("orchestrate")).toBeInTheDocument();
  });

  it("shows a selected hidden entry in the tree with Show hidden off", () => {
    const store = loadedTree();
    store.dispatch(selected("rev"));
    renderWithStore(<Sidebar />, store);
    expect(within(tree()).getByText("review-42")).toBeInTheDocument();
  });

  it("expands the ancestors of a selected child", () => {
    const store = loadedTree();
    store.dispatch(selected("t1r"));
    renderWithStore(<Sidebar />, store);
    expect(within(tree()).getByText("t1-review").closest("button")).toHaveAttribute("aria-current", "true");
  });
});
