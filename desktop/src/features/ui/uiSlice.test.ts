import { describe, expect, it, vi } from "vitest";
import { creatingStarted, loopSelected } from "../loops/loopsSlice";
import { repoSelected } from "../repos/reposSlice";
import { reviewSelected } from "../reviews/reviewsSlice";
import { groupChosen } from "../settings/settingsSlice";
import { windowChosen } from "../stats/statsSlice";
import {
  agentFocused,
  dialogClosed,
  dialogOpened,
  narrowBack,
  readSidebarCollapsed,
  sectionChosen,
  selected,
  sidebarToggled,
  uiReducer,
  writeSidebarCollapsed,
  type UiState,
} from "./uiSlice";

const initial = (): UiState => uiReducer(undefined, { type: "init" });

describe("uiReducer", () => {
  it("starts on Agents", () => {
    expect(initial().section).toBe("agents");
  });

  it("chooses a section", () => {
    expect(uiReducer(initial(), sectionChosen("repos")).section).toBe("repos");
  });

  it("chooses the Stats section", () => {
    expect(uiReducer(initial(), sectionChosen("stats")).section).toBe("stats");
  });

  it("keeps the selected agent when the section changes", () => {
    const state = uiReducer({ ...initial(), selectedKey: "fix-login" }, sectionChosen("repos"));
    expect(state.selectedKey).toBe("fix-login");
  });

  it("shows the tray's agent on Agents", () => {
    const state = uiReducer({ ...initial(), section: "repos" }, agentFocused("fix-login"));
    expect(state).toMatchObject({ section: "agents", selectedKey: "fix-login" });
  });

  it("opens a dialog without specs by default", () => {
    expect(uiReducer(initial(), dialogOpened("agent"))).toMatchObject({ dialog: "agent", dialogSpecs: [] });
  });

  it("opens a dialog with specs to start from", () => {
    const state = uiReducer(initial(), dialogOpened("shell", ["github.com/o/n@main"]));
    expect(state.dialogSpecs).toEqual(["github.com/o/n@main"]);
  });

  it("forgets the specs when the dialog closes", () => {
    const open = uiReducer(initial(), dialogOpened("agent", ["github.com/o/n"]));
    expect(uiReducer(open, dialogClosed()).dialogSpecs).toEqual([]);
  });

  it("shows the list pane initially", () => {
    expect(initial().narrowPane).toBe("list");
  });

  it("shows the detail pane when an agent is selected", () => {
    expect(uiReducer(initial(), selected("k")).narrowPane).toBe("detail");
  });

  it("shows the list pane when the selection is cleared", () => {
    const state = uiReducer({ ...initial(), narrowPane: "detail" }, selected(null));
    expect(state.narrowPane).toBe("list");
  });

  it("shows the list pane when a section is chosen", () => {
    const state = uiReducer({ ...initial(), narrowPane: "detail" }, sectionChosen("repos"));
    expect(state.narrowPane).toBe("list");
  });

  it("returns to the list pane on back", () => {
    expect(uiReducer({ ...initial(), narrowPane: "detail" }, narrowBack()).narrowPane).toBe("list");
  });

  it.each([
    ["repoSelected", repoSelected("r"), "repos/repoSelected", "detail"],
    ["loopSelected", loopSelected("l"), "loops/loopSelected", "detail"],
    ["reviewSelected", reviewSelected("p"), "reviews/reviewSelected", "detail"],
    ["creatingStarted", creatingStarted(), "loops/creatingStarted", "detail"],
    ["groupChosen", groupChosen("reviews"), "settings/groupChosen", "detail"],
    ["windowChosen", windowChosen("7d"), "stats/windowChosen", "detail"],
  ])("shows the detail pane on %s", (_name, action, type, pane) => {
    expect(action.type).toBe(type);
    expect(uiReducer(initial(), action).narrowPane).toBe(pane);
  });

  it.each([
    ["repoSelected", repoSelected(null), "repos/repoSelected"],
    ["loopSelected", loopSelected(null), "loops/loopSelected"],
    ["reviewSelected", reviewSelected(null), "reviews/reviewSelected"],
  ])("shows the list pane when %s clears the selection", (_name, action, type) => {
    expect(action.type).toBe(type);
    expect(uiReducer({ ...initial(), narrowPane: "detail" }, action).narrowPane).toBe("list");
  });

  it("shows the sidebar initially", () => {
    expect(initial().sidebarCollapsed).toBe(false);
  });

  it("collapses the sidebar when toggled", () => {
    expect(uiReducer(initial(), sidebarToggled()).sidebarCollapsed).toBe(true);
  });

  it("expands the sidebar when toggled again", () => {
    const collapsed = uiReducer(initial(), sidebarToggled());
    expect(uiReducer(collapsed, sidebarToggled()).sidebarCollapsed).toBe(false);
  });

  it("keeps the sidebar state when the section changes", () => {
    const state = uiReducer({ ...initial(), sidebarCollapsed: true }, sectionChosen("repos"));
    expect(state.sidebarCollapsed).toBe(true);
  });
});

describe("sidebar storage", () => {
  it("reads collapsed when the saved value is true", () => {
    window.localStorage.setItem("ws.sidebarCollapsed", "true");
    expect(readSidebarCollapsed()).toBe(true);
    window.localStorage.removeItem("ws.sidebarCollapsed");
  });

  it("reads expanded when nothing is saved", () => {
    window.localStorage.removeItem("ws.sidebarCollapsed");
    expect(readSidebarCollapsed()).toBe(false);
  });

  it("reads expanded when the saved value is not true", () => {
    window.localStorage.setItem("ws.sidebarCollapsed", "garbage");
    expect(readSidebarCollapsed()).toBe(false);
    window.localStorage.removeItem("ws.sidebarCollapsed");
  });

  it("writes the collapsed state so it survives a reload", () => {
    writeSidebarCollapsed(true);
    expect(window.localStorage.getItem("ws.sidebarCollapsed")).toBe("true");
    writeSidebarCollapsed(false);
    expect(window.localStorage.getItem("ws.sidebarCollapsed")).toBe("false");
  });

  it("reads expanded when storage throws", () => {
    const getItem = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(readSidebarCollapsed()).toBe(false);
    getItem.mockRestore();
  });

  it("does not throw when storage refuses writes", () => {
    const setItem = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(() => writeSidebarCollapsed(true)).not.toThrow();
    setItem.mockRestore();
  });
});
