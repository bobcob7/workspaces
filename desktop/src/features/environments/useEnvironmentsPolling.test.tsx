import { act, renderHook } from "@testing-library/react";
import type { JSX, ReactNode } from "react";
import { Provider } from "react-redux";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fakeApi, testStore } from "../../test/fixtures";
import { useEnvironmentsPolling } from "./useEnvironmentsPolling";

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

const setup = (intervalMs?: number) => {
  const api = fakeApi();
  const store = testStore({ api });
  const wrapper = ({ children }: { children: ReactNode }): JSX.Element => <Provider store={store}>{children}</Provider>;
  const hook = renderHook(({ ms }: { ms?: number }) => useEnvironmentsPolling(ms), { wrapper, initialProps: { ms: intervalMs } });
  return { api, ...hook };
};

describe("useEnvironmentsPolling", () => {
  it("fetches at once", async () => {
    const { api } = setup();
    await act(async () => undefined);
    expect(api.environmentsList).toHaveBeenCalledTimes(1);
  });

  it("fetches every 10 s", async () => {
    const { api } = setup();
    await act(async () => undefined);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(api.environmentsList).toHaveBeenCalledTimes(2);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(api.environmentsList).toHaveBeenCalledTimes(3);
  });

  it("refresh fetches at once", async () => {
    const { api, result } = setup();
    await act(async () => undefined);
    await act(async () => {
      result.current.refresh();
    });
    expect(api.environmentsList).toHaveBeenCalledTimes(2);
  });

  it("stops polling when unmounted", async () => {
    const { api, unmount } = setup();
    await act(async () => undefined);
    unmount();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(api.environmentsList).toHaveBeenCalledTimes(1);
  });

  it("does not start a poll while the previous one is in flight", async () => {
    const api = fakeApi();
    let release: () => void = () => undefined;
    api.environmentsList = vi.fn(
      () =>
        new Promise<{ environments: never[]; warnings: never[] }>((resolve) => {
          release = () => resolve({ environments: [], warnings: [] });
        }),
    );
    const store = testStore({ api });
    const wrapper = ({ children }: { children: ReactNode }): JSX.Element => <Provider store={store}>{children}</Provider>;
    renderHook(() => useEnvironmentsPolling(), { wrapper });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(35_000);
    });
    expect(api.environmentsList).toHaveBeenCalledTimes(1);
    await act(async () => {
      release();
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(api.environmentsList).toHaveBeenCalledTimes(2);
  });

  describe("visibility", () => {
    let hidden = false;
    const setHidden = (value: boolean): void => {
      hidden = value;
      document.dispatchEvent(new Event("visibilitychange"));
    };
    beforeEach(() => {
      hidden = false;
      vi.spyOn(document, "hidden", "get").mockImplementation(() => hidden);
    });

    it("pauses while the document is hidden", async () => {
      const { api } = setup();
      await act(async () => undefined);
      setHidden(true);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000);
      });
      expect(api.environmentsList).toHaveBeenCalledTimes(1);
    });

    it("refreshes at once when it becomes visible and then resumes", async () => {
      const { api } = setup();
      await act(async () => undefined);
      setHidden(true);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000);
      });
      await act(async () => {
        setHidden(false);
      });
      expect(api.environmentsList).toHaveBeenCalledTimes(2);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10_000);
      });
      expect(api.environmentsList).toHaveBeenCalledTimes(3);
    });

    it("stops listening for visibility when unmounted", async () => {
      const { api, unmount } = setup();
      await act(async () => undefined);
      unmount();
      setHidden(false);
      await act(async () => undefined);
      expect(api.environmentsList).toHaveBeenCalledTimes(1);
    });
  });

  it("restarts the timer when the interval changes", async () => {
    const { api, rerender } = setup(10_000);
    await act(async () => undefined);
    rerender({ ms: 1_000 });
    await act(async () => undefined);
    expect(api.environmentsList).toHaveBeenCalledTimes(2);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3_000);
    });
    expect(api.environmentsList).toHaveBeenCalledTimes(5);
  });
});
