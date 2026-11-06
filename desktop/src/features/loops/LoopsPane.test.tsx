import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, loop, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { creatingStarted, fetchLoops, loopSelected } from "./loopsSlice";
import { LoopsPane } from "./LoopsPane";

const storeWithLoop = (api = fakeApi()) => {
  const store = testStore({ api });
  store.dispatch(fetchLoops.fulfilled({ loops: [loop()], warnings: [] }, "req"));
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
    await userEvent.type(screen.getByRole("textbox", { name: /Every/ }), "1h");
    await userEvent.type(screen.getByRole("textbox", { name: /Prompt/ }), "Lint the repo.");
    await userEvent.type(screen.getByRole("textbox", { name: /Repos/ }), "buff-bear  api");
    await userEvent.click(screen.getByRole("button", { name: "Add loop" }));
    expect(api.loopsAdd).toHaveBeenCalledWith({
      name: "nightly",
      every: "1h",
      prompt: "Lint the repo.",
      specs: ["buff-bear", "api"],
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
    await userEvent.type(screen.getByRole("textbox", { name: /Every/ }), "2m");
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

  it("asks to select a loop when none is selected", () => {
    renderWithStore(<LoopsPane />, testStore());
    expect(screen.getByText("Select a loop, or add one.")).toBeInTheDocument();
  });
});
