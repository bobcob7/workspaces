import { useEffect } from "react";
import { useAppDispatch, useAppStore } from "../../store";
import { fetchStats, sinceFor, type StatsWindow } from "./statsSlice";

/** How often the bar and the Stats section refresh (ADR 0059). */
export const statsPollInterval = 15_000;

/** Fetches `statsWindow` now and every statsPollInterval while mounted, skipping
 * a tick while the document is hidden or that window is already loading. */
export const useStatsPolling = (statsWindow: StatsWindow): void => {
  const dispatch = useAppDispatch();
  const store = useAppStore();

  useEffect(() => {
    const refresh = (): void => {
      if (store.getState().stats.results[statsWindow].loading) {
        return;
      }
      void dispatch(fetchStats({ window: statsWindow, sinceMs: sinceFor(statsWindow, new Date()) }));
    };
    const tick = (): void => {
      if (!document.hidden) {
        refresh();
      }
    };
    refresh();
    const timer = window.setInterval(tick, statsPollInterval);
    return () => window.clearInterval(timer);
  }, [dispatch, store, statsWindow]);
};
