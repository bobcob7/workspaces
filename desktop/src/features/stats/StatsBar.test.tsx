import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Stats } from "../../api";
import { entry, fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { fetchEntries } from "../agents/agentsSlice";
import { StatsBar } from "./StatsBar";

const workspaceEntries = () => [
  entry({ key: "a", status: "working" }),
  entry({ key: "b", status: "needsInput" }),
  entry({ key: "c", status: "done" }),
  entry({ key: "d", kind: "shell", workspaceId: null }),
];

const neverResolves = () => vi.fn(() => new Promise<Stats>(() => undefined));

describe("StatsBar", () => {
  it("counts the active workspaces out of all workspaces", () => {
    const store = testStore({ api: fakeApi({ statsGet: neverResolves() }) });
    store.dispatch(fetchEntries.fulfilled({ entries: workspaceEntries(), warnings: [] }, "req"));
    renderWithStore(<StatsBar />, store);
    expect(screen.getByText("2 / 3 workspaces")).toBeInTheDocument();
  });

  it("shows dashes for the numbers before the first stats arrive", () => {
    renderWithStore(<StatsBar />, testStore({ api: fakeApi({ statsGet: neverResolves() }) }));
    expect(screen.getByText("— tokens")).toBeInTheDocument();
    expect(screen.getByText("cache —")).toBeInTheDocument();
    expect(screen.getByText("CPU —")).toBeInTheDocument();
  });

  it("shows tokens, cache hit, host load and cost from today's stats", async () => {
    renderWithStore(<StatsBar />, testStore());
    expect(await screen.findByText("3.4M tokens")).toBeInTheDocument();
    expect(screen.getByText("cache 88%")).toBeInTheDocument();
    expect(screen.getByText("$17.70")).toBeInTheDocument();
    expect(screen.getByText("CPU 13%")).toBeInTheDocument();
    expect(screen.getByText("RAM 63%")).toBeInTheDocument();
    expect(screen.getByText("disk 40%")).toBeInTheDocument();
  });

  it("marks the cost as an estimate at API rates", async () => {
    renderWithStore(<StatsBar />, testStore());
    expect(await screen.findByText("$17.70")).toHaveAttribute("title", "Estimated at API rates");
  });

  it("marks the cost and host load as wide items that hide on a phone", async () => {
    renderWithStore(<StatsBar />, testStore());
    await screen.findByText("$17.70");
    for (const text of ["$17.70", "CPU 13%", "RAM 63%", "disk 40%"]) {
      expect(screen.getByText(text)).toHaveAttribute("data-wide");
    }
  });

  it("keeps the workspace, token and cache items on a phone", async () => {
    const store = testStore();
    store.dispatch(fetchEntries.fulfilled({ entries: workspaceEntries(), warnings: [] }, "req"));
    renderWithStore(<StatsBar />, store);
    await screen.findByText("3.4M tokens");
    expect(screen.getByText("2 / 3 workspaces")).not.toHaveAttribute("data-wide");
    expect(screen.getByText("3.4M tokens")).not.toHaveAttribute("data-wide");
    expect(screen.getByText("cache 88%")).not.toHaveAttribute("data-wide");
  });

  it("opens the Stats section from Open Stats", async () => {
    const store = testStore();
    renderWithStore(<StatsBar />, store);
    await userEvent.click(screen.getByRole("button", { name: "Open Stats" }));
    expect(store.getState().ui.section).toBe("stats");
  });
});
