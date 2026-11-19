import { useEffect, useState } from "react";
import type { RunnerSettings } from "../../api";
import { useAppDispatch } from "../../store";
import { fetchRunnerSettings } from "../settings/settingsSlice";

/** The runner's keys for the runner pickers, read once `enabled` is true; null until read or when unreadable. */
export const useRunnerSettings = (enabled: boolean): RunnerSettings | null => {
  const dispatch = useAppDispatch();
  const [settings, setSettings] = useState<RunnerSettings | null>(null);
  useEffect(() => {
    if (!enabled) {
      return;
    }
    let current = true;
    void dispatch(fetchRunnerSettings()).then((result) => {
      if (current && fetchRunnerSettings.fulfilled.match(result)) {
        setSettings(result.payload);
      }
    });
    return () => {
      current = false;
    };
  }, [dispatch, enabled]);
  return settings;
};
