import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Entry } from "../../api";
import { entry, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchEntries } from "./agentsSlice";
import { RootProgress } from "./RootProgress";

const rollup = { total: 12, done: 3, fraction: 0.25 };
const root = entry({ key: "root", workspaceId: "root", name: "fix-login", rollup });
const child = (overrides: Partial<Entry>): Entry => entry({ key: "t1", workspaceId: "t1", name: "t1-impl", parentId: "root", ...overrides });

const loadedWith = (entries: readonly Entry[]): ReturnType<typeof testStore> => {
  const store = testStore();
  store.dispatch(fetchEntries.fulfilled({ entries, warnings: [] }, "req"));
  return store;
};

describe("RootProgress", () => {
  it("shows one overall bar with the steps of the workspace and its children", () => {
    renderWithStore(<RootProgress entry={root} />, loadedWith([root]));
    const bar = screen.getByRole("progressbar", { name: "fix-login progress" });
    expect(bar).toHaveAttribute("aria-valuenow", "3");
    expect(bar).toHaveAttribute("aria-valuemax", "12");
    expect(screen.getByText("3/12 steps")).toBeVisible();
  });

  it("shows the note of the most recently updated descendant", () => {
    const newer = child({ key: "t1", progress: { name: "token bucket", total: 3, done: 1, note: "tests", updatedAt: "2026-11-22T21:00:00Z" } });
    const older = child({ key: "t2", workspaceId: "t2", name: "t2-impl", progress: { name: "login", total: 2, done: 0, note: "lint", updatedAt: "2026-11-22T19:00:00Z" } });
    renderWithStore(<RootProgress entry={root} />, loadedWith([root, newer, older]));
    expect(screen.getByText("tests")).toBeVisible();
    expect(screen.queryByText("lint")).not.toBeInTheDocument();
  });

  it("draws a failed workspace's bar in the error colour", () => {
    const failed = { ...root, result: "FAIL" };
    renderWithStore(<RootProgress entry={failed} />, loadedWith([failed]));
    expect(screen.getByRole("progressbar", { name: "fix-login progress" })).toHaveClass("MuiLinearProgress-colorError");
  });

  it("renders nothing for a workspace that started others", () => {
    const nested = entry({ key: "t1", workspaceId: "t1", name: "t1-impl", parentId: "root", rollup });
    const { container } = renderWithStore(<RootProgress entry={nested} />, loadedWith([nested]));
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing without a rollup", () => {
    const { container } = renderWithStore(<RootProgress entry={entry({ key: "solo", workspaceId: "solo", name: "solo" })} />, loadedWith([]));
    expect(container).toBeEmptyDOMElement();
  });
});
