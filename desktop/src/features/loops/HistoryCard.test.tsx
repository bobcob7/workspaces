import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { LoopHistory } from "../../api";
import { fakeApi, loop, loopHistoryRun, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { HistoryCard } from "./HistoryCard";

const history = (overrides: Partial<LoopHistory> = {}): LoopHistory => ({
  loop: "deps",
  runs: [
    loopHistoryRun({ seq: 56, status: "succeeded", workspace: "loop-deps-56" }),
    loopHistoryRun({ seq: 55, status: "failed", error: { code: "agent_failed", message: "exit 1" }, workspace: "loop-deps-55", workspaceExists: false }),
  ],
  total: 2,
  warnings: [],
  ...overrides,
});

describe("HistoryCard", () => {
  it("lists the runs and says so when there are none", async () => {
    const api = fakeApi({ loopsHistory: vi.fn(async () => history()) });
    renderWithStore(<HistoryCard loop={loop()} />, testStore({ api }));
    const list = await screen.findByRole("list", { name: "Run history" });
    expect(within(list).getByRole("button", { name: /Run 56/ })).toBeInTheDocument();
    expect(within(list).getByRole("button", { name: /Run 55/ })).toBeInTheDocument();
  });

  it("says there are no runs yet when the history is empty", async () => {
    renderWithStore(<HistoryCard loop={loop()} />, testStore());
    expect(await screen.findByText("No runs yet.")).toBeInTheDocument();
  });

  it("opens a run's report when its row is clicked", async () => {
    const api = fakeApi({
      loopsHistory: vi.fn(async () => history()),
      loopsHistoryRun: vi.fn(async (name: string, seq: number) => ({
        loop: name,
        run: loopHistoryRun({ seq, status: "succeeded" }),
        reportText: "Bumped two packages.",
        warnings: [],
      })),
    });
    renderWithStore(<HistoryCard loop={loop()} />, testStore({ api }));
    await userEvent.click(await screen.findByRole("button", { name: /Run 56/ }));
    expect(api.loopsHistoryRun).toHaveBeenCalledWith("deps", 56);
    expect(await screen.findByRole("heading", { name: "Run 56 · Succeeded" })).toBeInTheDocument();
    expect(await screen.findByText("Bumped two packages.")).toBeInTheDocument();
  });

  it("loads the next page of runs with Show more", async () => {
    const api = fakeApi({ loopsHistory: vi.fn(async () => history({ total: 25 })) });
    renderWithStore(<HistoryCard loop={loop()} />, testStore({ api }));
    await userEvent.click(await screen.findByRole("button", { name: "Show more" }));
    expect(api.loopsHistory).toHaveBeenLastCalledWith("deps", 40);
  });

  it("loads the first page for another loop after Show more on this one", async () => {
    const api = fakeApi({ loopsHistory: vi.fn(async () => history({ total: 25 })) });
    const store = testStore({ api });
    const first = renderWithStore(<HistoryCard loop={loop()} />, store);
    await userEvent.click(await screen.findByRole("button", { name: "Show more" }));
    await waitFor(() => expect(store.getState().loopData.historyLimit).toBe(40));
    first.unmount();
    renderWithStore(<HistoryCard loop={loop({ name: "nightly" })} />, store);
    await waitFor(() => expect(api.loopsHistory).toHaveBeenLastCalledWith("nightly", 20));
  });

  it("closes a run's report with Close", async () => {
    const api = fakeApi({
      loopsHistory: vi.fn(async () => history()),
      loopsHistoryRun: vi.fn(async (name: string, seq: number) => ({
        loop: name,
        run: loopHistoryRun({ seq, status: "succeeded" }),
        reportText: null,
        warnings: [],
      })),
    });
    renderWithStore(<HistoryCard loop={loop()} />, testStore({ api }));
    await userEvent.click(await screen.findByRole("button", { name: /Run 56/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("clears the history once confirmed", async () => {
    const api = fakeApi({ loopsHistory: vi.fn(async () => history()) });
    renderWithStore(<HistoryCard loop={loop()} />, testStore({ api }));
    await userEvent.click(await screen.findByRole("button", { name: "Clear history" }));
    await userEvent.click(screen.getByRole("button", { name: "Clear history" }));
    expect(api.loopsHistoryClear).toHaveBeenCalledWith("deps");
  });

  it("selects the workspace of a run in the agents section", async () => {
    const store = testStore({ api: fakeApi({ loopsHistory: vi.fn(async () => history()) }) });
    renderWithStore(<HistoryCard loop={loop()} />, store);
    await userEvent.click(await screen.findByRole("button", { name: "Open loop-deps-56" }));
    expect(store.getState().ui.section).toBe("agents");
  });

  it("shows a removed workspace without a link", async () => {
    renderWithStore(<HistoryCard loop={loop()} />, testStore({ api: fakeApi({ loopsHistory: vi.fn(async () => history()) }) }));
    expect(await screen.findByText("loop-deps-55 (removed)")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Open loop-deps-55" })).not.toBeInTheDocument();
  });

  it("shows the error and retries the fetch", async () => {
    const loopsHistory = vi
      .fn<() => Promise<LoopHistory>>()
      .mockRejectedValueOnce({ code: "internal", message: "history unreadable" })
      .mockResolvedValueOnce(history());
    renderWithStore(<HistoryCard loop={loop()} />, testStore({ api: fakeApi({ loopsHistory }) }));
    expect(await screen.findByRole("alert")).toHaveTextContent("history unreadable");
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("list", { name: "Run history" })).toBeInTheDocument();
  });
});
