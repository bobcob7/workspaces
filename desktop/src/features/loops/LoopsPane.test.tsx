import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Loop } from "../../api";
import { fakeApi, loop, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { LoopsList } from "./LoopsList";
import { creatingStarted, fetchLoops, loopSelected } from "./loopsSlice";
import { LoopsPane } from "./LoopsPane";

const storeWithLoop = (api = fakeApi(), overrides: Partial<Loop> = {}) => {
  const store = testStore({ api });
  store.dispatch(fetchLoops.fulfilled({ loops: [loop(overrides)], warnings: [] }, "req"));
  store.dispatch(loopSelected("deps"));
  return store;
};

describe("LoopsPane", () => {
  it("adds a loop from the form and shows it selected", async () => {
    const added = loop({ name: "nightly", every: "1h", prompt: "Lint the repo.", specs: ["buff-bear", "api"] });
    const api = fakeApi({
      loopsAdd: vi.fn(async () => added),
      loopsList: vi.fn(async () => ({ loops: [added], warnings: [] })),
    });
    const store = testStore({ api });
    store.dispatch(fetchLoops.fulfilled({ loops: [], warnings: [] }, "req"));
    store.dispatch(creatingStarted());
    renderWithStore(<LoopsPane />, store);
    await userEvent.type(screen.getByRole("textbox", { name: /Name/ }), "nightly");
    await userEvent.type(screen.getByRole("textbox", { name: /Prompt/ }), "Lint the repo.");
    await userEvent.type(screen.getByRole("combobox", { name: /Repositories/ }), "buff-bear{Enter}api{Enter}");
    await userEvent.click(screen.getByRole("button", { name: "Add loop" }));
    expect(api.loopsAdd).toHaveBeenCalledWith({
      name: "nightly",
      title: "nightly",
      every: "1h",
      prompt: "Lint the repo.",
      specs: ["buff-bear", "api"],
      keep: 5,
      model: "",
      permissionMode: "",
      hidden: true,
    });
    expect(await screen.findByRole("heading", { name: "nightly" })).toBeInTheDocument();
  });

  it("shows the add error in the form", async () => {
    const api = fakeApi({
      loopsAdd: vi.fn(async () => {
        throw { code: "loop_invalid", message: 'period "2m" is under 5m' };
      }),
    });
    const store = testStore({ api });
    store.dispatch(creatingStarted());
    renderWithStore(<LoopsPane />, store);
    await userEvent.type(screen.getByRole("textbox", { name: /Name/ }), "deps");
    const every = screen.getByRole("spinbutton", { name: "Every" });
    await userEvent.clear(every);
    await userEvent.type(every, "5");
    await userEvent.click(screen.getByRole("combobox", { name: "Every unit" }));
    await userEvent.click(screen.getByRole("option", { name: "minutes" }));
    await userEvent.type(screen.getByRole("textbox", { name: /Prompt/ }), "Update.");
    await userEvent.click(screen.getByRole("button", { name: "Add loop" }));
    expect(await screen.findByText('period "2m" is under 5m')).toBeInTheDocument();
  });

  it("pauses an active loop and then offers Resume", async () => {
    const api = fakeApi();
    renderWithStore(<LoopsPane />, storeWithLoop(api));
    await userEvent.click(screen.getByRole("button", { name: "Pause" }));
    expect(api.loopsSetPaused).toHaveBeenCalledWith("deps", true);
    expect(await screen.findByRole("button", { name: "Resume" })).toBeInTheDocument();
  });

  it("runs a loop now", async () => {
    const api = fakeApi();
    renderWithStore(<LoopsPane />, storeWithLoop(api));
    await userEvent.click(screen.getByRole("button", { name: "Run now" }));
    expect(api.loopsRunNow).toHaveBeenCalledWith("deps");
  });

  it("deletes a loop once confirmed", async () => {
    const api = fakeApi();
    renderWithStore(<LoopsPane />, storeWithLoop(api));
    await userEvent.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("Its runs' workspaces stay.")).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(api.loopsRemove).toHaveBeenCalledWith("deps");
  });

  it("adds a loop with a script and variables", async () => {
    const api = fakeApi();
    const store = testStore({ api });
    store.dispatch(creatingStarted());
    renderWithStore(<LoopsPane />, store);
    await userEvent.type(screen.getByRole("textbox", { name: /Name/ }), "issues");
    await userEvent.type(screen.getByRole("textbox", { name: /Prompt/ }), "Triage.");
    await userEvent.type(screen.getByRole("textbox", { name: /Script/ }), "gh issue list | grep .");
    await userEvent.click(screen.getByRole("button", { name: "Add variable" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Key 1" }), "REPO");
    await userEvent.type(screen.getByRole("textbox", { name: "Value 1" }), "buff-bear");
    await userEvent.click(screen.getByRole("button", { name: "Add loop" }));
    expect(api.loopsAdd).toHaveBeenCalledWith({
      name: "issues",
      title: "issues",
      every: "1h",
      prompt: "Triage.",
      specs: [],
      keep: 5,
      model: "",
      permissionMode: "",
      script: "gh issue list | grep .",
      scriptTimeout: "1m",
      vars: [{ key: "REPO", value: "buff-bear" }],
      hidden: true,
    });
  });

  it("says whether a loop's runs are hidden in the Agent card", () => {
    renderWithStore(<LoopsPane />, storeWithLoop(fakeApi(), { hidden: true }));
    expect(within(screen.getByRole("region", { name: "Agent" })).getByText("Hidden unless blocked")).toBeInTheDocument();
  });

  it("says a loop's runs are shown in Agents when they are not hidden", () => {
    renderWithStore(<LoopsPane />, storeWithLoop(fakeApi(), { hidden: false }));
    expect(within(screen.getByRole("region", { name: "Agent" })).getByText("Shown in Agents")).toBeInTheDocument();
  });

  it("shows the script timeout only while there is a script", async () => {
    const store = testStore();
    store.dispatch(creatingStarted());
    renderWithStore(<LoopsPane />, store);
    expect(screen.queryByRole("spinbutton", { name: /Timeout/ })).not.toBeInTheDocument();
    await userEvent.type(screen.getByRole("textbox", { name: /Script/ }), "check");
    expect(screen.getByRole("spinbutton", { name: /Timeout/ })).toBeInTheDocument();
  });

  it("shows a loop's script, timeout, variables, and last check", () => {
    const store = storeWithLoop(fakeApi(), {
      script: "check",
      scriptTimeout: "1m",
      vars: { REPO: "x" },
      lastGate: { at: new Date().toISOString(), outcome: "skipped", exitCode: 1, message: "no new issues" },
    });
    renderWithStore(<LoopsPane />, store);
    const gate = screen.getByRole("region", { name: "Gate script" });
    expect(within(gate).getByLabelText("Gate script", { selector: "pre" })).toHaveTextContent("check");
    expect(within(gate).getByText("1m")).toBeInTheDocument();
    expect(within(gate).getByText(/skipped \(exit 1\): no new issues/)).toBeInTheDocument();
    const variables = screen.getByRole("region", { name: "Variables" });
    expect(within(variables).getByRole("cell", { name: "REPO" })).toBeInTheDocument();
    expect(within(variables).getByRole("cell", { name: "x" })).toBeInTheDocument();
  });

  it("shows no script or last check for a loop without a script", () => {
    renderWithStore(<LoopsPane />, storeWithLoop());
    const gate = screen.getByRole("region", { name: "Gate script" });
    expect(within(gate).getByText("No gate script: every due run starts.")).toBeInTheDocument();
    expect(screen.queryByLabelText("Gate script", { selector: "pre" })).not.toBeInTheDocument();
    expect(within(gate).queryByText("Last check")).not.toBeInTheDocument();
  });

  it("shows each setting in its card in the detail view", () => {
    const store = storeWithLoop(fakeApi(), {
      script: "go list | grep .",
      scriptTimeout: "2m",
      vars: { BRANCH: "main" },
      model: "opus",
    });
    renderWithStore(<LoopsPane />, store);
    for (const name of ["Schedule", "Prompt", "Repos", "Agent", "Gate script", "Variables"]) {
      expect(screen.getByRole("region", { name })).toBeInTheDocument();
    }
    expect(screen.getByLabelText("Prompt", { selector: "pre" })).toHaveTextContent("Update the dependencies and open a PR.");
    expect(screen.getByLabelText("Gate script", { selector: "pre" })).toHaveTextContent("go list | grep .");
    expect(within(screen.getByRole("region", { name: "Gate script" })).getByText("2m")).toBeInTheDocument();
    const variables = screen.getByRole("region", { name: "Variables" });
    expect(within(variables).getByRole("row", { name: /BRANCH/ })).toHaveTextContent("main");
    expect(within(screen.getByRole("region", { name: "Agent" })).getByText("opus")).toBeInTheDocument();
  });

  it("shows that a loop without repos gets an empty workspace", () => {
    renderWithStore(<LoopsPane />, storeWithLoop(fakeApi(), { specs: [] }));
    expect(screen.getByText("No repos: each run gets an empty workspace")).toBeInTheDocument();
  });

  it("edits a loop in the same form and shows the detail again", async () => {
    const api = fakeApi({
      loopsList: vi.fn(async () => ({ loops: [loop({ every: "6h" })], warnings: [] })),
    });
    renderWithStore(<LoopsPane />, storeWithLoop(api));
    await userEvent.click(screen.getByRole("button", { name: "Edit" }));
    const form = screen.getByRole("form", { name: "Edit loop" });
    expect(within(form).getByRole("textbox", { name: /Name/ })).toHaveValue("deps");
    const every = within(form).getByRole("spinbutton", { name: "Every" });
    await userEvent.clear(every);
    await userEvent.type(every, "6");
    await userEvent.click(within(form).getByRole("combobox", { name: "Every unit" }));
    await userEvent.click(screen.getByRole("option", { name: "hours" }));
    await userEvent.click(within(form).getByRole("button", { name: "Save changes" }));
    expect(api.loopsEdit).toHaveBeenCalledWith(expect.objectContaining({ name: "deps", every: "6h" }));
    const schedule = await screen.findByRole("region", { name: "Schedule" });
    expect(within(schedule).getByText("6h")).toBeInTheDocument();
  });

  it("keeps the edit form open and shows an edit error", async () => {
    const api = fakeApi({
      loopsEdit: vi.fn(async () => {
        throw { code: "loop_invalid", message: "prompt: is empty" };
      }),
    });
    renderWithStore(<LoopsPane />, storeWithLoop(api));
    await userEvent.click(screen.getByRole("button", { name: "Edit" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("prompt: is empty")).toBeInTheDocument();
    expect(screen.getByRole("form", { name: "Edit loop" })).toBeInTheDocument();
  });

  it("cancels an edit without saving", async () => {
    const api = fakeApi();
    renderWithStore(<LoopsPane />, storeWithLoop(api));
    await userEvent.click(screen.getByRole("button", { name: "Edit" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(api.loopsEdit).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: "deps" })).toBeInTheDocument();
  });

  it("shows a loop's title in the list, with the ID muted in the detail", async () => {
    const api = fakeApi({
      loopsList: vi.fn(async () => ({
        loops: [loop({ name: "deps-bump", title: "Dependency bumps" }), loop({ name: "triage", title: "" })],
        warnings: [],
      })),
    });
    const store = testStore({ api });
    renderWithStore(
      <>
        <LoopsList />
        <LoopsPane />
      </>,
      store,
    );
    const list = await screen.findByRole("list", { name: "Loops" });
    expect(within(list).getByText("Dependency bumps")).toBeInTheDocument();
    expect(within(list).getByText("triage")).toBeInTheDocument();
    store.dispatch(loopSelected("deps-bump"));
    expect(await screen.findByRole("heading", { name: "Dependency bumps" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Dependency bumps" })).toBeInTheDocument();
    expect(screen.getByText("deps-bump")).toHaveClass("muted");
  });

  it("shows the name in the detail, with no ID line, when a loop has no title", () => {
    renderWithStore(<LoopsPane />, storeWithLoop());
    expect(screen.getByRole("heading", { name: "deps" })).toBeInTheDocument();
    expect(screen.queryByText("deps", { selector: "span" })).not.toBeInTheDocument();
  });

  it("asks to select a loop when none is selected", () => {
    renderWithStore(<LoopsPane />, testStore());
    expect(screen.getByText("Select a loop, or add one.")).toBeInTheDocument();
  });
  it("shows the History and Memory cards in the detail view", () => {
    renderWithStore(<LoopsPane />, storeWithLoop());
    expect(screen.getByRole("region", { name: "History" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Memory" })).toBeInTheDocument();
  });

  it("puts the History card last, after Memory", () => {
    renderWithStore(<LoopsPane />, storeWithLoop());
    const titles = screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent);
    expect(titles.at(-1)).toBe("History");
    expect(titles.indexOf("Memory")).toBeLessThan(titles.indexOf("History"));
  });
});
