import type { JSX } from "react";
import styles from "./EnvBadge.module.css";

export interface EnvBadgeProps {
  /** The environment's name, or the state word shown beside it. */
  readonly label: string;
  /** Draws the badge as a warning, as for offline and stale. */
  readonly warn?: boolean;
  readonly title?: string;
}

/** A small outlined badge: an environment's name, or its offline or stale state. */
export const EnvBadge = ({ label, warn = false, title }: EnvBadgeProps): JSX.Element => (
  <span className={warn ? `${styles.badge} ${styles.warn}` : styles.badge} title={title}>
    {label}
  </span>
);
