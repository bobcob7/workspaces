import { Alert, Button, Checkbox, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel } from "@mui/material";
import { useCallback, useEffect, useState, type JSX } from "react";
import { api as backend, toAppError, type Api, type CaInfo, type CaStatus } from "../../api";
import { rotateCa } from "../environments/environmentsSlice";
import { useAppDispatch, useAppSelector } from "../../store";
import dialogStyles from "./ProfileEditor.module.css";
import styles from "./EnvironmentsGroup.module.css";

const canCopy = (): boolean => typeof navigator !== "undefined" && navigator.clipboard !== undefined;

const dateOf = (iso: string): string => iso.slice(0, 10);

const CaLine = ({ label, ca }: { readonly label: string; readonly ca: CaInfo }): JSX.Element => (
  <div className={styles.caLine}>
    <span className={styles.caLabel}>{label}</span>
    <code className={styles.mono} aria-label={`${label} fingerprint`}>
      {ca.fingerprint.slice(0, 16)}
    </code>
    <span className={styles.muted}>expires {dateOf(ca.notAfter)}</span>
    {canCopy() && (
      <Button size="small" aria-label={`Copy ${label} fingerprint`} onClick={() => void navigator.clipboard.writeText(ca.fingerprint)}>
        Copy
      </Button>
    )}
  </div>
);

interface CaCardProps {
  readonly client?: Pick<Api, "environmentsCaStatus">;
}

/** The certificate authority that signs the environments' certificates, and its rotation (ADR 0075). */
export const CaCard = ({ client = backend }: CaCardProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const items = useAppSelector((s) => s.environments.items);
  const [status, setStatus] = useState<CaStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [force, setForce] = useState(false);
  const [busy, setBusy] = useState(false);
  const [stranded, setStranded] = useState<readonly string[]>([]);
  const load = useCallback(async (): Promise<void> => {
    try {
      setStatus(await client.environmentsCaStatus());
      setError(null);
    } catch (err: unknown) {
      setError(toAppError(err).message);
    }
  }, [client]);
  useEffect(() => {
    void load();
  }, [load]);
  const rotate = async (finish: boolean): Promise<void> => {
    setBusy(true);
    const result = await dispatch(rotateCa({ finish, force: finish && force }));
    setBusy(false);
    setConfirming(false);
    if (rotateCa.fulfilled.match(result)) {
      setError(null);
      setStranded(result.payload.stranded);
      setForce(false);
    } else {
      setError(result.payload?.message ?? result.error.message ?? "rotating the CA failed");
    }
    await load();
  };
  if (status === null) {
    return (
      <div className={styles.card} aria-label="Certificate authority">
        {error === null ? <CircularProgress size={18} aria-label="Loading the CA" /> : <Alert severity="error">{error}</Alert>}
      </div>
    );
  }
  const onOld = status.environmentsOnOld;
  const oldNames = items.filter((env) => env.certCa === "old").map((env) => env.name);
  const finishBlocked = onOld > 0 && !force;
  return (
    <div className={styles.card} role="group" aria-label="Certificate authority">
      <h2 className={styles.subtitle}>Certificate authority</h2>
      {error !== null && <Alert severity="error">{error}</Alert>}
      {status.warnings.map((warning) => (
        <Alert key={warning} severity="warning">
          {warning}
        </Alert>
      ))}
      {status.current === null ? <p className={styles.muted}>No CA yet; adding the first environment creates it.</p> : <CaLine label="Current CA" ca={status.current} />}
      {status.old !== null && (
        <>
          <CaLine label="Old CA" ca={status.old} />
          <p className={styles.muted}>{onOld} environments still on the old CA</p>
        </>
      )}
      {stranded.length > 0 && <Alert severity="warning">Stranded, enroll again: {stranded.join(", ")}</Alert>}
      <div className={styles.buttons}>
        <Button size="small" variant="outlined" disabled={busy || status.old !== null} onClick={() => setConfirming(true)}>
          Rotate CA
        </Button>
        {status.old !== null && (
          <Button size="small" variant="outlined" disabled={busy || finishBlocked} onClick={() => void rotate(true)}>
            Finish rotation
          </Button>
        )}
      </div>
      {status.old !== null && onOld > 0 && (
        <>
          <p className={styles.muted}>{onOld} environments still use the old CA</p>
          <FormControlLabel
            control={<Checkbox size="small" checked={force} onChange={(e) => setForce(e.target.checked)} />}
            label={`Force${oldNames.length > 0 ? `: ${oldNames.join(", ")} will stop connecting` : ""}`}
          />
        </>
      )}
      {confirming && (
        <Dialog open onClose={() => setConfirming(false)} fullWidth maxWidth="sm">
          <DialogTitle>Rotate the CA?</DialogTitle>
          <DialogContent className={dialogStyles.content}>
            <p>A new CA signs new certificates. Environments renew onto it on their own; finish the rotation once none use the old one.</p>
          </DialogContent>
          <DialogActions>
            <Button onClick={() => setConfirming(false)}>Cancel</Button>
            <Button variant="contained" disabled={busy} onClick={() => void rotate(false)}>
              Rotate CA
            </Button>
          </DialogActions>
        </Dialog>
      )}
    </div>
  );
};
