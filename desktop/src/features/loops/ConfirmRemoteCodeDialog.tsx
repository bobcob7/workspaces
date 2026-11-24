import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle } from "@mui/material";
import type { JSX } from "react";
import styles from "./ConfirmRemoteCodeDialog.module.css";

export interface ConfirmRemoteCodeDialogProps {
  readonly environment: string;
  /** The loop's period, as `1h`. */
  readonly every: string;
  /** The gate script to show; empty for a bypass-only change. */
  readonly script: string;
  /** The loop's agent runs with bypassPermissions. */
  readonly bypass: boolean;
  /** Called only from the Run button. */
  readonly onConfirm: () => void;
  readonly onCancel: () => void;
}

/** Asks the person to confirm code that will run on a remote environment (R3). Cancel has the focus. */
export const ConfirmRemoteCodeDialog = (props: ConfirmRemoteCodeDialogProps): JSX.Element => {
  const { environment, every, script, bypass, onConfirm, onCancel } = props;
  const body = bypass
    ? `This loop's agent runs on ${environment} with every permission (bypassPermissions)`
    : `This script runs as the agent's user on ${environment} every ${every}, outside Claude Code's permissions. Anyone who can use this app can run code there.`;
  return (
    <Dialog open onClose={onCancel} fullWidth maxWidth="sm">
      <DialogTitle>Run code on {environment}?</DialogTitle>
      <DialogContent>
        <DialogContentText>{body}</DialogContentText>
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
