import { Alert, Button, Dialog, DialogActions, DialogContent, DialogTitle, TextField } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { LoopMemoryEntry } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { fetchLoopMemory, saveErrorCleared, saveLoopMemory } from "./loopDataSlice";
import styles from "./MemoryCard.module.css";

const keyPattern = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;
const valueLimit = 65_536;
const noRev = 0;

/** Add a memory key, or edit an existing one; a conflict offers a reload of its value. */
export const MemoryDialog = ({
  loopName,
  entry,
  onClose,
}: {
  readonly loopName: string;
  readonly entry: LoopMemoryEntry | null;
  readonly onClose: () => void;
}): JSX.Element => {
  const dispatch = useAppDispatch();
  const saving = useAppSelector((s) => s.loopData.saving);
  const saveError = useAppSelector((s) => s.loopData.saveError);
  const [key, setKey] = useState(entry?.key ?? "");
  const [value, setValue] = useState(entry?.value ?? "");
  const [rev, setRev] = useState(entry?.rev ?? noRev);
  const keyValid = keyPattern.test(key);
  const bytes = new TextEncoder().encode(value).length;
  const tooLarge = bytes > valueLimit;
  const conflict = saveError?.code === "memory_conflict";

  useEffect(
    () => () => {
      dispatch(saveErrorCleared());
    },
    [dispatch],
  );

  const save = async (): Promise<void> => {
    const result = await dispatch(saveLoopMemory({ name: loopName, key, value, expectRev: rev }));
    if (saveLoopMemory.fulfilled.match(result)) {
      onClose();
    }
  };

  const reload = async (): Promise<void> => {
    dispatch(saveErrorCleared());
    const result = await dispatch(fetchLoopMemory(loopName));
    if (!fetchLoopMemory.fulfilled.match(result)) {
      return;
    }
    const fresh = result.payload.entries.find((e) => e.key === key);
    setValue(fresh?.value ?? "");
    setRev(fresh?.rev ?? noRev);
  };

  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="sm" className={styles.dialog}>
      <DialogTitle>{entry === null ? "Add memory" : `Edit ${entry.key}`}</DialogTitle>
      <DialogContent className={styles.form}>
        {conflict && (
          <Alert
            severity="warning"
            action={
              <Button color="inherit" size="small" onClick={() => void reload()}>
                Reload
              </Button>
            }
          >
            A run changed {key} since you opened it.
          </Alert>
        )}
        {saveError !== null && !conflict && <Alert severity="error">{saveError.message}</Alert>}
        <TextField
          label="Key"
          value={key}
          disabled={entry !== null}
          onChange={(event) => setKey(event.target.value)}
          error={key !== "" && !keyValid}
          helperText={key !== "" && !keyValid ? "Start with a letter or _, then letters, digits or _ (64 at most)." : " "}
          slotProps={{ htmlInput: { className: styles.mono } }}
        />
        <TextField
          label="Value"
          value={value}
          multiline
          minRows={4}
          maxRows={16}
          onChange={(event) => setValue(event.target.value)}
          className={styles.mono}
          helperText={`${bytes.toLocaleString("en-US")} / ${valueLimit.toLocaleString("en-US")} bytes`}
          error={tooLarge}
        />
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="contained" disabled={saving || !keyValid || tooLarge} onClick={() => void save()}>
          Save
        </Button>
      </DialogActions>
    </Dialog>
  );
};
