import type { Entry, Status } from "../../api";

export type GroupId = "attention" | "working" | "done" | "stopped" | "shells" | "other";

export interface Group {
  readonly id: GroupId;
  readonly title: string;
  readonly entries: readonly Entry[];
}

const groupTitles: Readonly<Record<GroupId, string>> = {
  attention: "Needs input",
  working: "Working",
  done: "Done",
  stopped: "Stopped",
  shells: "Shells",
  other: "Other",
};

const groupOrder: readonly GroupId[] = ["attention", "working", "done", "stopped", "shells", "other"];

export const statusLabels: Readonly<Record<Status, string>> = {
  working: "Working",
  needsInput: "Needs input",
  idle: "Waiting",
  done: "Done",
  starting: "Starting",
  stopped: "Stopped",
  running: "Running",
  pending: "Handing over",
  unknown: "Unknown",
};

/** Statuses whose session has a process, so attaching doesn't resume it. */
const liveStatuses: ReadonlySet<Status> = new Set(["working", "needsInput", "idle", "done", "starting"]);

export const isLive = (status: Status): boolean => liveStatuses.has(status);

/** Hidden entries never count; they're in the tree only with Show hidden (ADR 0057). */
export const needsAttention = (entry: Entry): boolean =>
  !entry.hidden && entry.kind === "agent" && (entry.status === "needsInput" || entry.status === "idle");

export const groupOf = (entry: Entry): GroupId => {
  if (entry.kind === "shell") {
    return "shells";
  }
  if (entry.kind !== "agent") {
    return "other";
  }
  switch (entry.status) {
    case "needsInput":
    case "idle":
      return "attention";
    case "working":
    case "starting":
    case "pending":
      return "working";
    case "done":
      return "done";
    case "stopped":
    case "unknown":
    case "running":
      return "stopped";
  }
};

const recency = (entry: Entry): string => entry.updatedAt ?? entry.createdAt ?? "";

/** Most recently active first, then by name (ADR 0057 orders roots and siblings by it). */
export const byRecency = (a: Entry, b: Entry): number => recency(b).localeCompare(recency(a)) || a.name.localeCompare(b.name);

/** What the Agents list's status filter can be set to: everything, or one group (ADR 0057). */
export type StatusFilter = "all" | GroupId;

/** The status filter's choices in display order: All statuses, then each group. */
export const statusFilterOptions: readonly { readonly value: StatusFilter; readonly label: string }[] = [
  { value: "all", label: "All statuses" },
  ...groupOrder.map((id) => ({ value: id, label: groupTitles[id] })),
];

/** The Needs input group: every entry needsAttention accepts, children included, most recent first. */
export const attentionEntries = (entries: readonly Entry[]): readonly Entry[] =>
  entries.filter(needsAttention).toSorted(byRecency);

/** The workspaces entry started directly, most recently active first (ADR 0044). */
export const childrenOf = (entries: readonly Entry[], entry: Entry): readonly Entry[] =>
  entry.workspaceId === null
    ? []
    : entries.filter((e) => e.parentId === entry.workspaceId && e.key !== entry.key).toSorted(byRecency);

/** Everything entry started, and what that started, each after its parent, with its depth from 1. */
export const descendantsOf = (entries: readonly Entry[], entry: Entry): readonly { readonly entry: Entry; readonly depth: number }[] => {
  const out: { entry: Entry; depth: number }[] = [];
  const seen = new Set<string>([entry.key]);
  const visit = (parent: Entry, depth: number): void => {
    for (const child of childrenOf(entries, parent)) {
      if (seen.has(child.key)) {
        continue;
      }
      seen.add(child.key);
      out.push({ entry: child, depth });
      visit(child, depth + 1);
    }
  };
  visit(entry, 1);
  return out;
};
