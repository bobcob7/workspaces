import type { Entry, EntryProgress } from "../../api";
import { descendantsOf } from "./status";

/** The verdicts that fail a run; `ws machine result` defaults (roles can add more). */
const failVerdicts: ReadonlySet<string> = new Set(["FAIL", "CHANGES_REQUESTED"]);

/** Whether an entry's result is a failing verdict, which draws its progress bar in the error colour. */
export const isFailed = (entry: Entry): boolean =>
  entry.result !== null && failVerdicts.has(entry.result.trim().toUpperCase());

const mostRecentFirst = (a: EntryProgress, b: EntryProgress): number => (b.updatedAt ?? "").localeCompare(a.updatedAt ?? "");

/** The note of the most recently updated step record among what entry started, or null. */
export const latestNote = (entries: readonly Entry[], entry: Entry): string | null => {
  const latest = descendantsOf(entries, entry)
    .flatMap(({ entry: child }) => (child.progress === null ? [] : [child.progress]))
    .toSorted(mostRecentFirst)[0];
  return latest?.note ?? null;
};
