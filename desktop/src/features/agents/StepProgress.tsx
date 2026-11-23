import { LinearProgress } from "@mui/material";
import type { JSX } from "react";
import styles from "./StepProgress.module.css";

export interface StepProgressProps {
  /** The bar's accessible name. */
  readonly label: string;
  readonly done: number;
  readonly total: number;
  /** The text beside the bar; `done/total` when not given. */
  readonly caption?: string;
  readonly failed?: boolean;
}

/** A thin determinate bar of steps, with its count beside it; nothing when `total` isn't a positive number (ADR 0077). */
export const StepProgress = ({ label, done, total, caption, failed = false }: StepProgressProps): JSX.Element | null => {
  if (!Number.isFinite(total) || total <= 0) {
    return null;
  }
  const clamped = Number.isFinite(done) ? Math.min(Math.max(done, 0), total) : 0;
  return (
    <div className={styles.wrap}>
      <LinearProgress
        className={styles.bar}
        variant="determinate"
        value={(clamped / total) * 100}
        color={failed ? "error" : "primary"}
        role="progressbar"
        aria-label={label}
        aria-valuenow={clamped}
        aria-valuemin={0}
        aria-valuemax={total}
      />
      <span className={styles.count}>{caption ?? `${done}/${total}`}</span>
    </div>
  );
};
