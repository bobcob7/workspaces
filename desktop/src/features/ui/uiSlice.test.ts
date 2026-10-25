import { describe, expect, it } from "vitest";
import { dialogClosed, dialogOpened, sectionChosen, uiReducer, type UiState } from "./uiSlice";

const initial = (): UiState => uiReducer(undefined, { type: "init" });

describe("uiReducer", () => {
  it("starts on Agents", () => {
    expect(initial().section).toBe("agents");
  });

  it("chooses a section", () => {
    expect(uiReducer(initial(), sectionChosen("repos")).section).toBe("repos");
  });

  it("keeps the selected agent when the section changes", () => {
    const state = uiReducer({ ...initial(), selectedKey: "fix-login" }, sectionChosen("repos"));
    expect(state.selectedKey).toBe("fix-login");
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
});
