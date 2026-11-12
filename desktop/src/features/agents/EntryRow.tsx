import type { JSX } from "react";
import type { Entry, LoopAttention } from "../../api";
import { age, repoLabel } from "../../format";
import { StatusDot } from "./StatusDot";
import styles from "./Sidebar.module.css";

const attentionPrefix: Readonly<Record<LoopAttention, string>> = {
  blocked: "Blocked · ",
  needs_attention: "Needs attention · ",
};

const plainSummary = (entry: Entry): string => {
  if (entry.detail !== null) {
    return entry.detail;
  }
  if (entry.intent !== null) {
    return entry.intent;
  }
  if (entry.repos.length > 0) {
    return entry.repos.map((r) => repoLabel(r.id, r.ref)).join(", ");
  }
  return entry.kind === "agent" && entry.workspaceId === null ? "Not in a workspace" : "Empty workspace";
};

const summary = (entry: Entry): string => {
  const attention = entry.loopRun?.attention;
  return attention ? attentionPrefix[attention] + plainSummary(entry) : plainSummary(entry);
};

export interface EntryRowProps {
  readonly entry: Entry;
  readonly current: boolean;
  readonly onClick: () => void;
  /** The name of the workspace it was started from, if that's listed (ADR 0044). */
  readonly parentName?: string | null;
}

/** An agent or workspace as a two-line list row, as the Agents list shows it. */
export const EntryRow = ({ entry, current, onClick, parentName = null }: EntryRowProps): JSX.Element => (
  <button type="button" className={styles.item} aria-current={current ? "true" : undefined} onClick={onClick}>
    <span className={styles.row}>
      <StatusDot status={entry.status} />
      <span className={styles.name}>{entry.name}</span>
      <span className={styles.age}>{age(entry.updatedAt ?? entry.createdAt)}</span>
    </span>
    <span className={styles.summary}>
      {parentName !== null && <span className={styles.parent}>↳ {parentName} · </span>}
      {summary(entry)}
    </span>
  </button>
);
