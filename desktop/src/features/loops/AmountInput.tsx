import { MenuItem, TextField } from "@mui/material";
import type { JSX } from "react";
import styles from "./LoopsPane.module.css";
import type { Amount } from "./period";

/** A whole-number TextField (type="number", inputMode numeric) and a unit select side by side. */
export const AmountInput = <U extends string>(props: {
  readonly label: string;
  readonly value: Amount<U>;
  readonly units: readonly { readonly value: U; readonly label: string }[];
  readonly onChange: (value: Amount<U>) => void;
  readonly error?: string;
  readonly helperText?: string;
}): JSX.Element => {
  const { label, value, units, onChange, error, helperText } = props;
  const selectUnit = (next: string): void => {
    const unit = units.find((candidate) => candidate.value === next);
    if (unit !== undefined) {
      onChange({ amount: value.amount, unit: unit.value });
    }
  };
  return (
    <div className={styles.amount}>
      <TextField
        label={label}
        type="number"
        value={value.amount}
        onChange={(e) => onChange({ amount: e.target.value, unit: value.unit })}
        error={error !== undefined}
        helperText={error ?? helperText}
        slotProps={{ htmlInput: { inputMode: "numeric" } }}
        size="small"
        className={styles.amountNumber}
      />
      <TextField
        select
        label="Unit"
        value={value.unit}
        onChange={(e) => selectUnit(e.target.value)}
        slotProps={{ select: { SelectDisplayProps: { "aria-labelledby": undefined, "aria-label": `${label} unit` } } }}
        size="small"
        className={styles.amountUnit}
      >
        {units.map((unit) => (
          <MenuItem key={unit.value} value={unit.value}>
            {unit.label}
          </MenuItem>
        ))}
      </TextField>
    </div>
  );
};
