import { useCallback, useEffect } from "react";
import { useAppDispatch } from "../../store";
import { fetchEnvironments } from "./environmentsSlice";

/** How often `ws env list` is read. */
export const environmentsPollInterval = 10_000;

/** Fetches the environments at once and then every interval; returns refresh for actions to call. */
export const useEnvironmentsPolling = (intervalMs: number = environmentsPollInterval): { readonly refresh: () => void } => {
  const dispatch = useAppDispatch();
  const refresh = useCallback((): void => {
    void dispatch(fetchEnvironments());
  }, [dispatch]);
  useEffect(() => {
    refresh();
    const timer = window.setInterval(refresh, intervalMs);
    return () => window.clearInterval(timer);
  }, [refresh, intervalMs]);
  return { refresh };
};
