import { Alert, Button, Dialog, DialogActions, DialogContent, DialogTitle, TextField } from "@mui/material";
import { useState, type FormEvent, type JSX } from "react";
import { useAppDispatch, useAppSelector } from "../../store";
import { randomToken } from "../agents/names";
import { openTerminal, shellKey } from "../terminals/terminalsSlice";
import { dialogClosed, sectionChosen, selected } from "../ui/uiSlice";
import { RepoSpecsField } from "./RepoSpecsField";
import { usePrefilledSpecs } from "./usePrefilledSpecs";
import styles from "./NewDialog.module.css";

export const NewShellDialog = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const open = useAppSelector((s) => s.ui.dialog === "shell");
  const [name, setName] = useState("");
  const [specs, setSpecs] = useState<readonly string[]>([]);
  usePrefilledSpecs(open, setSpecs);
  const submit = (event: FormEvent): void => {
    event.preventDefault();
    // ws picks the workspace id, so the terminal has a key of its own until
    // ws list shows its workspace (shellLinks).
    const key = shellKey(randomToken());
    const trimmed = name.trim();
    void dispatch(
      openTerminal({
        key,
        title: trimmed === "" ? "New shell" : trimmed,
        spec: { kind: "shell", name: trimmed, specs },
        openedAt: Date.now(),
      }),
    );
    dispatch(selected(key));
    dispatch(sectionChosen("agents"));
    dispatch(dialogClosed());
    setName("");
    setSpecs([]);
  };
  return (
    <Dialog open={open} onClose={() => dispatch(dialogClosed())} fullWidth maxWidth="sm">
      <form onSubmit={submit}>
        <DialogTitle>New shell workspace</DialogTitle>
        <DialogContent className={styles.content}>
          <TextField
            label="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="API bump"
            helperText="Shown in the list, and can be changed later. Leave empty for a generated id."
            autoFocus
          />
          <RepoSpecsField value={specs} onChange={setSpecs} />
          <Alert severity="info">
            The workspace lives as long as its shell: exiting the shell, or quitting this app, removes it.
          </Alert>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => dispatch(dialogClosed())}>Cancel</Button>
          <Button type="submit" variant="contained">
            Open shell
          </Button>
        </DialogActions>
      </form>
    </Dialog>
  );
};
