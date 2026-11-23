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

/** A thin determinate bar of steps, with its count beside it (ADR 0077). */
export const StepProgress = ({ label, done, total, caption, failed = false }: StepProgressProps): JSX.Element => (
  <div className={styles.wrap}>
    <LinearProgress
      className={styles.bar}
      variant="determinate"
      value={total === 0 ? 0 : (done / total) * 100}
      color={failed ? "error" : "primary"}
      role="progressbar"
      aria-label={label}
      aria-valuenow={done}
      aria-valuemin={0}
      aria-valuemax={total}
    />
    <span className={styles.count}>{caption ?? `${done}/${total}`}</span>
  </div>
);
