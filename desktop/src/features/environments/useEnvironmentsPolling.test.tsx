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
  vi.useRealTimers();
});

const setup = () => {
  const api = fakeApi();
  const store = testStore({ api });
  const wrapper = ({ children }: { children: ReactNode }): JSX.Element => <Provider store={store}>{children}</Provider>;
  const hook = renderHook(() => useEnvironmentsPolling(), { wrapper });
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
});
