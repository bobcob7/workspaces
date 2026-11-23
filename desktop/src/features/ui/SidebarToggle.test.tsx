import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { isSidebarShortcut, SidebarToggle } from "./SidebarToggle";

const key = (k: string, mods: Partial<KeyboardEvent> = {}) => ({
  key: k,
  metaKey: true,
  altKey: false,
  shiftKey: false,
  ctrlKey: false,
  ...mods,
});

describe("SidebarToggle", () => {
  it("is expanded by default", () => {
    renderWithStore(<SidebarToggle />, testStore());
    expect(screen.getByRole("button", { name: "Collapse sidebar" })).toHaveAttribute("aria-expanded", "true");
  });

  it("collapses the sidebar when clicked", async () => {
    const store = testStore();
    renderWithStore(<SidebarToggle />, store);
    await userEvent.click(screen.getByRole("button", { name: "Collapse sidebar" }));
    expect(store.getState().ui.sidebarCollapsed).toBe(true);
  });

  it("flips aria-expanded and the label when collapsed", async () => {
    renderWithStore(<SidebarToggle />, testStore());
    await userEvent.click(screen.getByRole("button", { name: "Collapse sidebar" }));
    const button = screen.getByRole("button", { name: "Expand sidebar" });
    expect(button).toHaveAttribute("aria-expanded", "false");
  });

  it("expands the sidebar again when clicked while collapsed", async () => {
    const store = testStore();
    renderWithStore(<SidebarToggle />, store);
    await userEvent.click(screen.getByRole("button", { name: "Collapse sidebar" }));
    await userEvent.click(screen.getByRole("button", { name: "Expand sidebar" }));
    expect(store.getState().ui.sidebarCollapsed).toBe(false);
  });

  it("saves the collapsed state to localStorage", async () => {
    renderWithStore(<SidebarToggle />, testStore());
    await userEvent.click(screen.getByRole("button", { name: "Collapse sidebar" }));
    expect(window.localStorage.getItem("ws.sidebarCollapsed")).toBe("true");
  });

  it("toggles with Ctrl+B outside text inputs", () => {
    const store = testStore();
    renderWithStore(<SidebarToggle />, store);
    fireEvent.keyDown(document.body, { key: "b", ctrlKey: true });
    expect(store.getState().ui.sidebarCollapsed).toBe(true);
  });

  it("ignores Ctrl+B while typing in a text input", () => {
    const store = testStore();
    renderWithStore(
      <>
        <SidebarToggle />
        <input aria-label="Search" />
      </>,
      store,
    );
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Search" }), { key: "b", ctrlKey: true });
    expect(store.getState().ui.sidebarCollapsed).toBe(false);
  });
});

describe("isSidebarShortcut", () => {
  it("matches Cmd+B", () => {
    expect(isSidebarShortcut(key("b"))).toBe(true);
  });

  it("matches Ctrl+B and uppercase B", () => {
    expect(isSidebarShortcut(key("B", { metaKey: false, ctrlKey: true }))).toBe(true);
  });

  it("ignores B with Shift or Alt", () => {
    expect(isSidebarShortcut(key("b", { shiftKey: true }))).toBe(false);
    expect(isSidebarShortcut(key("b", { altKey: true }))).toBe(false);
  });

  it("ignores other keys", () => {
    expect(isSidebarShortcut(key("c"))).toBe(false);
  });

  it("ignores B without a modifier", () => {
    expect(isSidebarShortcut(key("b", { metaKey: false }))).toBe(false);
  });
});
