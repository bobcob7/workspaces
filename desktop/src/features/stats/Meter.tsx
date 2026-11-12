import type { JSX } from "react";
import styles from "./StatsPane.module.css";

interface MeterProps {
  readonly label: string;
  /** Fraction used, 0..1; null when unknown. */
  readonly ratio: number | null;
  readonly detail: string;
}

/** A labelled bar showing a 0..1 ratio, empty when the ratio is unknown. */
export const Meter = ({ label, ratio, detail }: Readonly<MeterProps>): JSX.Element => (
  <div
    className={styles.meter}
    role="meter"
    aria-label={label}
    aria-valuemin={0}
    aria-valuemax={100}
    aria-valuenow={ratio === null ? undefined : Math.round(ratio * 100)}
    aria-valuetext={ratio === null ? "unknown" : detail}
  >
    <div className={styles.meterHead}>
      <span>{label}</span>
      <span className={styles.muted}>{detail}</span>
    </div>
    <div className={styles.track}>
      <div className={styles.fill} style={{ width: `${(ratio ?? 0) * 100}%` }} />
    </div>
  </div>
);
