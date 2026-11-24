import { Alert, Button, Dialog, DialogActions, DialogContent, DialogTitle, TextField } from "@mui/material";
import { useState, type JSX } from "react";
import { api as backend, toAppError, type Api, type EnrollmentCode as Code } from "../../api";
import { fetchEnvironments } from "../environments/environmentsSlice";
import { useAppDispatch } from "../../store";
import { EnrollmentCode } from "./EnrollmentCode";
import dialogStyles from "./ProfileEditor.module.css";

/** The shape `ws env add` accepts for a name. */
export const environmentNamePattern = /^[a-z][a-z0-9-]{0,31}$/;

/** Why a name can't be used, or null. The server's `name_invalid` is shown too if this lets one through. */
export const nameProblem = (name: string): string | null => {
  if (name === "local") {
    return "local is this machine; pick another name";
  }
  if (!environmentNamePattern.test(name)) {
    return "Use a lowercase letter, then lowercase letters, digits or -, up to 32 characters";
  }
  return null;
};

type Issuer = Pick<Api, "environmentAdd" | "environmentRotate">;

interface AddEnvironmentDialogProps {
  /** Rotates this environment's code instead of adding one; a confirmation comes first. */
  readonly rotate?: string;
  readonly onClose: () => void;
  /** The backend calls; a test passes a fake so the code never touches a store. */
  readonly client?: Issuer;
}

interface Failure {
  readonly field: string | null;
  readonly general: string | null;
}

const failureOf = (err: unknown): Failure => {
  const error = toAppError(err);
  if (error.code === "name_invalid") {
    return { field: error.message, general: null };
  }
  if (error.code === "usage") {
    return { field: null, general: "Set Public name below first." };
  }
  return { field: null, general: error.message };
};

/**
 * Adds an environment, or rotates one's code, and shows the one-time code.
 * The code lives only in this component's state (D5), so closing the dialog forgets it.
 */
export const AddEnvironmentDialog = ({ rotate, onClose, client = backend }: AddEnvironmentDialogProps): JSX.Element => {
  const dispatch = useAppDispatch();
  const [name, setName] = useState("");
  const [code, setCode] = useState<Code | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<Failure | null>(null);
  const problem = name === "" ? null : nameProblem(name);
  const submit = async (): Promise<void> => {
    setBusy(true);
    setFailure(null);
    try {
      setCode(rotate === undefined ? await client.environmentAdd(name) : await client.environmentRotate(rotate));
      void dispatch(fetchEnvironments());
    } catch (err: unknown) {
      setFailure(failureOf(err));
    } finally {
      setBusy(false);
    }
  };
  const title = rotate === undefined ? "Add environment" : `Rotate ${rotate}?`;
  return (
    <Dialog open onClose={onClose} fullWidth maxWidth="sm" aria-labelledby="environment-code-title">
      <DialogTitle id="environment-code-title">{code === null ? title : `Enroll ${code.name}`}</DialogTitle>
      <DialogContent className={dialogStyles.content}>
        {failure?.general != null && <Alert severity="error">{failure.general}</Alert>}
        {code !== null ? (
          <EnrollmentCode code={code} />
        ) : rotate === undefined ? (
          <TextField
            autoFocus
            size="small"
            label="Name"
            value={name}
            error={problem !== null || failure?.field != null}
            helperText={problem ?? failure?.field ?? "The name the machine goes by: --env <name>"}
            onChange={(e) => setName(e.target.value)}
          />
        ) : (
          <p>The old code or certificate stops working. The machine must enroll again with the new code.</p>
        )}
      </DialogContent>
      <DialogActions>
        {code === null ? (
          <>
            <Button onClick={onClose}>Cancel</Button>
            <Button
              variant="contained"
              disabled={busy || (rotate === undefined && (name === "" || problem !== null))}
              onClick={() => void submit()}
            >
              {rotate === undefined ? "Add" : "Rotate"}
            </Button>
          </>
        ) : (
          <Button variant="contained" onClick={onClose}>
            Done
          </Button>
        )}
      </DialogActions>
    </Dialog>
  );
};
