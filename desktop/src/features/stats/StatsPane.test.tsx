import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { fakeApi, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { StatsList } from "./StatsList";
import { StatsPane } from "./StatsPane";

const dayMs = 86_400_000;

describe("StatsPane", () => {
  it("shows the System and Usage cards", async () => {
    renderWithStore(<StatsPane />, testStore());
    expect(await screen.findByRole("heading", { name: "System" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Usage" })).toBeInTheDocument();
  });

  it("sets the CPU, memory and disk meters from the snapshot", async () => {
    renderWithStore(<StatsPane />, testStore());
    expect(await screen.findByRole("meter", { name: "CPU" })).toHaveAttribute("aria-valuenow", "13");
    expect(screen.getByRole("meter", { name: "Memory" })).toHaveAttribute("aria-valuenow", "63");
    expect(screen.getByRole("meter", { name: "Disk" })).toHaveAttribute("aria-valuenow", "40");
  });

  it("shows the usage totals and agent time", async () => {
    renderWithStore(<StatsPane />, testStore());
    expect(await screen.findByText("2 h 23 min")).toBeInTheDocument();
    expect(screen.getByText("$17.70")).toBeInTheDocument();
  });

  it("asks for the last 7 days when Last 7 days is chosen", async () => {
    const api = fakeApi();
    renderWithStore(
      <>
        <StatsList />
        <StatsPane />
      </>,
      testStore({ api }),
    );
    await screen.findByRole("heading", { name: "System" });
    const before = Date.now();
    await userEvent.click(screen.getByRole("button", { name: "Last 7 days" }));
    await waitFor(() => expect(api.statsGet).toHaveBeenCalledTimes(2));
    const sinceMs = vi.mocked(api.statsGet).mock.calls[1]?.[0];
    expect(sinceMs).toBeGreaterThanOrEqual(before - 7 * dayMs);
    expect(sinceMs).toBeLessThanOrEqual(Date.now() - 7 * dayMs);
  });

  it("shows the error when there is no data", async () => {
    const api = fakeApi({ statsGet: vi.fn(() => Promise.reject(new Error("stats failed"))) });
    renderWithStore(<StatsPane />, testStore({ api }));
    expect(await screen.findByRole("alert")).toHaveTextContent("stats failed");
  });
});
