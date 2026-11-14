import type { JSX } from "react";
import type { MergeInfo } from "../../api";
import { checksSummary, mergeLabels, mergeTones } from "./groups";
import styles from "./MergeBadge.module.css";

export interface MergeBadgeProps {
  readonly merge: MergeInfo;
  readonly compact?: boolean;
}

const titleOf = (merge: MergeInfo): string => {
  const reasons = merge.reasons.length === 0 ? mergeLabels[merge.status] : merge.reasons.map((reason) => mergeLabels[reason]).join(" · ");
  const checks = checksSummary(merge.checks);
  return checks === null ? reasons : `${reasons} · ${checks}`;
};

/** A pull request's merge status: the label, toned by status, with every reason and the checks in its title. */
export const MergeBadge = ({ merge, compact = false }: MergeBadgeProps): JSX.Element => (
  <span
    className={compact ? `${styles.root} ${styles.compact}` : styles.root}
    data-tone={mergeTones[merge.status]}
    title={titleOf(merge)}
  >
    {mergeLabels[merge.status]}
  </span>
);
