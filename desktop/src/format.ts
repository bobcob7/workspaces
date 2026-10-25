const units: readonly [Intl.RelativeTimeFormatUnit, number][] = [
  ["day", 86_400],
  ["hour", 3_600],
  ["minute", 60],
];

/** "5m ago" style age of an ISO timestamp, or "" when there is none. */
export const age = (iso: string | null, now: number = Date.now()): string => {
  if (iso === null) {
    return "";
  }
  const then = Date.parse(iso);
  if (Number.isNaN(then)) {
    return "";
  }
  const seconds = Math.max(0, Math.round((now - then) / 1000));
  for (const [unit, size] of units) {
    if (seconds >= size) {
      return `${Math.floor(seconds / size)}${unit[0] ?? ""} ago`;
    }
  }
  return "just now";
};

/** `name@ref`, with a full commit hash shortened to 7 characters. */
export const repoLabel = (id: string, ref: string | null): string => {
  const name = id.split("/").at(-1) ?? id;
  if (ref === null) {
    return name;
  }
  return `${name}@${/^[0-9a-f]{40}$/.test(ref) ? ref.slice(0, 7) : ref}`;
};

const sizeUnits: readonly string[] = ["KB", "MB", "GB", "TB"];

/** "18.4 MB" style size in decimal units, as Finder shows them. */
export const formatSize = (bytes: number): string => {
  if (bytes < 1000) {
    return `${bytes} B`;
  }
  let value = bytes;
  let unit = "B";
  for (const next of sizeUnits) {
    if (value < 1000) {
      break;
    }
    value /= 1000;
    unit = next;
  }
  return `${value < 100 ? value.toFixed(1).replace(/\.0$/, "") : Math.round(value)} ${unit}`;
};

/** n with its noun, pluralized with an s: "1 branch", "14 branches". */
export const count = (n: number, noun: string, plural = `${noun}s`): string => `${n} ${n === 1 ? noun : plural}`;
