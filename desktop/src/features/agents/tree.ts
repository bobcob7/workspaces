import type { Entry } from "../../api";
import { byRecency, groupOf, type StatusFilter } from "./status";

/** The Agents list's filters (ADR 0058). */
export interface TreeFilter {
  readonly query: string;
  readonly status: StatusFilter;
  readonly showHidden: boolean;
}

/** No search, every status, hidden entries left out. */
export const noFilter: TreeFilter = { query: "", status: "all", showHidden: false };

/** An entry in the tree, with the children that are shown. */
export interface TreeNode {
  readonly entry: Entry;
  /** 0 for a root. */
  readonly depth: number;
  /** False for a context row: shown only as an ancestor of a match or of the selected entry. */
  readonly match: boolean;
  readonly children: readonly TreeNode[];
}

/** The listed entry that entry was started from, or undefined. */
export const parentOf = (entries: readonly Entry[], entry: Entry): Entry | undefined => {
  if (entry.parentId === null) {
    return undefined;
  }
  return entries.find((e) => e.workspaceId === entry.parentId && e.key !== entry.key);
};

/** Keys of the ancestors of the entry with key, nearest first; [] for a root or an unknown key. Stops before repeating a key, so a cycle ends. */
export const ancestorKeys = (entries: readonly Entry[], key: string): readonly string[] => {
  const start = entries.find((e) => e.key === key);
  if (start === undefined) {
    return [];
  }
  const keys: string[] = [];
  let current = parentOf(entries, start);
  while (current !== undefined && current.key !== key && !keys.includes(current.key)) {
    keys.push(current.key);
    current = parentOf(entries, current);
  }
  return keys;
};

/** True when a search or a status filter is set; Show hidden alone isn't filtering. */
export const isFiltering = (filter: TreeFilter): boolean => filter.query.trim() !== "" || filter.status !== "all";

const searchableText = (entry: Entry): readonly string[] =>
  [entry.name, entry.workspaceId, entry.intent, entry.detail, ...entry.repos.map((repo) => repo.id)].filter(
    (text): text is string => text !== null,
  );

const matchesQuery = (entry: Entry, query: string): boolean => {
  const needle = query.trim().toLowerCase();
  return needle === "" || searchableText(entry).some((text) => text.toLowerCase().includes(needle));
};

/** Whether entry passes every filter. */
export const matchesFilter = (entry: Entry, filter: TreeFilter): boolean =>
  (filter.showHidden || !entry.hidden) &&
  (filter.status === "all" || groupOf(entry) === filter.status) &&
  matchesQuery(entry, filter.query);

const shownKeys = (entries: readonly Entry[], filter: TreeFilter, selectedKey: string | null): ReadonlySet<string> => {
  const keys = new Set<string>();
  const addWithAncestors = (key: string): void => {
    keys.add(key);
    for (const ancestor of ancestorKeys(entries, key)) {
      keys.add(ancestor);
    }
  };
  for (const entry of entries) {
    if (matchesFilter(entry, filter)) {
      addWithAncestors(entry.key);
    }
  }
  if (selectedKey !== null) {
    addWithAncestors(selectedKey);
  }
  return keys;
};

/** The roots, most recent first, each with its shown descendants. */
export const buildForest = (
  entries: readonly Entry[],
  filter: TreeFilter,
  selectedKey: string | null,
): readonly TreeNode[] => {
  const shown = shownKeys(entries, filter, selectedKey);
  const shownEntries = entries.filter((e) => shown.has(e.key));
  const childrenByKey = new Map<string, Entry[]>();
  for (const entry of shownEntries) {
    const parent = parentOf(entries, entry);
    if (parent !== undefined) {
      childrenByKey.set(parent.key, [...(childrenByKey.get(parent.key) ?? []), entry]);
    }
  }
  const seen = new Set<string>();
  const buildNode = (entry: Entry, depth: number): TreeNode => {
    seen.add(entry.key);
    const children = (childrenByKey.get(entry.key) ?? [])
      .toSorted(byRecency)
      .filter((child) => !seen.has(child.key))
      .map((child) => buildNode(child, depth + 1));
    return { entry, depth, match: matchesFilter(entry, filter), children };
  };

  const roots = shownEntries.filter((e) => parentOf(entries, e) === undefined).toSorted(byRecency);
  const forest = roots.map((root) => buildNode(root, 0));
  for (const entry of shownEntries.toSorted(byRecency)) {
    if (!seen.has(entry.key)) {
      forest.push(buildNode(entry, 0));
    }
  }
  return forest;
};

/** Keys of nodes opened without the user asking: ancestors of the selected entry, and while filtering, ancestors of every match. */
export const autoExpandedKeys = (
  entries: readonly Entry[],
  filter: TreeFilter,
  selectedKey: string | null,
): ReadonlySet<string> => {
  const keys = new Set<string>(selectedKey === null ? [] : ancestorKeys(entries, selectedKey));
  if (!isFiltering(filter)) {
    return keys;
  }
  for (const entry of entries) {
    if (matchesFilter(entry, filter)) {
      for (const ancestor of ancestorKeys(entries, entry.key)) {
        keys.add(ancestor);
      }
    }
  }
  return keys;
};

/** Whether a node is open: the user's last choice if there is one, else whether it's auto-expanded. */
export const isOpen = (
  key: string,
  expanded: Readonly<Record<string, boolean>>,
  auto: ReadonlySet<string>,
): boolean => expanded[key] ?? auto.has(key);
