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

  it("names a child's bar by its progress name, else by the child's name", () => {
    const named = entry({ key: "a", workspaceId: "a", name: "a-impl", parentId: "root", rollup: { total: 3, done: 1, fraction: 0.3333 }, progress: { name: "token bucket", total: 3, done: 1, note: null, updatedAt: null } });
    const bare = entry({ key: "b", workspaceId: "b", name: "b-impl", parentId: "root", rollup: { total: 1, done: 0, fraction: 0 } });
    renderWithStore(<ChildrenCard entry={root} />, loadedWith([named, bare]));
    expect(screen.getByRole("progressbar", { name: "token bucket progress" })).toHaveAttribute("aria-valuenow", "1");
    expect(screen.getByRole("progressbar", { name: "b-impl progress" })).toHaveAttribute("aria-valuemax", "1");
  });

  it("draws a child without a rollup with no bar", () => {
    renderWithStore(<ChildrenCard entry={root} />, loadedWith(childrenNamed(["a"])));
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("draws a failed child's bar in the error colour and the rest in the primary colour", () => {
    const failed = entry({ key: "f", workspaceId: "f", name: "f-review", parentId: "root", result: "CHANGES_REQUESTED", rollup: { total: 1, done: 0, fraction: 0 } });
    const passing = entry({ key: "p", workspaceId: "p", name: "p-impl", parentId: "root", rollup: { total: 1, done: 1, fraction: 1 } });
    renderWithStore(<ChildrenCard entry={root} />, loadedWith([failed, passing]));
    expect(screen.getByRole("progressbar", { name: "f-review progress" })).toHaveClass("MuiLinearProgress-colorError");
    expect(screen.getByRole("progressbar", { name: "p-impl progress" })).toHaveClass("MuiLinearProgress-colorPrimary");
  });
});
