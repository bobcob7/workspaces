import type { ConfigListing, RemoteEnvironment } from "../../api";
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

/** A scope choice in the New agent dialog: an environment the agent may start children on. */
export interface ScopeChoice {
  readonly name: string;
  readonly status: "online" | "offline" | "unknown";
  /** The agent's own environment: checked and disabled. */
  readonly own: boolean;
}

const scopeStatus = (env: RemoteEnvironment): ScopeChoice["status"] => {
  if (env.status === "online" || env.status === "unknown") {
    return env.status;
  }
  return "offline";
};

const byOwnThenLocalThenName = (a: ScopeChoice, b: ScopeChoice): number => {
  if (a.own !== b.own) {
    return a.own ? -1 : 1;
  }
  if (a.name === "local" || b.name === "local") {
    return a.name === "local" ? -1 : 1;
  }
  return a.name.localeCompare(b.name);
};

/** local and each enrolled environment, as scope choices; pending ones are left out. */
export const scopeChoices = (items: readonly RemoteEnvironment[], target: string): readonly ScopeChoice[] => {
  const enrolled = items
    .filter((env) => env.state === "enrolled")
    .map((env): ScopeChoice => ({ name: env.name, status: scopeStatus(env), own: env.name === target }));
  const local: ScopeChoice = { name: "local", status: "online", own: target === "local" };
  return [local, ...enrolled].sort(byOwnThenLocalThenName);
};

/** default_scope's names split into the enrolled ones (or local) and the rest. */
export const splitDefaultScope = (
  defaults: readonly string[],
  items: readonly RemoteEnvironment[],
): { readonly kept: readonly string[]; readonly leftOut: readonly string[] } => {
  const known = new Set(["local", ...items.filter((env) => env.state === "enrolled").map((env) => env.name)]);
  const kept = [...new Set(defaults.filter((name) => known.has(name)))];
  const leftOut = defaults.filter((name) => !known.has(name));
  return { kept, leftOut };
};

const maxScopeNames = 64;

/** environments.default_scope from a ws config list; [] when absent or not a list of strings. */
export const defaultScopeOf = (listing: ConfigListing): readonly string[] => {
  const setting = listing.settings.find((item) => item.key === "environments.default_scope");
  if (!Array.isArray(setting?.value) || !setting.value.every((name): name is string => typeof name === "string")) {
    return [];
  }
  return [...new Set(setting.value)].slice(0, maxScopeNames);
};
