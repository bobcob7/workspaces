import { Button, IconButton, TextField } from "@mui/material";
import type { JSX } from "react";
import type { LoopVar } from "../../api";
import styles from "./LoopsPane.module.css";

const blankRow: LoopVar = { key: "", value: "" };

/** Key-value rows for a loop's variables. */
export const VarsEditor = ({ rows, onChange }: {
  readonly rows: readonly LoopVar[];
  readonly onChange: (rows: readonly LoopVar[]) => void;
}): JSX.Element => {
  const replaceRow = (index: number, row: LoopVar): void => {
    onChange(rows.map((current, i) => (i === index ? row : current)));
  };
  return (
    <div role="group" aria-label="Variables" className={styles.vars}>
      {rows.map((row, index) => (
        <div key={index} className={styles.varRow}>
          <TextField
            label="Key"
            value={row.key}
            onChange={(e) => replaceRow(index, { ...row, key: e.target.value })}
            slotProps={{ htmlInput: { "aria-label": `Key ${index + 1}` } }}
            size="small"
            fullWidth
          />
          <TextField
            label="Value"
            value={row.value}
            onChange={(e) => replaceRow(index, { ...row, value: e.target.value })}
            slotProps={{ htmlInput: { "aria-label": `Value ${index + 1}` } }}
            size="small"
            fullWidth
          />
          <IconButton
            size="small"
            aria-label={`Remove variable ${index + 1}`}
            onClick={() => onChange(rows.filter((_, i) => i !== index))}
          >
            ×
          </IconButton>
        </div>
      ))}
      <div>
        <Button size="small" onClick={() => onChange([...rows, blankRow])}>
          Add variable
        </Button>
      </div>
    </div>
  );
};
