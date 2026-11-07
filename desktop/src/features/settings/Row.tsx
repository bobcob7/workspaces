import type { JSX, ReactNode } from "react";
import styles from "./SettingsPane.module.css";

export interface RowProps {
  readonly label: string;
  readonly help?: ReactNode;
  readonly errors: readonly (string | undefined)[];
  /** The control goes under the text, full width. */
  readonly wide?: boolean;
  readonly children: ReactNode;
}

/** One setting: its label and help text, with the control and any error beside them. */
export const Row = ({ label, help, errors, wide = false, children }: RowProps): JSX.Element => {
  const shown = errors.filter((e) => e !== undefined);
  return (
    <div className={wide ? styles.wideRow : styles.row} role="group" aria-label={label}>
      <div className={wide ? styles.wideText : styles.text}>
        <span className={styles.label}>{label}</span>
        {help !== undefined && <span className={styles.help}>{help}</span>}
      </div>
      <div className={styles.control}>
        {children}
        {shown.length > 0 && (
          <span className={styles.error} role="alert">
            {shown[0]}
          </span>
        )}
      </div>
    </div>
  );
};
