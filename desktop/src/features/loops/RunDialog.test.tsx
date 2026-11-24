import { screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, loopHistoryRun, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { RunDialog } from "./RunDialog";

describe("RunDialog", () => {
  it("does not show another environment's run with the same loop name and seq", () => {
    const api = fakeApi({ loopsHistoryRun: vi.fn(() => new Promise<never>(() => undefined)) });
    const base = testStore().getState().loopData;
    const state = {
      loopData: { ...base, loop: "local/deps", run: { loop: "deps", run: loopHistoryRun({ seq: 5 }), reportText: "LOCAL REPORT", warnings: [] } },
    };
    renderWithStore(<RunDialog name="deps" environment="alpha" seq={5} onClose={() => undefined} />, testStore({ api, state }));
    expect(screen.queryByText("LOCAL REPORT")).not.toBeInTheDocument();
    expect(screen.getByText("Loading…")).toBeInTheDocument();
  });
});
