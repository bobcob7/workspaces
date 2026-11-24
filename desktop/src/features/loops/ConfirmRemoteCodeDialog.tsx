import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle } from "@mui/material";
import type { JSX } from "react";
import styles from "./ConfirmRemoteCodeDialog.module.css";

export interface ConfirmRemoteCodeDialogProps {
  readonly environment: string;
  /** The loop's period, as `1h`. */
  readonly every: string;
  /** The gate script to show; empty when the script is not part of what is confirmed. */
  readonly script: string;
  /** The script is new or changed. */
  readonly scriptChanged: boolean;
  /** The loop's agent newly runs with bypassPermissions. */
  readonly bypassChanged: boolean;
  /** Called only from the Run button. */
  readonly onConfirm: () => void;
  readonly onCancel: () => void;
}

/** Asks the person to confirm code that will run on a remote environment (R3). Cancel has the focus. */
export const ConfirmRemoteCodeDialog = (props: ConfirmRemoteCodeDialogProps): JSX.Element => {
  const { environment, every, script, scriptChanged, bypassChanged, onConfirm, onCancel } = props;
  const bypassText = `This loop's agent runs on ${environment} with every permission (bypassPermissions)`;
  const scriptText = `This script runs as the agent's user on ${environment} every ${every}, outside Claude Code's permissions. Anyone who can use this app can run code there.`;
  return (
    <Dialog open onClose={onCancel} fullWidth maxWidth="sm">
      <DialogTitle>Run code on {environment}?</DialogTitle>
      <DialogContent>
        {scriptChanged && <DialogContentText>{scriptText}</DialogContentText>}
        {bypassChanged && <DialogContentText>{bypassText}</DialogContentText>}
        {script !== "" && (
          <pre className={styles.script} aria-label="Script" tabIndex={0}>
            {script}
          </pre>
        )}
      </DialogContent>
      <DialogActions>
        <Button autoFocus onClick={onCancel}>
          Cancel
        </Button>
        <Button color="error" variant="contained" onClick={onConfirm}>
          Run on {environment}
        </Button>
      </DialogActions>
    </Dialog>
  );
};
