import type { RemoteEnvironment } from "../../api";
import { useAppSelector } from "../../store";
import { offlineReason } from "../environments/environments";

/** The reason a remote environment can't be acted on now; empty for `local`, an online one, or one not listed. */
export const environmentBlock = (items: readonly RemoteEnvironment[], environment: string, now: number = Date.now()): string => {
  if (environment === "local") {
    return "";
  }
  const env = items.find((item) => item.name === environment);
  return env === undefined ? "" : offlineReason(env, now);
};

/** `environmentBlock` for the store's environments. */
export const useEnvironmentBlock = (environment: string): string => {
  const items = useAppSelector((s) => s.environments.items);
  return environmentBlock(items, environment);
};

/** Whether the environment's `claude` is known to be missing. */
export const useClaudeMissing = (environment: string): boolean =>
  useAppSelector(
    (s) => environment !== "local" && s.environments.items.find((item) => item.name === environment)?.hello?.claude === "missing",
  );

/** Permission modes a review may use: a remote review never gets `bypassPermissions`. */
export const reviewPermissionModes = (modes: readonly string[], environment: string): readonly string[] =>
  environment === "local" ? modes : modes.filter((mode) => mode !== "bypassPermissions");
