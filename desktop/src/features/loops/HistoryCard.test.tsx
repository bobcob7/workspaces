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

  const manyRuns = (count: number): LoopHistory =>
    history({ runs: Array.from({ length: count }, (_, i) => loopHistoryRun({ seq: count - i })), total: count });

  it("asks for the whole history once and shows it ten runs at a time", async () => {
    const api = fakeApi({ loopsHistory: vi.fn(async () => manyRuns(25)) });
    renderWithStore(<HistoryCard loop={loop()} />, testStore({ api }));
    const list = await screen.findByRole("list", { name: "Run history" });
    expect(api.loopsHistory).toHaveBeenCalledWith("deps", 200);
    expect(within(list).getAllByRole("listitem")).toHaveLength(10);
    expect(within(list).getByRole("button", { name: /Run 25\b/ })).toBeInTheDocument();
    expect(screen.getByText("Page 1 of 3 · 25 runs")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled();
  });

  it("moves between pages with Next and Previous", async () => {
    const api = fakeApi({ loopsHistory: vi.fn(async () => manyRuns(25)) });
    renderWithStore(<HistoryCard loop={loop()} />, testStore({ api }));
    await screen.findByRole("list", { name: "Run history" });
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Page 2 of 3 · 25 runs")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Run 15\b/ })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getAllByRole("listitem")).toHaveLength(5);
    expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Previous" }));
    expect(screen.getByText("Page 2 of 3 · 25 runs")).toBeInTheDocument();
  });

  it("shows no pager when everything fits on one page", async () => {
    const api = fakeApi({ loopsHistory: vi.fn(async () => manyRuns(10)) });
    renderWithStore(<HistoryCard loop={loop()} />, testStore({ api }));
    await screen.findByRole("list", { name: "Run history" });
    expect(screen.queryByRole("navigation", { name: "History pages" })).not.toBeInTheDocument();
  });

  it("goes back to the first page when another loop is shown", async () => {
    const api = fakeApi({ loopsHistory: vi.fn(async (name: string) => ({ ...manyRuns(25), loop: name })) });
    const store = testStore({ api });
    const first = renderWithStore(<HistoryCard loop={loop()} />, store);
    await screen.findByRole("list", { name: "Run history" });
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    first.unmount();
    renderWithStore(<HistoryCard loop={loop({ name: "nightly" })} />, store);
    await waitFor(() => expect(api.loopsHistory).toHaveBeenLastCalledWith("nightly", 200));
    await waitFor(() => expect(screen.getByText("Page 1 of 3 · 25 runs")).toBeInTheDocument());
  });

  it("titles a blocked run's report Question", async () => {
    const blocked = loopHistoryRun({
      seq: 58,
      status: "blocked",
      verdict: { verdict: "BLOCKED", outcome: "blocked", recordedAt: "2026-10-24T08:20:00Z", report: null },
    });
    const api = fakeApi({
      loopsHistory: vi.fn(async () => history({ runs: [blocked], total: 1 })),
      loopsHistoryRun: vi.fn(async (name: string, seq: number) => ({
        loop: name,
        run: { ...blocked, seq },
        reportText: "Which major version should I target?",
        warnings: [],
      })),
    });
    renderWithStore(<HistoryCard loop={loop()} />, testStore({ api }));
    await userEvent.click(await screen.findByRole("button", { name: /Run 58/ }));
    expect(await screen.findByRole("heading", { name: "Question" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Report" })).not.toBeInTheDocument();
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
