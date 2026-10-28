import { Alert, Button, Dialog, DialogActions, DialogContent, DialogTitle } from "@mui/material";
import { useState, type JSX } from "react";
import type { ListedProfile } from "../../api";
import { useAppDispatch } from "../../store";
import { removeProfile } from "./settingsSlice";
import styles from "./ProfileEditor.module.css";

export interface RemoveProfileDialogProps {
  readonly profile: ListedProfile;
  readonly onClose: () => void;
}

/**
 * Confirms `ws review profiles rm`: Delete for a custom profile, which is
 * gone for good, or Reset to built-in for an edited one, whose built-in
 * comes back.
 */
export const RemoveProfileDialog = ({ profile, onClose }: RemoveProfileDialogProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [removing, setRemoving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const reset = profile.source === "override";
  const confirm = async (): Promise<void> => {
    setRemoving(true);
    setError(null);
    const result = await dispatch(removeProfile(profile.name));
    setRemoving(false);
    if (removeProfile.fulfilled.match(result)) {
      onClose();
      return;
    }
    setError(result.payload?.message ?? result.error.message ?? "removing the profile failed");
  };
  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{reset ? `Reset ${profile.name} to built-in?` : `Delete ${profile.name}?`}</DialogTitle>
      <DialogContent className={styles.content}>
        {error !== null && <Alert severity="error">{error}</Alert>}
        {reset ? (
          <p>
            Deletes your edited copy. The built-in <b>{profile.name}</b> comes back, with ws&apos;s description, file patterns,
            and guide. Its Runs and Model here stay as they are.
          </p>
        ) : (
          <p>
            Deletes the <b>{profile.name}</b> profile from your review profiles. It isn&apos;t built in, so nothing comes back,
            and its Runs and Model here go too.
          </p>
        )}
        <p className={styles.label}>Reviews already running keep the guide they started with.</p>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button color={reset ? "primary" : "error"} variant="contained" disabled={removing} onClick={() => void confirm()}>
          {reset ? "Reset to built-in" : "Delete"}
        </Button>
      </DialogActions>
    </Dialog>
  );
};
