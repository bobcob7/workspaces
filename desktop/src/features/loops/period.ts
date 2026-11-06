/** A period's unit: minutes, hours or days. */
export type PeriodUnit = "m" | "h" | "d";
/** A script timeout's unit: seconds or minutes. */
export type TimeoutUnit = "s" | "m";

/** A number-plus-unit input's state; amount is the text typed. */
export interface Amount<U extends string> {
  readonly amount: string;
  readonly unit: U;
}

const minimumPeriodSeconds = 5 * 60;
const maximumPeriodSeconds = 7 * 24 * 60 * 60;
const minimumTimeoutSeconds = 1;
const maximumTimeoutSeconds = 10 * 60;

const periodUnitSeconds: Readonly<Record<PeriodUnit, number>> = { m: 60, h: 3600, d: 86400 };
const timeoutUnitSeconds: Readonly<Record<TimeoutUnit, number>> = { s: 1, m: 60 };

const durationPattern = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/;
const wholeNumberPattern = /^\d+$/;

/** Seconds in a Go duration made of h, m, and s parts ("1h30m", "90s", "24h"); null for anything else. */
export const parseGoDuration = (text: string): number | null => {
  const match = durationPattern.exec(text);
  if (text === "" || match === null) {
    return null;
  }
  const [, hours = "0", minutes = "0", seconds = "0"] = match;
  return Number(hours) * 3600 + Number(minutes) * 60 + Number(seconds);
};

/** A whole number of seconds as ws prints it: "1h30m", "24h", "15m", "1m30s", "45s". */
export const formatGoDuration = (seconds: number): string => {
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const rest = seconds % 60;
  const text = [hours > 0 ? `${hours}h` : "", minutes > 0 ? `${minutes}m` : "", rest > 0 ? `${rest}s` : ""].join("");
  return text === "" ? "0s" : text;
};

const largestPeriodUnit = (seconds: number): PeriodUnit => {
  if (seconds % periodUnitSeconds.d === 0) {
    return "d";
  }
  if (seconds % periodUnitSeconds.h === 0) {
    return "h";
  }
  return "m";
};

/** A loop's every as the largest unit that divides it: "48h" → 2 d, "90m" → 90 m, "6h" → 6 h. Unparseable → { amount: "1", unit: "h" }. */
export const periodOf = (every: string): Amount<PeriodUnit> => {
  const seconds = parseGoDuration(every);
  if (seconds === null || seconds % periodUnitSeconds.m !== 0) {
    return { amount: "1", unit: "h" };
  }
  const unit = largestPeriodUnit(seconds);
  return { amount: String(seconds / periodUnitSeconds[unit]), unit };
};

/** The Go duration for a period, or null unless amount is a whole number ≥ 1 and the total is 5 minutes to 7 days. */
export const everyOf = (period: Amount<PeriodUnit>): string | null => {
  const total = wholeTotal(period.amount, periodUnitSeconds[period.unit]);
  if (total === null || total < minimumPeriodSeconds || total > maximumPeriodSeconds) {
    return null;
  }
  return formatGoDuration(total);
};

/** A script timeout like periodOf, in s or m; null or unparseable → { amount: "1", unit: "m" }. */
export const timeoutOf = (timeout: string | null): Amount<TimeoutUnit> => {
  const seconds = timeout === null ? null : parseGoDuration(timeout);
  if (seconds === null) {
    return { amount: "1", unit: "m" };
  }
  if (seconds % timeoutUnitSeconds.m === 0) {
    return { amount: String(seconds / timeoutUnitSeconds.m), unit: "m" };
  }
  return { amount: String(seconds), unit: "s" };
};

/** The Go duration for a timeout, or null unless it is a whole number of 1 second to 10 minutes. */
export const scriptTimeoutOf = (timeout: Amount<TimeoutUnit>): string | null => {
  const total = wholeTotal(timeout.amount, timeoutUnitSeconds[timeout.unit]);
  if (total === null || total < minimumTimeoutSeconds || total > maximumTimeoutSeconds) {
    return null;
  }
  return formatGoDuration(total);
};

/** The seconds for a whole, positive amount in a unit; null for anything else. */
const wholeTotal = (amount: string, unitSeconds: number): number | null => {
  if (!wholeNumberPattern.test(amount)) {
    return null;
  }
  const count = Number(amount);
  return count > 0 ? count * unitSeconds : null;
};

export const periodUnits: readonly { readonly value: PeriodUnit; readonly label: string }[] = [
  { value: "m", label: "minutes" },
  { value: "h", label: "hours" },
  { value: "d", label: "days" },
];

export const timeoutUnits: readonly { readonly value: TimeoutUnit; readonly label: string }[] = [
  { value: "s", label: "seconds" },
  { value: "m", label: "minutes" },
];
