import { Alert, Button, Chip, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle } from "@mui/material";
import { useEffect, useState, type JSX } from "react";
import type { ListedRole, RoleSource } from "../../api";
import { useAppDispatch, useAppSelector } from "../../store";
import { RoleEditor } from "./RoleEditor";
import { fetchRoles, removeRole } from "./settingsSlice";
import dialogStyles from "./ProfileEditor.module.css";
import styles from "./RolesGroup.module.css";

/** The badge after a role's name: none for a built-in (ADR 0050). */
const badges: Readonly<Record<RoleSource, { readonly label: string; readonly className: string } | null>> = {
  builtin: null,
  user: { label: "custom", className: styles.custom ?? "" },
  override: { label: "edited", className: styles.edited ?? "" },
};

/** The role being edited, or null for a new one. */
interface Editing {
  readonly role: ListedRole | null;
}

interface RemoveRoleDialogProps {
  readonly role: ListedRole;
  readonly onClose: () => void;
}

/**
 * Confirms `ws machine roles rm`: Remove for a custom role, which is gone
 * for good, or Reset to built-in for an edited one, whose built-in comes back.
 */
const RemoveRoleDialog = ({ role, onClose }: RemoveRoleDialogProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [removing, setRemoving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const reset = role.source === "override";
  const confirm = async (): Promise<void> => {
    setRemoving(true);
    setError(null);
    const result = await dispatch(removeRole(role.name));
    setRemoving(false);
    if (removeRole.fulfilled.match(result)) {
      onClose();
      return;
    }
    setError(result.payload?.message ?? result.error.message ?? "removing the role failed");
  };
  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{reset ? `Reset ${role.name} to built-in?` : `Remove ${role.name}?`}</DialogTitle>
      <DialogContent className={dialogStyles.content}>
        {error !== null && <Alert severity="error">{error}</Alert>}
        {reset ? (
          <p>
            Deletes your edited copy. The built-in <b>{role.name}</b> comes back, with ws&apos;s description, model, and prompt.
          </p>
        ) : (
          <p>
            Deletes the <b>{role.name}</b> role. It isn&apos;t built in, so nothing comes back.
          </p>
        )}
        <p className={dialogStyles.label}>Agents already started from this role keep the prompt they started with.</p>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button color={reset ? "primary" : "error"} variant="contained" disabled={removing} onClick={() => void confirm()}>
          {reset ? "Reset to built-in" : "Remove"}
        </Button>
      </DialogActions>
    </Dialog>
  );
};

/** Agent roles, from `ws machine roles`: the table, and the editor and remove dialogs (ADR 0050). */
export const RolesGroup = (): JSX.Element => {
  const dispatch = useAppDispatch();
  const roles = useAppSelector((s) => s.settings.roles);
  const error = useAppSelector((s) => s.settings.rolesError);
  const [editing, setEditing] = useState<Editing | null>(null);
  const [removing, setRemoving] = useState<ListedRole | null>(null);
  useEffect(() => {
    void dispatch(fetchRoles());
  }, [dispatch]);
  return (
    <section className={styles.group} aria-label="Agent roles">
      <h1 className={styles.title}>Agent roles</h1>
      <p className={styles.help}>
        What <code>ws machine spawn --role</code> starts a child as. Stored in <code>machine/roles/</code> under the ws config directory.
      </p>
      {roles === null ? (
        error === null ? (
          <CircularProgress size={20} aria-label="Loading roles" />
        ) : (
          <Alert severity="error">{error.message}</Alert>
        )
      ) : (
        <div className={styles.root}>
          <table className={styles.table} aria-label="Roles">
            <thead>
              <tr>
                <th>Role</th>
                <th>Description</th>
                <th>Model</th>
                <th>Verdicts</th>
                <th aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {roles.map((role) => {
                const badge = badges[role.source];
                return (
                  <tr key={role.name}>
                    <td className={styles.name}>
                      {role.name}
                      {badge !== null && <Chip size="small" label={badge.label} className={`${styles.badge} ${badge.className}`} />}
                    </td>
                    <td>{role.description}</td>
                    <td>{role.model === "" ? "Default" : role.model}</td>
                    <td>
                      <div className={styles.chips}>
                        {role.pass.map((verdict) => (
                          <Chip key={`pass-${verdict}`} size="small" label={verdict} className={styles.verdict} />
                        ))}
                        {role.fail.map((verdict) => (
                          <Chip key={`fail-${verdict}`} size="small" label={verdict} className={`${styles.verdict} ${styles.fail}`} />
                        ))}
                      </div>
                    </td>
                    <td className={styles.actions}>
                      <Button size="small" aria-label={`Edit ${role.name}`} onClick={() => setEditing({ role })}>
                        Edit
                      </Button>
                      {role.source !== "builtin" && (
                        <Button size="small" color="error" aria-label={`Remove ${role.name}`} onClick={() => setRemoving(role)}>
                          Remove
                        </Button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          <div className={styles.buttons}>
            <Button size="small" variant="outlined" onClick={() => setEditing({ role: null })}>
              New role
            </Button>
          </div>
        </div>
      )}
      {editing !== null && <RoleEditor role={editing.role} onClose={() => setEditing(null)} />}
      {removing !== null && <RemoveRoleDialog role={removing} onClose={() => setRemoving(null)} />}
    </section>
  );
};
