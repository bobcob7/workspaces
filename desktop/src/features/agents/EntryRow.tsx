import type { JSX } from "react";
import type { Entry, LoopAttention } from "../../api";
import { age, repoLabel } from "../../format";
import { isFailed } from "./progress";
import { modelTooltip, stepLabel } from "./runner";
import { StatusDot } from "./StatusDot";
import { StepProgress } from "./StepProgress";
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

const agentSummary = (entry: Entry): string =>
  (entry.runner === "ws" ? stepLabel(entry.wsRunner) : null) ?? plainSummary(entry);

const summary = (entry: Entry): string => {
  const attention = entry.loopRun?.attention;
  return attention ? attentionPrefix[attention] + agentSummary(entry) : agentSummary(entry);
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
  <button
    type="button"
    className={styles.item}
    aria-current={current ? "true" : undefined}
    title={modelTooltip(entry)}
    onClick={onClick}
  >
    <span className={styles.row}>
      <StatusDot status={entry.status} />
      <span className={styles.name}>{entry.name}</span>
      <span className={styles.age}>{age(entry.updatedAt ?? entry.createdAt)}</span>
    </span>
    <span className={styles.summaryLine}>
      <span className={styles.summary}>
        {parentName !== null && <span className={styles.parent}>↳ {parentName} · </span>}
        {summary(entry)}
      </span>
      {entry.model !== null && <span className={styles.modelLabel}>{entry.model}</span>}
    </span>
    {entry.rollup !== null && (
      <StepProgress
        label={`${entry.name} progress`}
        done={entry.rollup.done}
        total={entry.rollup.total}
        failed={isFailed(entry)}
      />
    )}
  </button>
);
