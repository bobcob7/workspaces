import type { RemoteEnvironment } from "../../api";
import type { RootState } from "../../store";

/** An option in the Environment picker. */
export interface EnvironmentChoice {
  readonly name: string;
  readonly disabled: boolean;
  /** Why the choice is disabled; absent when it isn't. */
  readonly reason?: string;
}

/** How old the listing may be before it is called stale (ms). */
export const staleAfterMs = 30_000;

const pad = (n: number): string => String(n).padStart(2, "0");

/** The clock time of an instant, with the date when it isn't today. */
export const clock = (iso: string, now: number): string => {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) {
    return iso;
  }
  const time = `${pad(at.getHours())}:${pad(at.getMinutes())}`;
  const today = new Date(now);
  const sameDay = at.getFullYear() === today.getFullYear() && at.getMonth() === today.getMonth() && at.getDate() === today.getDate();
  return sameDay ? time : `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())} ${time}`;
};

/** Why an environment can't be used now: "win-rig is offline (last seen 14:02)", or an `unknown` one's daemon. Empty when it is online. */
export const offlineReason = (env: RemoteEnvironment, now: number = Date.now()): string => {
  if (env.status === "online") {
    return "";
  }
  if (env.status === "unknown") {
    return `${env.name} is offline (the ws daemon isn't running)`;
  }
  if (env.status === "pending") {
    return `${env.name} hasn't enrolled yet`;
  }
  if (env.lastSeen === null) {
    return `${env.name} is offline`;
  }
  return `${env.name} is offline (last seen ${clock(env.lastSeen, now)})`;
};

/** True when the listing is more than 30 s old, or never loaded. */
export const statusIsOld = (loadedAt: number | null, now: number = Date.now()): boolean =>
  loadedAt === null || now - loadedAt > staleAfterMs;

/** `local`, then each enrolled environment; one that isn't online is disabled with its reason. */
export const selectEnvironmentChoices = (state: RootState, now: number = Date.now()): readonly EnvironmentChoice[] => [
  { name: "local", disabled: false },
  ...state.environments.items
    .filter((env) => env.state === "enrolled")
    .map((env): EnvironmentChoice => {
      const online = env.status === "online";
      return online ? { name: env.name, disabled: false } : { name: env.name, disabled: true, reason: offlineReason(env, now) };
    }),
];

/** The enrolled environments, for Settings. */
export const selectEnvironments = (state: RootState): readonly RemoteEnvironment[] => state.environments.items;
