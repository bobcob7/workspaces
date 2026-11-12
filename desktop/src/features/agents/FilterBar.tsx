import { FormControlLabel, Switch, TextField } from "@mui/material";
import type { JSX } from "react";
import { statusFilterOptions, type StatusFilter } from "./status";
import styles from "./FilterBar.module.css";

export interface FilterBarProps {
  readonly query: string;
  readonly status: StatusFilter;
  readonly showHidden: boolean;
  /** How many entries are hidden, shown in the switch's label. */
  readonly hiddenCount: number;
  readonly onQueryChange: (query: string) => void;
  readonly onStatusChange: (status: StatusFilter) => void;
  readonly onShowHiddenChange: (showHidden: boolean) => void;
}

/** The Agents list's search box, status filter and Show hidden switch (ADR 0058). */
export const FilterBar = (props: FilterBarProps): JSX.Element => {
  const { query, status, showHidden, hiddenCount, onQueryChange, onStatusChange, onShowHiddenChange } = props;
  return (
    <div role="search" aria-label="Filter workspaces" className={styles.bar}>
      <TextField
        size="small"
        type="search"
        label="Search"
        value={query}
        onChange={(e) => onQueryChange(e.target.value)}
        className={styles.search}
        autoComplete="off"
      />
      <TextField
        select
        size="small"
        label="Status"
        slotProps={{ select: { native: true } }}
        value={status}
        onChange={(e) => onStatusChange(e.target.value as StatusFilter)}
        className={styles.status}
      >
        {statusFilterOptions.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </TextField>
      <FormControlLabel
        control={<Switch size="small" checked={showHidden} onChange={(e) => onShowHiddenChange(e.target.checked)} />}
        label={`Show hidden (${hiddenCount})`}
        className={styles.hidden}
      />
    </div>
  );
};
