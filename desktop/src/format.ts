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

export const repoLabel = (id: string, ref: string | null): string => {
  const name = id.split("/").at(-1) ?? id;
  return ref === null ? name : `${name}@${ref}`;
};
