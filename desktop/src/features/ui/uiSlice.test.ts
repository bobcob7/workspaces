import { describe, expect, it } from "vitest";
import { creatingStarted, loopSelected } from "../loops/loopsSlice";
import { repoSelected } from "../repos/reposSlice";
import { agentFocused, dialogClosed, dialogOpened, narrowBack, sectionChosen, selected, uiReducer, type UiState } from "./uiSlice";

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

  it("shows the detail pane when a repo or loop is selected", () => {
    expect(uiReducer(initial(), repoSelected("r")).narrowPane).toBe("detail");
    expect(uiReducer(initial(), loopSelected("l")).narrowPane).toBe("detail");
    expect(uiReducer(initial(), creatingStarted()).narrowPane).toBe("detail");
  });
});
