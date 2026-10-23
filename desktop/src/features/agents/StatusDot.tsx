import type { JSX } from "react";
import type { Status } from "../../api";
import { statusLabels } from "./status";
import styles from "./StatusDot.module.css";

export interface StatusDotProps {
  readonly status: Status;
  readonly withLabel?: boolean;
}

export const StatusDot = ({ status, withLabel = false }: StatusDotProps): JSX.Element => (
  <span className={styles.root} data-status={status} title={statusLabels[status]}>
    <span className={styles.dot} />
    {withLabel && <span className={styles.label}>{statusLabels[status]}</span>}
  </span>
);
