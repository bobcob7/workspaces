import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Entry } from "../../api";
import { entry, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchEntries } from "./agentsSlice";
import { ChildrenCard } from "./ChildrenCard";

const root = entry({ key: "root", workspaceId: "root", name: "fix-login", path: "/w/root", status: "working" });

const childrenNamed = (names: readonly string[]): Entry[] =>
  names.map((name) => entry({ key: name, workspaceId: name, name, parentId: "root" }));

const loadedWith = (children: readonly Entry[]): ReturnType<typeof testStore> => {
  const store = testStore();
  store.dispatch(fetchEntries.fulfilled({ entries: [root, ...children], warnings: [] }, "req"));
  return store;
};

const toggleFor = (count: number): HTMLElement =>
  screen.getByRole("button", { name: new RegExp(`${count} · removed with it`) });

describe("ChildrenCard", () => {
  it("starts expanded with three or fewer children", () => {
    renderWithStore(<ChildrenCard entry={root} />, loadedWith(childrenNamed(["a", "b", "c"])));
    expect(toggleFor(3)).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("a")).toBeVisible();
  });

  it("starts collapsed with more than three children and keeps the count visible", () => {
    renderWithStore(<ChildrenCard entry={root} />, loadedWith(childrenNamed(["a", "b", "c", "d"])));
    expect(toggleFor(4)).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByText("4 · removed with it")).toBeVisible();
    expect(screen.getByText("a")).not.toBeVisible();
  });

  it("expands and collapses the list from its heading", async () => {
    renderWithStore(<ChildrenCard entry={root} />, loadedWith(childrenNamed(["a", "b", "c", "d"])));
    const toggle = toggleFor(4);
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("a")).toBeVisible();
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByText("a")).not.toBeVisible();
  });

  it("points the toggle at the list it shows and hides", () => {
    renderWithStore(<ChildrenCard entry={root} />, loadedWith(childrenNamed(["a", "b", "c", "d"])));
    const list = screen.getByRole("list", { hidden: true });
    expect(toggleFor(4)).toHaveAttribute("aria-controls", list.id);
  });

  it("keeps each child's actions inside the expanded list", async () => {
    renderWithStore(<ChildrenCard entry={root} />, loadedWith(childrenNamed(["a", "b", "c", "d"])));
    await userEvent.click(toggleFor(4));
    const list = screen.getByRole("list");
    expect(within(list).getAllByRole("button", { name: "Open" })).toHaveLength(4);
  });

  it("renders nothing without children", () => {
    renderWithStore(<ChildrenCard entry={root} />, loadedWith([]));
    expect(screen.queryByRole("region", { name: "Started from this workspace" })).not.toBeInTheDocument();
  });
});
