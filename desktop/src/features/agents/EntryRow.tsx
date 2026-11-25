import type { JSX } from "react";
import type { Entry, LoopAttention } from "../../api";
import { age, repoLabel } from "../../format";
import { EnvBadge } from "./EnvBadge";
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

/** The parent's name and, when it's on another environment, where it runs (ADR 0082). */
const parentLabel = (parentName: string | null, parentEnvironment: string | null): string | null => {
  if (parentEnvironment === null) {
    return parentName === null ? null : `↳ ${parentName} · `;
  }
  return parentName === null ? `from ${parentEnvironment} · ` : `↳ ${parentName} on ${parentEnvironment} · `;
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
  /** The environment of the workspace it was started from, when that's another environment (ADR 0082). */
  readonly parentEnvironment?: string | null;
}

/** An agent or workspace as a two-line list row, as the Agents list shows it. */
export const EntryRow = ({ entry, current, onClick, parentName = null, parentEnvironment = null }: EntryRowProps): JSX.Element => {
  const dimmed = !entry.environmentOnline || entry.snapshotStale;
  const label = parentLabel(parentName, parentEnvironment);
  const badgeTitle =
    parentEnvironment === null
      ? undefined
      : `Runs on ${entry.environment}; started from ${parentEnvironment} with ws machine spawn --env`;
  return (
    <button
      type="button"
      className={styles.item}
      aria-current={current ? "true" : undefined}
      title={modelTooltip(entry)}
      data-dimmed={dimmed ? "true" : undefined}
      onClick={onClick}
    >
      <span className={styles.row}>
        <StatusDot status={entry.status} />
        <span className={styles.name}>{entry.name}</span>
        {entry.environment !== "local" && <EnvBadge label={entry.environment} title={badgeTitle} />}
        {!entry.environmentOnline && (
          <EnvBadge label="offline" warn title={`${entry.environment} is offline; the last known status is shown`} />
        )}
        {entry.environmentOnline && entry.snapshotStale && (
          <EnvBadge label="stale" warn title={`${entry.environment} hasn't reported lately; the status may be old`} />
        )}
        <span className={styles.age}>{age(entry.updatedAt ?? entry.createdAt)}</span>
      </span>
      <span className={styles.summaryLine}>
        <span className={styles.summary}>
          {label !== null && <span className={styles.parent}>{label}</span>}
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
};
