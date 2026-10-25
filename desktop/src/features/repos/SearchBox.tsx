import type { JSX } from "react";
import styles from "./SearchBox.module.css";

export interface SearchBoxProps {
  readonly label: string;
  readonly value: string;
  readonly onChange: (value: string) => void;
  readonly compact?: boolean;
}

/** A filter box with a search icon, as at the top of a section's list. */
export const SearchBox = ({ label, value, onChange, compact = false }: SearchBoxProps): JSX.Element => (
  <label className={compact ? `${styles.root} ${styles.compact}` : styles.root}>
    {!compact && (
      <svg className={styles.icon} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
        <circle cx="11" cy="11" r="7" />
        <path d="m20 20-3.5-3.5" />
      </svg>
    )}
    <input
      type="search"
      className={styles.input}
      placeholder={label}
      aria-label={label}
      value={value}
      spellCheck={false}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Escape" && value !== "") {
          e.stopPropagation();
          onChange("");
        }
      }}
    />
  </label>
);
