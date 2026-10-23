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

export const needsAttention = (entry: Entry): boolean =>
  entry.kind === "agent" && (entry.status === "needsInput" || entry.status === "idle");

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

/** Groups entries the way agent view does, most recently active first. */
export const groupEntries = (entries: readonly Entry[]): readonly Group[] =>
  groupOrder
    .map((id) => ({
      id,
      title: groupTitles[id],
      entries: entries
        .filter((e) => groupOf(e) === id)
        .toSorted((a, b) => recency(b).localeCompare(recency(a)) || a.name.localeCompare(b.name)),
    }))
    .filter((g) => g.entries.length > 0);
