import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { entry, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchEntries } from "../agents/agentsSlice";
import { fetchReviews } from "../reviews/reviewsSlice";
import { pullRequest, reviewsView } from "../../test/fixtures";
import { Rail, shortcutSection } from "./Rail";

const key = (k: string, mods: Partial<KeyboardEvent> = {}) => ({
  key: k,
  metaKey: true,
  altKey: false,
  shiftKey: false,
  ctrlKey: false,
  ...mods,
});

describe("Rail", () => {
  it("shows every section, Settings last", () => {
    renderWithStore(<Rail />, testStore());
    const labels = screen.getAllByRole("button").map((b) => b.textContent);
    expect(labels).toEqual(["Agents", "Repos", "Reviews", "Settings"]);
  });

  it("marks the current section", () => {
    renderWithStore(<Rail />, testStore());
    expect(screen.getByRole("button", { name: "Agents" })).toHaveAttribute("aria-current", "page");
  });

  it("chooses a section when clicked", async () => {
    const store = testStore();
    renderWithStore(<Rail />, store);
    await userEvent.click(screen.getByRole("button", { name: "Repos" }));
    expect(store.getState().ui.section).toBe("repos");
  });

  it("badges Agents with how many need input", () => {
    const store = testStore();
    const entries = [entry({ key: "a", status: "needsInput" }), entry({ key: "b", status: "working" })];
    store.dispatch(fetchEntries.fulfilled({ entries, warnings: [] }, "req"));
    renderWithStore(<Rail />, store);
    expect(screen.getByRole("button", { name: "Agents, 1" })).toBeInTheDocument();
  });

  it("badges Reviews with the pull requests to review", () => {
    const store = testStore();
    store.dispatch(fetchReviews.fulfilled(reviewsView({ prs: [pullRequest(), pullRequest({ key: "b" })] }), "req"));
    renderWithStore(<Rail />, store);
    expect(screen.getByRole("button", { name: "Reviews, 2" })).toBeInTheDocument();
  });

  it("chooses Settings on ⌘4", () => {
    const store = testStore();
    renderWithStore(<Rail />, store);
    fireEvent.keyDown(window, { key: "4", metaKey: true });
    expect(store.getState().ui.section).toBe("settings");
  });

  it("chooses Repos on ⌘2", () => {
    const store = testStore();
    renderWithStore(<Rail />, store);
    fireEvent.keyDown(window, { key: "2", metaKey: true });
    expect(store.getState().ui.section).toBe("repos");
  });
});

describe("shortcutSection", () => {
  it("maps ⌘1 to Agents", () => {
    expect(shortcutSection(key("1"))).toBe("agents");
  });

  it("maps ⌘3 to Reviews", () => {
    expect(shortcutSection(key("3"))).toBe("reviews");
  });

  it("ignores digits past the last section", () => {
    expect(shortcutSection(key("5"))).toBeNull();
  });

  it("ignores the key without ⌘", () => {
    expect(shortcutSection(key("2", { metaKey: false }))).toBeNull();
  });

  it("ignores ⌘ with another modifier", () => {
    expect(shortcutSection(key("2", { shiftKey: true }))).toBeNull();
  });
});
