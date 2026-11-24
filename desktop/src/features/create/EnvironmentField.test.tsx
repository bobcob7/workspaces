import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { remoteEnvironment, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { clock } from "../environments/environments";
import { lastChoiceSet } from "../environments/environmentsSlice";
import { EnvironmentField, useEnvironmentPick } from "./EnvironmentField";
import type { JSX } from "react";

const seen = "2026-11-23T14:02:00Z";

const Harness = (): JSX.Element => {
  const pick = useEnvironmentPick();
  return (
    <>
      <EnvironmentField pick={pick} />
      <output aria-label="chosen">{pick.environment}</output>
      <output aria-label="claude missing">{String(pick.claudeMissing)}</output>
    </>
  );
};

const storeWith = (items = [remoteEnvironment(), remoteEnvironment({ id: "b", name: "win-rig", status: "offline", lastSeen: seen })]) =>
  testStore({ state: { environments: { items, loadedAt: Date.now(), error: null, latestRequestId: null, warnings: [], lastChoice: "local" } } });

describe("EnvironmentField", () => {
  it("is hidden without environments", () => {
    renderWithStore(<Harness />, testStore());
    expect(screen.queryByLabelText("Environment")).not.toBeInTheDocument();
  });

  it("is hidden when the only environment hasn't enrolled", () => {
    renderWithStore(<Harness />, storeWith([remoteEnvironment({ state: "pending", status: "pending" })]));
    expect(screen.queryByLabelText("Environment")).not.toBeInTheDocument();
  });

  it("disables an offline environment and says since when", async () => {
    renderWithStore(<Harness />, storeWith());
    await userEvent.click(screen.getByRole("combobox", { name: "Environment" }));
    expect(screen.getByRole("option", { name: `win-rig (offline since ${clock(seen, Date.now())})` })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("option", { name: "linux-server" })).not.toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("option", { name: "local" })).not.toHaveAttribute("aria-disabled", "true");
  });

  it("says when the ws daemon of an environment isn't running", async () => {
    renderWithStore(<Harness />, storeWith([remoteEnvironment({ status: "unknown" })]));
    await userEvent.click(screen.getByRole("combobox", { name: "Environment" }));
    expect(screen.getByRole("option", { name: "linux-server (the ws daemon isn't running)" })).toBeInTheDocument();
  });

  it("starts at the last choice when it is enabled", () => {
    const store = storeWith();
    store.dispatch(lastChoiceSet("linux-server"));
    renderWithStore(<Harness />, store);
    expect(screen.getByLabelText("chosen")).toHaveTextContent("linux-server");
    expect(screen.queryByText(/using local/)).not.toBeInTheDocument();
  });

  it("falls back to local with a note when the last choice is offline", () => {
    const store = storeWith();
    store.dispatch(lastChoiceSet("win-rig"));
    renderWithStore(<Harness />, store);
    expect(screen.getByLabelText("chosen")).toHaveTextContent("local");
    expect(screen.getByText("win-rig is offline; using local")).toBeInTheDocument();
  });

  it("follows the user's pick and drops the note", async () => {
    const store = storeWith();
    store.dispatch(lastChoiceSet("win-rig"));
    renderWithStore(<Harness />, store);
    await userEvent.click(screen.getByRole("combobox", { name: "Environment" }));
    await userEvent.click(screen.getByRole("option", { name: "linux-server" }));
    expect(screen.getByLabelText("chosen")).toHaveTextContent("linux-server");
    expect(screen.queryByText(/using local/)).not.toBeInTheDocument();
  });

  it("uses local without a note when the last choice was removed", () => {
    const store = storeWith();
    store.dispatch(lastChoiceSet("gone"));
    renderWithStore(<Harness />, store);
    expect(screen.getByLabelText("chosen")).toHaveTextContent("local");
    expect(screen.queryByText(/using local/)).not.toBeInTheDocument();
  });

  it("flags an environment without claude, and only that one", async () => {
    const hello = { ...remoteEnvironment().hello!, claude: "missing" as const, claudeVersion: null };
    const store = storeWith([remoteEnvironment({ hello })]);
    store.dispatch(lastChoiceSet("linux-server"));
    renderWithStore(<Harness />, store);
    expect(screen.getByLabelText("claude missing")).toHaveTextContent("true");
    await userEvent.click(screen.getByRole("combobox", { name: "Environment" }));
    await userEvent.click(screen.getByRole("option", { name: "local" }));
    expect(screen.getByLabelText("claude missing")).toHaveTextContent("false");
  });
});
