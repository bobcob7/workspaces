import type { Entry, Status } from "../../api";

const dash = "—";

const oneDecimal = (value: number): string => value.toFixed(1).replace(/\.0$/, "");

/** `value` in the first unit that keeps it under `base`, one decimal; a rounding that reaches `base` moves up a unit. */
const scaled = (value: number, base: number, suffixes: readonly string[]): string => {
  let amount = value;
  let index = 0;
  while (index < suffixes.length - 1 && Number(amount.toFixed(1)) >= base) {
    amount /= base;
    index += 1;
  }
  return `${oneDecimal(amount)}${suffixes[index] ?? ""}`;
};

const countSuffixes: readonly string[] = ["", "k", "M", "B"];
const byteSuffixes: readonly string[] = [" B", " KB", " MB", " GB", " TB"];

/** "999", "12.3k", "1.2M", "3.4B". */
export const formatCount = (n: number): string => scaled(n, 1000, countSuffixes);

/** "93%" for a ratio 0..1; "—" when there is none. */
export const formatPercent = (ratio: number | null): string => (ratio === null ? dash : `${Math.round(ratio * 100)}%`);

/** "23%" for a percentage; "—" when there is none. */
export const formatPercentValue = (percent: number | null): string => (percent === null ? dash : `${Math.round(percent)}%`);

/** "$0.00", "<$0.01", "$4.12", "$123". */
export const formatUsd = (usd: number): string => {
  if (usd === 0) {
    return "$0.00";
  }
  if (usd < 0.01) {
    return "<$0.01";
  }
  if (usd < 100) {
    return `$${usd.toFixed(2)}`;
  }
  return `$${Math.round(usd)}`;
};

/** "900 B", "512 MB", "12.3 GB", in 1024-based units. */
export const formatBytes = (bytes: number): string => scaled(bytes, 1024, byteSuffixes);

/** "<1 min", "15 min", "2 h 5 min", "2 h". */
export const formatMinutes = (minutes: number): string => {
  if (minutes < 1) {
    return "<1 min";
  }
  const total = Math.round(minutes);
  if (total < 60) {
    return `${total} min`;
  }
  const remainder = total % 60;
  const hours = `${Math.floor(total / 60)} h`;
  return remainder === 0 ? hours : `${hours} ${remainder} min`;
};

/** A rate with `digits` decimals; "—" when there is none. */
export const formatRate = (value: number | null, digits: number): string => (value === null ? dash : value.toFixed(digits));

/** used / total as a 0..1 ratio; null when either is unknown or the total is 0. */
export const usedRatio = (used: number | null, total: number | null): number | null => {
  if (used === null || total === null || total === 0) {
    return null;
  }
  return used / total;
};

const activeStatuses: ReadonlySet<Status> = new Set(["working", "needsInput", "starting", "running"]);

/** Workspaces (entries with a workspace id) and, of those, the ones working, needing input, starting or running. */
export const workspaceCounts = (entries: readonly Entry[]): { readonly active: number; readonly total: number } => {
  const workspaces = entries.filter((entry) => entry.workspaceId !== null);
  return {
    active: workspaces.filter((entry) => activeStatuses.has(entry.status)).length,
    total: workspaces.length,
  };
};
