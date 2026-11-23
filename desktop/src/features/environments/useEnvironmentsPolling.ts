import { useCallback, useEffect, useRef } from "react";
import { useAppDispatch } from "../../store";
import { fetchEnvironments } from "./environmentsSlice";

/** How often `ws env list` is read. */
export const environmentsPollInterval = 10_000;

/**
 * Fetches the environments at once and then every interval; returns refresh for actions to call.
 * A tick is skipped while the previous fetch is in flight or the document is hidden, and becoming visible refreshes at once.
 */
export const useEnvironmentsPolling = (intervalMs: number = environmentsPollInterval): { readonly refresh: () => void } => {
  const dispatch = useAppDispatch();
  const inFlight = useRef(false);
  const refresh = useCallback((): void => {
    inFlight.current = true;
    void dispatch(fetchEnvironments()).finally(() => {
      inFlight.current = false;
    });
  }, [dispatch]);
  useEffect(() => {
    refresh();
    const timer = window.setInterval(() => {
      if (document.hidden || inFlight.current) {
        return;
      }
      refresh();
    }, intervalMs);
    const onVisibility = (): void => {
      if (!document.hidden) {
        refresh();
      }
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [refresh, intervalMs]);
  return { refresh };
};
