import { act } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { JSX } from "react";
import { fakeApi, statsSnapshot, testStore } from "../../test/fixtures";
import { renderWithStore } from "../../test/render";
import { statsPollInterval, useStatsPolling } from "./useStatsPolling";
import type { StatsWindow } from "./statsSlice";

const now = new Date(2026, 10, 12, 15, 30);
const dayMs = 86_400_000;

const Probe = ({ statsWindow }: { readonly statsWindow: StatsWindow }): JSX.Element => {
  useStatsPolling(statsWindow);
  return <p>probe</p>;
};

const setHidden = (hidden: boolean): void => {
  Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden });
};

const advance = (ms: number): Promise<void> =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });

describe("useStatsPolling", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(now);
    setHidden(false);
  });

  afterEach(() => {
    vi.useRealTimers();
    setHidden(false);
  });

  it("fetches the window on mount with its sinceMs", () => {
    const statsGet = vi.fn(async () => statsSnapshot());
    renderWithStore(<Probe statsWindow="7d" />, testStore({ api: fakeApi({ statsGet }) }));
    expect(statsGet).toHaveBeenCalledTimes(1);
    expect(statsGet).toHaveBeenCalledWith(now.getTime() - 7 * dayMs);
  });

  it("fetches again after statsPollInterval", async () => {
    const statsGet = vi.fn(async () => statsSnapshot());
    renderWithStore(<Probe statsWindow="all" />, testStore({ api: fakeApi({ statsGet }) }));
    await advance(0);
    await advance(statsPollInterval);
    expect(statsGet).toHaveBeenCalledTimes(2);
    expect(statsGet).toHaveBeenLastCalledWith(null);
  });

  it("skips a tick while the document is hidden", async () => {
    const statsGet = vi.fn(async () => statsSnapshot());
    setHidden(true);
    renderWithStore(<Probe statsWindow="all" />, testStore({ api: fakeApi({ statsGet }) }));
    await advance(statsPollInterval * 3);
    expect(statsGet).toHaveBeenCalledTimes(1);
  });

  it("skips a tick while that window is still loading", async () => {
    const statsGet = vi.fn(() => new Promise<never>(() => undefined));
    renderWithStore(<Probe statsWindow="30d" />, testStore({ api: fakeApi({ statsGet }) }));
    await advance(statsPollInterval);
    expect(statsGet).toHaveBeenCalledTimes(1);
  });

  it("stops polling after unmount", async () => {
    const statsGet = vi.fn(async () => statsSnapshot());
    const { unmount } = renderWithStore(<Probe statsWindow="all" />, testStore({ api: fakeApi({ statsGet }) }));
    unmount();
    await advance(statsPollInterval * 2);
    expect(statsGet).toHaveBeenCalledTimes(1);
  });
});
