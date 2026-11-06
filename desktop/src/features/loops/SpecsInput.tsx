import { Chip, IconButton, TextField } from "@mui/material";
import { useState, type ClipboardEvent, type JSX, type KeyboardEvent } from "react";
import styles from "./LoopsPane.module.css";

const separators = /[\s,]+/;

/** Adds each word not already in specs, in order. */
const withSpecs = (specs: readonly string[], words: readonly string[]): readonly string[] =>
  words.reduce((current, word) => (current.includes(word) ? current : [...current, word]), specs);

/** Repo specs as deletable chips. Enter, space, comma, or blur adds the typed spec;
 * Backspace in an empty field removes the last. */
export const SpecsInput = ({ value, onChange }: {
  readonly value: readonly string[];
  readonly onChange: (specs: readonly string[]) => void;
}): JSX.Element => {
  const [draft, setDraft] = useState("");
  const add = (text: string): void => {
    const words = text.split(separators).filter((word) => word !== "");
    setDraft("");
    const next = withSpecs(value, words);
    if (next !== value) {
      onChange(next);
    }
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>): void => {
    if (event.key === "Enter" || event.key === " " || event.key === ",") {
      event.preventDefault();
      add(draft);
      return;
    }
    if (event.key === "Backspace" && draft === "" && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  };
  const onPaste = (event: ClipboardEvent<HTMLDivElement>): void => {
    event.preventDefault();
    add(`${draft} ${event.clipboardData.getData("text")}`);
  };
  return (
    <div className={styles.specs}>
      {value.length > 0 && (
        <div className={styles.chips}>
          {value.map((spec) => (
            <Chip
              key={spec}
              label={spec}
              size="small"
              onDelete={() => onChange(value.filter((current) => current !== spec))}
              deleteIcon={<IconButton size="small" aria-label={`Remove ${spec}`}>×</IconButton>}
            />
          ))}
        </div>
      )}
      <TextField
        label="Repos"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={onKeyDown}
        onPaste={onPaste}
        onBlur={() => add(draft)}
        helperText="Repo specs, such as buff-bear or github.com/owner/repo@main. Press Enter to add."
        size="small"
        fullWidth
      />
    </div>
  );
};
