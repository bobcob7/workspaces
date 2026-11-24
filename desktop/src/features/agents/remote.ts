import type { Entry, RemoteEnvironment } from "../../api";
import { offlineReason } from "../environments/environments";

/** Whether an action may be used, and why not when it can't. */
export interface ActionState {
  readonly enabled: boolean;
  /** Empty when the action is enabled. */
  readonly reason: string;
}

/** The pane's actions on one entry (D3). */
export interface EntryActions {
  readonly attach: ActionState;
  readonly stop: ActionState;
  readonly stopRunner: ActionState;
  readonly rename: ActionState;
  readonly hide: ActionState;
  readonly remove: ActionState;
  readonly openFolder: ActionState;
  readonly conversation: ActionState;
}

const enabled: ActionState = { enabled: true, reason: "" };

const disabled = (reason: string): ActionState => ({ enabled: false, reason });

/** Why a remote entry's environment can't be acted on now; empty while it is connected. */
const unreachableReason = (entry: Entry, env: RemoteEnvironment | undefined, now: number): string => {
  if (entry.environmentOnline) {
    return "";
  }
  const reason = env === undefined ? "" : offlineReason(env, now);
  return reason === "" ? `${entry.environment} is offline` : reason;
};

/** Each action's availability for entry: a local entry has them all; a remote one follows D3 and its environment's state. */
export const entryActions = (entry: Entry, env: RemoteEnvironment | undefined, now: number): EntryActions => {
  if (entry.environment === "local") {
    return {
      attach: enabled,
      stop: enabled,
      stopRunner: enabled,
      rename: enabled,
      hide: enabled,
      remove: enabled,
      openFolder: enabled,
      conversation: enabled,
    };
  }
  const name = entry.environment;
  const unreachable = unreachableReason(entry, env, now);
  const reachable = unreachable === "" ? enabled : disabled(unreachable);
  return {
    attach: reachable,
    stop: reachable,
    stopRunner: disabled(`Stopping a ws runner on ${name} isn't supported yet`),
    rename: reachable,
    hide: reachable,
    remove: reachable,
    openFolder: disabled(`The files are on ${name}`),
    conversation: disabled(`The conversation of an agent on ${name} isn't shown here yet. Use Terminal.`),
  };
};
