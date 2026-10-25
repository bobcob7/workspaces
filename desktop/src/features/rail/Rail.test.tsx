import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { entry, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchEntries } from "../agents/agentsSlice";
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
  it("shows only the sections built so far", () => {
    renderWithStore(<Rail />, testStore());
    const labels = screen.getAllByRole("button").map((b) => b.textContent);
    expect(labels).toEqual(["Agents", "Repos"]);
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

  it("ignores a section that isn't shown", () => {
    expect(shortcutSection(key("3"))).toBeNull();
  });

  it("ignores the key without ⌘", () => {
    expect(shortcutSection(key("2", { metaKey: false }))).toBeNull();
  });

  it("ignores ⌘ with another modifier", () => {
    expect(shortcutSection(key("2", { shiftKey: true }))).toBeNull();
  });
});
