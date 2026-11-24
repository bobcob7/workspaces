import { Alert, Button, Dialog, DialogActions, DialogContent, DialogTitle } from "@mui/material";
import { useState, type JSX } from "react";
import type { RemoteEnvironment } from "../../api";
import { removeEnvironment } from "../environments/environmentsSlice";
import { useAppDispatch } from "../../store";
import dialogStyles from "./ProfileEditor.module.css";

interface RemoveEnvironmentDialogProps {
  readonly environment: RemoteEnvironment;
  readonly onClose: () => void;
}

const plural = (n: number): string => `${n} workspace${n === 1 ? "" : "s"}`;

/** Confirms `ws env rm`; a pending environment is cancelled, not removed. */
export const RemoveEnvironmentDialog = ({ environment, onClose }: RemoveEnvironmentDialogProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [removing, setRemoving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pending = environment.state === "pending";
  const confirm = async (): Promise<void> => {
    setRemoving(true);
    setError(null);
    const result = await dispatch(removeEnvironment(environment.name));
    setRemoving(false);
    if (removeEnvironment.fulfilled.match(result)) {
      onClose();
      return;
    }
    setError(result.payload?.message ?? result.error.message ?? "removing the environment failed");
  };
  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{pending ? `Cancel ${environment.name}?` : `Remove ${environment.name}?`}</DialogTitle>
      <DialogContent className={dialogStyles.content}>
        {error !== null && <Alert severity="error">{error}</Alert>}
        {pending ? (
          <p>The enrollment code for {environment.name} stops working.</p>
        ) : (
          <p>
            Revokes the certificate of <b>{environment.name}</b>, which has {plural(environment.workspaces ?? 0)}. The machine is disconnected; its
            workspaces stay on it but are no longer listed here.
          </p>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Keep</Button>
        <Button color="error" variant="contained" disabled={removing} onClick={() => void confirm()}>
          {pending ? "Cancel enrollment" : "Remove"}
        </Button>
      </DialogActions>
    </Dialog>
  );
};
