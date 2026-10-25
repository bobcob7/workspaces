import type { JSX } from "react";
import type { PullRequest } from "../../api";
import { prStatus, type PrStatus } from "./groups";
import styles from "./PrDot.module.css";

export const prStatusLabels: Readonly<Record<PrStatus, string>> = {
  toReview: "To review",
  reviewing: "Reviewing",
  reviewed: "Reviewed",
  outOfDate: "Reviewed",
  dismissed: "Dismissed",
};

export interface PrDotProps {
  readonly pr: PullRequest;
  readonly withLabel?: boolean;
}

/** A pull request's review status: amber to review, blue reviewing, green reviewed (yellow if out of date), grey dismissed. */
export const PrDot = ({ pr, withLabel = false }: PrDotProps): JSX.Element => {
  const status = prStatus(pr);
  return (
    <span className={styles.root} data-status={status} title={prStatusLabels[status]}>
      <span className={styles.dot} />
      {withLabel && <span className={styles.label}>{prStatusLabels[status]}</span>}
    </span>
  );
};
